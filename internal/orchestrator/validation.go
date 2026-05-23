package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// WorkflowValidation configures artifact checks for a workflow template (rig-flow, etc.).
// Operators edit these fields in orchestrator/templates/*.yaml when changing SPEC scope.
type WorkflowValidation struct {
	LayoutRoot           string   `yaml:"layout_root" json:"layout_root"`
	BeadTitleContains    string   `yaml:"bead_title_contains" json:"bead_title_contains"`
	UnittestModule       string   `yaml:"unittest_module" json:"unittest_module"`
	QAVerifyCommand      string   `yaml:"qa_verify_command" json:"qa_verify_command"`
	TestRunner           string   `yaml:"test_runner" json:"test_runner"`
	RequiredFiles        []string         `yaml:"required_files" json:"required_files"`
	DeliveryPhases       []DeliveryPhase  `yaml:"delivery_phases" json:"delivery_phases,omitempty"`
	ActivePhaseIDField   string           `yaml:"active_phase_id" json:"active_phase_id,omitempty"`
	SpecSummary                string   `yaml:"spec_summary" json:"spec_summary"`
	MinArchitectureBytes       int64    `yaml:"min_architecture_bytes" json:"min_architecture_bytes"`
	MinPlanBytes               int64    `yaml:"min_plan_bytes" json:"min_plan_bytes"`
	MinImplementationFileBytes int64    `yaml:"min_implementation_file_bytes" json:"min_implementation_file_bytes"`
	MinSubstantiveLines        int      `yaml:"min_substantive_lines" json:"min_substantive_lines"`
	// PythonVenvDir is the venv directory under mayor/rig (default ".venv"). Set "off" to disable.
	PythonVenvDir string `yaml:"python_venv_dir" json:"python_venv_dir"`
}

// Artifact size guard defaults for rig-flow (gt rig spec-index / workflow-profile.json).
// LLMs often emit min_plan_bytes near the full SPEC size; ClampProfileValidation corrects that.
const (
	MinArtifactBytesFloor       int64 = 200
	DefaultMinPlanBytes         int64 = 2500
	MaxMinPlanBytes             int64 = 4096
	DefaultMinArchitectureBytes int64 = 4000
	MaxMinArchitectureBytes     int64 = 8192
	// SmallRigMaxArchitectureBytes caps min_architecture_bytes when the profile lists few files
	// (e.g. Link Shelf). Spec-index often requests 8k+; a complete doc for 7 paths is ~3–4k.
	SmallRigMaxArchitectureBytes int64 = 3200
	smallRigRequiredFileCap        = 10
)

// DefaultWorkflowValidation returns minimal rig-flow defaults when YAML/profile omit validation.
// Per-rig values should come from mayor/rig/.gastown/workflow-profile.json (gt rig spec-index).
func DefaultWorkflowValidation() WorkflowValidation {
	return WorkflowValidation{
		BeadTitleContains:    "Implement ",
		MinArchitectureBytes: MinArtifactBytesFloor,
		MinPlanBytes:         MinArtifactBytesFloor,
	}
}

// MinPlanBytesFromArchitecture returns the minimum plan.md size: half of architecture bytes (floored/capped).
func MinPlanBytesFromArchitecture(architectureBytes int64) int64 {
	if architectureBytes < 0 {
		architectureBytes = 0
	}
	n := architectureBytes / 2
	if n < MinArtifactBytesFloor {
		n = MinArtifactBytesFloor
	}
	if n > MaxMinPlanBytes {
		n = MaxMinPlanBytes
	}
	return n
}

// phasedPlanByteScale returns the fraction of architecture.md that applies to plan.md sizing.
// With delivery_phases, planning covers only ActiveRequiredFiles — not the full union.
func (v WorkflowValidation) phasedPlanByteScale() float64 {
	if !v.HasPhasedDelivery() {
		return 1
	}
	total := len(v.UnionRequiredFiles())
	active := len(v.ActiveRequiredFiles())
	if total == 0 || active == 0 || active >= total {
		return 1
	}
	ratio := float64(active) / float64(total)
	const minRatio = 0.12 // avoid tiny plans when a phase has only a few paths
	if ratio < minRatio {
		ratio = minRatio
	}
	return ratio
}

// EffectiveMinPlanBytes returns the plan.md minimum for a rig: half of on-disk architecture.md when present
// (scaled by active delivery phase when phased), otherwise half of min_architecture_bytes from the profile.
func EffectiveMinPlanBytes(rigDir string, v WorkflowValidation) int64 {
	var archBytes int64
	archPath := filepath.Join(rigDir, "architecture.md")
	if info, err := os.Stat(archPath); err == nil && info.Size() > 0 {
		archBytes = info.Size()
	} else if v.MinArchitectureBytes > 0 {
		archBytes = v.MinArchitectureBytes
	} else {
		return MinPlanBytesFromArchitecture(0)
	}
	scaled := int64(float64(archBytes) * v.phasedPlanByteScale())
	return MinPlanBytesFromArchitecture(scaled)
}

// PlanMinSizeHint describes how EffectiveMinPlanBytes was derived (for errors and prompts).
func (v WorkflowValidation) PlanMinSizeHint() string {
	if v.HasPhasedDelivery() && len(v.ActiveRequiredFiles()) < len(v.UnionRequiredFiles()) {
		id := v.ActivePhaseID()
		if id == "" {
			if p, ok := v.ActivePhase(); ok {
				id = strings.TrimSpace(p.ID)
			}
		}
		if id != "" {
			return fmt.Sprintf("half of architecture.md scaled for delivery phase %q", id)
		}
		return "half of architecture.md scaled for active delivery phase"
	}
	return "half of architecture.md"
}

// ClampProfileValidation normalizes min_*_bytes from spec-index LLM output or hand-edited profiles.
func ClampProfileValidation(v WorkflowValidation) WorkflowValidation {
	v.MinArchitectureBytes = clampArtifactBytes(v.MinArchitectureBytes, DefaultMinArchitectureBytes, MinArtifactBytesFloor, MaxMinArchitectureBytes)
	v.MinImplementationFileBytes = clampArtifactBytes(
		v.MinImplementationFileBytes, DefaultMinImplementationFileBytes, MinImplementationFileBytesFloor, MaxMinImplementationFileBytes,
	)
	if v.MinSubstantiveLines < 1 {
		v.MinSubstantiveLines = DefaultMinSubstantiveLines
	}
	if v.MinSubstantiveLines > 20 {
		v.MinSubstantiveLines = DefaultMinSubstantiveLines
	}
	v = capArchitectureBytesForSmallRig(v)
	v.MinPlanBytes = MinPlanBytesFromArchitecture(v.MinArchitectureBytes)
	v = FinalizeDeliveryPhases(v)
	v = InjectSQLiteSchemaBead(v)
	v = SanitizeRigFlowProfile(v)
	return v
}

// ClampProfileValidationForRig applies ClampProfileValidation and, when architecture.md exists,
// aligns layout_root with paths documented in the architecture (flat mayor/rig worktrees).
func ClampProfileValidationForRig(townRoot, rig string, v WorkflowValidation) WorkflowValidation {
	v = ClampProfileValidation(v)
	if townRoot == "" || rig == "" {
		return v
	}
	archPath := filepath.Join(townRoot, rig, "mayor", "rig", "architecture.md")
	return AlignProfileLayoutWithArchitecture(v, archPath)
}

// capArchitectureBytesForSmallRig lowers min_architecture_bytes when required_files is a short list.
func capArchitectureBytesForSmallRig(v WorkflowValidation) WorkflowValidation {
	n := len(v.RequiredFiles)
	if v.HasPhasedDelivery() {
		if active := v.ActiveRequiredFiles(); len(active) > 0 {
			n = len(active)
		}
	}
	if n == 0 || n > smallRigRequiredFileCap {
		return v
	}
	if v.MinArchitectureBytes > SmallRigMaxArchitectureBytes {
		v.MinArchitectureBytes = SmallRigMaxArchitectureBytes
	}
	return v
}

// NormalizeLayoutProfile prefixes required_files with layout_root and ensures Go
// qa_verify_command runs from the module directory when the LLM omitted cd.
func NormalizeLayoutProfile(v WorkflowValidation) WorkflowValidation {
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" || layout == "." {
		return v
	}
	if len(v.RequiredFiles) > 0 {
		out := make([]string, 0, len(v.RequiredFiles))
		for _, f := range v.RequiredFiles {
			f = filepath.ToSlash(strings.TrimSpace(f))
			if f == "" {
				continue
			}
			if !strings.HasPrefix(f, layout+"/") && !strings.Contains(f, "..") {
				f = layout + "/" + strings.TrimPrefix(f, "/")
			}
			out = append(out, f)
		}
		v.RequiredFiles = out
	}
	v = NormalizeDeliveryPhasesLayout(v)
	qa := strings.TrimSpace(v.QAVerifyCommand)
	if qa != "" && WorkflowUsesGo(v) {
		lower := strings.ToLower(qa)
		cdLayout := "cd " + layout
		if !strings.Contains(lower, cdLayout) && !strings.Contains(lower, "cd ./"+layout) {
			if !strings.Contains(lower, "cd ") {
				v.QAVerifyCommand = cdLayout + " && " + qa
			}
		}
	}
	return v
}

func clampArtifactBytes(value, defaultVal, floor, ceiling int64) int64 {
	if value <= 0 {
		return defaultVal
	}
	if value < floor {
		return floor
	}
	if value > ceiling {
		return defaultVal
	}
	return value
}

// WithDefaults fills empty fields from DefaultWorkflowValidation.
func (v WorkflowValidation) WithDefaults() WorkflowValidation {
	d := DefaultWorkflowValidation()
	if v.BeadTitleContains == "" {
		v.BeadTitleContains = d.BeadTitleContains
	}
	hasCustomQA := strings.TrimSpace(v.QAVerifyCommand) != "" ||
		strings.EqualFold(strings.TrimSpace(v.TestRunner), "pytest") ||
		strings.EqualFold(strings.TrimSpace(v.TestRunner), "custom")
	if v.UnittestModule == "" && !hasCustomQA {
		v.UnittestModule = d.UnittestModule
	}
	if len(v.RequiredFiles) == 0 {
		v.RequiredFiles = append([]string(nil), d.RequiredFiles...)
	}
	if v.MinArchitectureBytes <= 0 {
		v.MinArchitectureBytes = d.MinArchitectureBytes
	}
	if v.MinPlanBytes <= 0 {
		v.MinPlanBytes = d.MinPlanBytes
	}
	return v
}

// MergeValidation overlays template and task validation onto defaults.
func MergeValidation(tpl *WorkflowTemplate, task *Task) WorkflowValidation {
	v := DefaultWorkflowValidation()
	if tpl != nil {
		v = mergeValidationFields(v, tpl.Validation)
	}
	if task != nil {
		v = mergeValidationFields(v, task.Validation)
	}
	return v.WithDefaults()
}

func mergeValidationFields(base, overlay WorkflowValidation) WorkflowValidation {
	if overlay.LayoutRoot != "" {
		base.LayoutRoot = overlay.LayoutRoot
	}
	if overlay.BeadTitleContains != "" {
		base.BeadTitleContains = overlay.BeadTitleContains
	}
	if overlay.UnittestModule != "" {
		base.UnittestModule = overlay.UnittestModule
	}
	if overlay.QAVerifyCommand != "" {
		base.QAVerifyCommand = overlay.QAVerifyCommand
	}
	if overlay.TestRunner != "" {
		base.TestRunner = overlay.TestRunner
	}
	if overlay.SpecSummary != "" {
		base.SpecSummary = overlay.SpecSummary
	}
	if len(overlay.RequiredFiles) > 0 {
		base.RequiredFiles = append([]string(nil), overlay.RequiredFiles...)
	}
	if len(overlay.DeliveryPhases) > 0 {
		base.DeliveryPhases = append([]DeliveryPhase(nil), overlay.DeliveryPhases...)
	}
	if overlay.ActivePhaseIDField != "" {
		base.ActivePhaseIDField = overlay.ActivePhaseIDField
	}
	if overlay.MinArchitectureBytes > 0 {
		base.MinArchitectureBytes = overlay.MinArchitectureBytes
	}
	if overlay.MinPlanBytes > 0 {
		base.MinPlanBytes = overlay.MinPlanBytes
	}
	if overlay.MinImplementationFileBytes > 0 {
		base.MinImplementationFileBytes = overlay.MinImplementationFileBytes
	}
	if overlay.MinSubstantiveLines > 0 {
		base.MinSubstantiveLines = overlay.MinSubstantiveLines
	}
	if overlay.PythonVenvDir != "" {
		base.PythonVenvDir = overlay.PythonVenvDir
	}
	base.QAVerifyCommand = NormalizePytestCommand(strings.TrimSpace(base.QAVerifyCommand))
	return base
}

// SubstituteVars replaces {{key}} in validation string fields.
func (v WorkflowValidation) SubstituteVars(vars map[string]string) WorkflowValidation {
	if len(vars) == 0 {
		return v
	}
	v.LayoutRoot = SubstituteVars(v.LayoutRoot, vars)
	v.BeadTitleContains = SubstituteVars(v.BeadTitleContains, vars)
	v.UnittestModule = SubstituteVars(v.UnittestModule, vars)
	v.QAVerifyCommand = SubstituteVars(v.QAVerifyCommand, vars)
	v.TestRunner = SubstituteVars(v.TestRunner, vars)
	v.SpecSummary = SubstituteVars(v.SpecSummary, vars)
	for i, f := range v.RequiredFiles {
		v.RequiredFiles[i] = SubstituteVars(f, vars)
	}
	for i := range v.DeliveryPhases {
		v.DeliveryPhases[i].ID = SubstituteVars(v.DeliveryPhases[i].ID, vars)
		v.DeliveryPhases[i].Title = SubstituteVars(v.DeliveryPhases[i].Title, vars)
		v.DeliveryPhases[i].QAVerifyCommand = SubstituteVars(v.DeliveryPhases[i].QAVerifyCommand, vars)
		v.DeliveryPhases[i].SpecFocus = SubstituteVars(v.DeliveryPhases[i].SpecFocus, vars)
		for j, f := range v.DeliveryPhases[i].RequiredFiles {
			v.DeliveryPhases[i].RequiredFiles[j] = SubstituteVars(f, vars)
		}
	}
	v.ActivePhaseIDField = SubstituteVars(v.ActivePhaseIDField, vars)
	return v
}

// RequirementsFilePath returns the first requirements.txt or pyproject.toml from the profile, if any.
func (v WorkflowValidation) RequirementsFilePath() string {
	for _, f := range v.RequiredFiles {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if strings.HasSuffix(f, "requirements.txt") || strings.HasSuffix(f, "pyproject.toml") {
			return f
		}
	}
	return ""
}

// LayoutRootDir returns the profile layout_root for path hints, or "." when unset.
func (v WorkflowValidation) LayoutRootDir() string {
	if l := strings.TrimSpace(v.LayoutRoot); l != "" {
		return l
	}
	return "."
}

// PromptVars returns keys for {{bead_title_contains}}, {{unittest_module}}, etc. in prompt files.
func (v WorkflowValidation) PromptVars() map[string]string {
	req := v.RequirementsFilePath()
	scoped := v.ForActivePhase()
	activeFiles := scoped.RequiredFiles
	allFiles := v.UnionRequiredFiles()
	if len(allFiles) == 0 {
		allFiles = append([]string(nil), activeFiles...)
	}
	phaseQA := scoped.QAVerifyCommand
	activeID := v.ActivePhaseID()
	activeTitle := ""
	if p, ok := v.ActivePhase(); ok {
		activeTitle = strings.TrimSpace(p.Title)
		if activeID == "" {
			activeID = strings.TrimSpace(p.ID)
		}
	}
	return map[string]string{
		"layout_root":             v.LayoutRoot,
		"bead_title_contains":     v.BeadTitleContains,
		"unittest_module":         v.UnittestModule,
		"qa_verify_command":       phaseQA,
		"phase_qa_verify_command": phaseQA,
		"test_runner":             v.TestRunner,
		"required_files":          strings.Join(activeFiles, ", "),
		"all_required_files":      strings.Join(allFiles, ", "),
		"active_phase_id":         activeID,
		"active_phase_title":      activeTitle,
		"delivery_phase_count":    fmt.Sprintf("%d", len(v.DeliveryPhases)),
		"phase_scope_note":        v.PhaseScopeNote(),
		"requirements_file":       req,
		"spec_summary":            v.SpecSummary,
		"unittest_command_hint":     v.UnittestCommandHint(),
		"implementation_verify_hint": "(resolved per rig at fetch_task — use go build until server main exists)",
		"project_setup_verify_hint": v.ProjectSetupVerifyHint(),
		"python_venv_dir":         v.PythonVenvRelDir(),
		"min_architecture_bytes":        fmt.Sprintf("%d", v.MinArchitectureBytes),
		"min_plan_bytes":                fmt.Sprintf("%d", v.MinPlanBytes),
		"min_implementation_file_bytes": fmt.Sprintf("%d", StubCheckOptionsFromValidation(v).MinFileBytes),
		"min_substantive_lines":         fmt.Sprintf("%d", StubCheckOptionsFromValidation(v).MinSubstantiveLines),
		"bead_id_example":                  beadIDExample(v),
		"static_url_contract_guidance":     RigFlowStaticURLContractGuidance,
		"static_url_contract_short":        RigFlowStaticURLContractShort,
	}
}

func beadIDExample(v WorkflowValidation) string {
	// Filled from rig prefix at payload build when available; fallback for templates.
	if p := strings.TrimSpace(v.BeadTitleContains); p != "" {
		return "<id-from-bd-list>"
	}
	return "<id-from-bd-list>"
}

// ForbiddenRigRootBasenames lists mayor/rig files that must not exist outside subdirs during design
// (e.g. layout_root/pkg/module.py → forbid module.py at rig root).
func (v WorkflowValidation) ForbiddenRigRootBasenames() []string {
	seen := make(map[string]bool)
	var out []string
	for _, f := range v.RequiredFiles {
		if strings.Contains(f, "/") {
			base := filepath.Base(f)
			if base != "" && !seen[base] {
				seen[base] = true
				out = append(out, base)
			}
		}
	}
	return out
}

// ImplementationVerifyHint returns verify text for polecat prompts (system/failure hints).
// Always compile-only — per-bead verify (incl. go run/curl on non-go.mod beads) is enforced by gt-agent.
func (v WorkflowValidation) ImplementationVerifyHint(mayorRigDir string) string {
	if WorkflowUsesGo(v) {
		return GoCompileOnlyVerifyCommand(v)
	}
	if WorkflowUsesPython(v) {
		return PythonVerifyCommand(v)
	}
	if WorkflowUsesDocker(v) {
		return DockerImplementationVerifyCommandForBead(v.ForActivePhase(), mayorRigDir, "")
	}
	return v.UnittestCommandHint()
}

// ProjectSetupVerifyHint returns the verify command agents should run in project_setup.
func (v WorkflowValidation) ProjectSetupVerifyHint() string {
	if WorkflowUsesGo(v) {
		return GoProjectSetupVerifyCommand(v)
	}
	if WorkflowUsesPython(v) {
		return PythonProjectSetupVerifyCommand(v)
	}
	if WorkflowUsesDocker(v) {
		scoped := v.ForActivePhase()
		layout := strings.Trim(strings.TrimSpace(scoped.LayoutRoot), "/")
		if layout == "" {
			layout = "."
		}
		return dockerVerifyWithLayout(scoped.ActivePhaseQAVerifyCommand(), layout)
	}
	return v.UnittestCommandHint()
}

// UnittestCommandHint returns the suggested QA command for error messages.
func (v WorkflowValidation) UnittestCommandHint() string {
	if q := strings.TrimSpace(v.QAVerifyCommand); q != "" {
		return NormalizePytestCommand(q)
	}
	mod := strings.TrimSpace(v.UnittestModule)
	if mod == "" {
		mod = DefaultWorkflowValidation().UnittestModule
	}
	return "python3 -m unittest " + mod
}

// NormalizePytestCommand rewrites bare `pytest` to `python3 -m pytest` for agent PATHs without a pytest shim.
func NormalizePytestCommand(cmd string) string {
	lower := strings.ToLower(cmd)
	if !strings.Contains(lower, "pytest") {
		return cmd
	}
	if strings.Contains(lower, "import pytest") {
		return cmd
	}
	if strings.Contains(lower, "-c ") && strings.Contains(lower, "import ") {
		return cmd
	}
	if strings.Contains(lower, "python3 -m pytest") || strings.Contains(lower, "python -m pytest") {
		return cmd
	}
	re := regexp.MustCompile(`(?i)(^|[;&|]\s*|\s+)pytest\b`)
	return re.ReplaceAllString(cmd, `${1}python3 -m pytest`)
}

// NormalizePipCommand rewrites bare `pip` to `python3 -m pip` when pip is not on PATH but python is.
func NormalizePipCommand(cmd string) string {
	lower := strings.ToLower(cmd)
	if !strings.Contains(lower, "pip") {
		return cmd
	}
	if strings.Contains(lower, ".venv/bin/pip") || strings.Contains(lower, "/bin/pip install") {
		return cmd
	}
	if strings.Contains(lower, "python3 -m pip") || strings.Contains(lower, "python -m pip") {
		return cmd
	}
	// "pip install --upgrade pip" — final pip is the package name, not the CLI.
	if regexp.MustCompile(`(?i)(^|[;&|]\s*)pip\s+install\b`).MatchString(lower) &&
		regexp.MustCompile(`(?i)\binstall\s+(\S+\s+)*pip\s*$`).MatchString(strings.TrimSpace(lower)) {
		return cmd
	}
	if regexp.MustCompile(`(?i)(^|[;&|]\s*)pip\s+install\s+--upgrade\s+pip\s*$`).MatchString(strings.TrimSpace(lower)) {
		return cmd
	}
	re := regexp.MustCompile(`(?i)(^|[;&|]\s*|\s+)pip\b`)
	return re.ReplaceAllString(cmd, `${1}python3 -m pip`)
}
