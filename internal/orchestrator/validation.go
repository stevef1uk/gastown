package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
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
	ActivePhaseIDField      string `yaml:"active_phase_id" json:"active_phase_id,omitempty"`
	RewoundFromPhaseIDField string `yaml:"rewound_from_phase_id,omitempty" json:"rewound_from_phase_id,omitempty"`
	SpecSummary                string   `yaml:"spec_summary" json:"spec_summary"`
	MinArchitectureBytes       int64    `yaml:"min_architecture_bytes" json:"min_architecture_bytes"`
	MinPlanBytes               int64    `yaml:"min_plan_bytes" json:"min_plan_bytes"`
	MinImplementationFileBytes int64    `yaml:"min_implementation_file_bytes" json:"min_implementation_file_bytes"`
	MinSubstantiveLines        int      `yaml:"min_substantive_lines" json:"min_substantive_lines"`
	// PythonVenvDir is the venv directory under mayor/rig (default ".venv"). Set "off" to disable.
	PythonVenvDir string `yaml:"python_venv_dir" json:"python_venv_dir"`
	// DevServerPort is the port the dev server listens on when the project is a web server.
	// 0 means the project is not a server (CLI, library) — no port cleanup needed.
	// Set during spec-index from SPEC.md; used by StopDevServersForRig.
	DevServerPort int `yaml:"dev_server_port" json:"dev_server_port"`
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

// MinPlanBytesFromArchitecture returns the minimum plan.md size: quarter of architecture bytes (floored/capped).
func MinPlanBytesFromArchitecture(architectureBytes int64) int64 {
	if architectureBytes < 0 {
		architectureBytes = 0
	}
	n := architectureBytes / 4
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

// EffectiveMinPlanBytes returns the plan.md minimum for a rig: quarter of on-disk architecture.md when present
// (scaled by active delivery phase when phased), otherwise quarter of min_architecture_bytes from the profile.
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
			return fmt.Sprintf("quarter of architecture.md scaled for delivery phase %q", id)
		}
		return "quarter of architecture.md scaled for active delivery phase"
	}
	return "quarter of architecture.md"
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
	minPlan := MinPlanBytesFromArchitecture(v.MinArchitectureBytes)
	if v.MinPlanBytes < MinArtifactBytesFloor || v.MinPlanBytes > minPlan {
		v.MinPlanBytes = minPlan
	}
	v = FinalizeDeliveryPhases(v)
	v = StripInvalidCDPrefixes(v)
	v = validatePhaseVerifyCommands(v)
	v.RequiredFiles = deduplicateRequiredFiles(v.RequiredFiles)
	for i := range v.DeliveryPhases {
		v.DeliveryPhases[i].RequiredFiles = deduplicateRequiredFiles(v.DeliveryPhases[i].RequiredFiles)
	}
	v = InjectSQLiteSchemaBead(v)
	v = SanitizeRigFlowProfile(v)
	v = ValidateDeliveryPhases(v)
	return v
}

// StripInvalidCDPrefixes removes leading "cd <dir> && " from verify commands when layout_root
// is empty (".") and <dir> is not a real subdirectory of the project. This catches LLM
// output that uses the rig name (e.g. "cd finally") instead of layout_root.
func StripInvalidCDPrefixes(v WorkflowValidation) WorkflowValidation {
	if v.LayoutRoot != "" {
		return v
	}
	topDirs := make(map[string]bool)
	for _, f := range v.RequiredFiles {
		if idx := strings.IndexByte(f, '/'); idx > 0 {
			topDirs[f[:idx]] = true
		}
	}

	v.QAVerifyCommand = stripBogusLeadCD(v.QAVerifyCommand, topDirs)
	for i := range v.DeliveryPhases {
		v.DeliveryPhases[i].QAVerifyCommand = stripBogusLeadCD(v.DeliveryPhases[i].QAVerifyCommand, topDirs)
	}
	return v
}

// stripBogusLeadCD checks whether cmd starts with "cd <dir> && " where <dir>
// is not a valid top-level project directory; if so, strips the cd prefix.
func stripBogusLeadCD(cmd string, validTopDirs map[string]bool) string {
	cmd = strings.TrimSpace(cmd)
	if !strings.HasPrefix(cmd, "cd ") {
		return cmd
	}
	rest := cmd[3:]
	spaceIdx := strings.IndexByte(rest, ' ')
	if spaceIdx < 0 {
		return cmd
	}
	dir := rest[:spaceIdx]

	firstComp := dir
	if slashIdx := strings.IndexByte(dir, '/'); slashIdx >= 0 {
		firstComp = dir[:slashIdx]
	}

	if validTopDirs[firstComp] || firstComp == "." || firstComp == ".." {
		return cmd
	}

	after := strings.TrimSpace(rest[spaceIdx:])
	after = strings.TrimPrefix(after, "&& ")
	after = strings.TrimSpace(after)
	if after == "" {
		return cmd
	}
	return after
}

// validatePhaseVerifyCommands ensures each delivery phase's QA verify command can run
// with the files provided by that phase and its dependencies. If a phase's command
// references tools/scripts not available (e.g., "npm test" without test script in
// package.json), it adds the missing files to the phase's required_files.
func validatePhaseVerifyCommands(v WorkflowValidation) WorkflowValidation {
	if !v.HasPhasedDelivery() {
		return v
	}
	for i := range v.DeliveryPhases {
		cmd := strings.TrimSpace(v.DeliveryPhases[i].QAVerifyCommand)
		if cmd == "" {
			continue
		}
		// npm test requires package.json with "test" script
		if strings.Contains(cmd, "npm test") || strings.Contains(cmd, "npm run test") {
			hasPackageJSON := false
			for _, f := range v.DeliveryPhases[i].RequiredFiles {
				if strings.HasSuffix(f, "package.json") {
					hasPackageJSON = true
					break
				}
			}
			if !hasPackageJSON {
				// Check earlier phases for package.json
				found := false
				for j := 0; j < i; j++ {
					for _, f := range v.DeliveryPhases[j].RequiredFiles {
						if strings.HasSuffix(f, "package.json") {
							found = true
							break
						}
					}
					if found {
						break
					}
				}
				if !found && len(v.DeliveryPhases[i].RequiredFiles) > 0 {
					// Find the correct package.json location - for frontend, it should be at the project root
					// not in src/. Look for the most likely project root from the phase's files.
					base := findProjectRootForNPM(v.DeliveryPhases[i].RequiredFiles)
					if base == "" {
						base = filepath.Dir(v.DeliveryPhases[i].RequiredFiles[0])
					}
					if base == "." {
						v.DeliveryPhases[i].RequiredFiles = append(v.DeliveryPhases[i].RequiredFiles, "package.json")
					} else {
						v.DeliveryPhases[i].RequiredFiles = append(v.DeliveryPhases[i].RequiredFiles, base+"/package.json")
					}
				}
			}
		}
		// pytest requires pyproject.toml
		if strings.Contains(cmd, "pytest") {
			hasPyproject := false
			for _, f := range v.DeliveryPhases[i].RequiredFiles {
				if strings.HasSuffix(f, "pyproject.toml") {
					hasPyproject = true
					break
				}
			}
			if !hasPyproject {
				found := false
				for j := 0; j < i; j++ {
					for _, f := range v.DeliveryPhases[j].RequiredFiles {
						if strings.HasSuffix(f, "pyproject.toml") {
							found = true
							break
						}
					}
					if found {
						break
					}
				}
				if !found && len(v.DeliveryPhases[i].RequiredFiles) > 0 {
					base := filepath.Dir(v.DeliveryPhases[i].RequiredFiles[0])
					if base == "." {
						v.DeliveryPhases[i].RequiredFiles = append(v.DeliveryPhases[i].RequiredFiles, "pyproject.toml")
					} else {
						v.DeliveryPhases[i].RequiredFiles = append(v.DeliveryPhases[i].RequiredFiles, base+"/pyproject.toml")
					}
				}
			}
		}
	}
	return v
}

// findProjectRootForNPM determines the correct project root for npm commands.
// It avoids placing package.json in src/ subdirectories by finding the most
// likely project root from the phase's required files.
func findProjectRootForNPM(files []string) string {
	// Common patterns for project roots in frontend projects
	for _, f := range files {
		dir := filepath.Dir(f)
		// Look for common frontend project root indicators
		parts := strings.Split(dir, "/")
		for i := len(parts) - 1; i >= 0; i-- {
			if parts[i] == "frontend" || parts[i] == "app" || parts[i] == "web" {
				// The project root is likely the parent of this directory
				if i > 0 {
					return strings.Join(parts[:i+1], "/")
				}
			}
		}
	}
	// Fallback: find the shortest common directory path that isn't "src"
	for _, f := range files {
		dir := filepath.Dir(f)
		parts := strings.Split(dir, "/")
		for i := len(parts) - 1; i >= 0; i-- {
			if parts[i] == "src" {
				if i > 0 {
					return strings.Join(parts[:i], "/")
				}
			}
		}
	}
	return ""
}

// deduplicateRequiredFiles removes obviously incorrect nested paths when the
// correct parent path is already present. E.g., if both "X/package.json" and
// "X/src/package.json" are in the list, the src/ one is wrong (the LLM placed
// the file at the wrong depth).
func deduplicateRequiredFiles(files []string) []string {
	// Build a set of all files for O(1) lookup
	fileSet := make(map[string]bool, len(files))
	for _, f := range files {
		fileSet[f] = true
	}
	out := make([]string, 0, len(files))
	for _, f := range files {
		dir := filepath.Dir(f)
		base := filepath.Base(f)
		parts := strings.Split(dir, "/")
		// Walk up one level: if X/Y/file exists and X/file also exists, skip this one
		skip := false
		if len(parts) >= 2 {
			parentDir := strings.Join(parts[:len(parts)-1], "/")
			parentPath := parentDir + "/" + base
			if fileSet[parentPath] && parentPath != f {
				skip = true
			}
		}
		if !skip {
			out = append(out, f)
		}
	}
	return out
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
	if overlay.DevServerPort > 0 {
		base.DevServerPort = overlay.DevServerPort
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
		"phase_scope_note":              v.PhaseScopeNote(),
		"integration_contract_scope_note": v.IntegrationContractScopeNote(),
		"requirements_file":       req,
		"spec_summary":            v.SpecSummary,
		"unittest_command_hint":     scoped.QAVerifyHint(),
		"implementation_verify_hint": "(resolved per rig at fetch_task — use go build until server main exists)",
		"project_setup_verify_hint":   scoped.ProjectSetupVerifyHint(),
		"project_setup_failure_hint":  ProjectSetupFailureHint(scoped),
		"project_setup_stack_kind":    ProjectSetupStackKind(scoped),
		"python_venv_dir":             v.PythonVenvRelDir(),
		"min_architecture_bytes":        fmt.Sprintf("%d", v.MinArchitectureBytes),
		"min_plan_bytes":                fmt.Sprintf("%d", v.MinPlanBytes),
		"min_implementation_file_bytes": fmt.Sprintf("%d", StubCheckOptionsFromValidation(v).MinFileBytes),
		"min_substantive_lines":         fmt.Sprintf("%d", StubCheckOptionsFromValidation(v).MinSubstantiveLines),
		"bead_id_example":                  beadIDExample(v),
		"static_url_contract_guidance":     RigFlowStaticURLContractGuidance,
		"static_url_contract_short":        RigFlowStaticURLContractShort,
		"target_os":                        runtime.GOOS,
		"target_arch":                      runtime.GOARCH,
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
			if base != "" && base != "/" && base != "." && !seen[base] {
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
		return GoCompileOnlyVerifyCommand(v, mayorRigDir)
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
		return GoProjectSetupVerifyCommand(v, "")
	}
	// Check Node.js before Python so dual-stack rigs (Python backend + Node frontend)
	// scope each delivery phase to its actual stack.
	if WorkflowUsesNodeJS(v) {
		return NodeProjectSetupVerifyCommand(v)
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

// QAVerifyHint returns the suggested QA command for error messages.
func (v WorkflowValidation) QAVerifyHint() string {
	// For Python workflows, use PythonVerifyCommand which handles venv and layout correctly
	if WorkflowUsesPython(v) {
		return PythonVerifyCommand(v)
	}
	// For other workflows, use the scoped QAVerifyCommand which already has
	// phase-specific overrides applied (e.g., Go mod phase uses "go mod download").
	cmd := strings.TrimSpace(v.QAVerifyCommand)
	if cmd == "" {
		return v.UnittestCommandHint()
	}
	return NormalizePytestCommand(cmd)
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
	if strings.Contains(lower, "pip install") {
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

// ValidateDeliveryPhases enforces internal consistency on delivery_phases from spec-index or hand-edited profiles.
// Rules:
//   - Phase IDs must be unique, lowercase, kebab-case
//   - depends_on must only reference phase IDs that exist in the same array
//   - Every phase MUST have a non-empty qa_verify_command
//   - Dockerfile/docker-compose files go in the final phase only
func ValidateDeliveryPhases(v WorkflowValidation) WorkflowValidation {
	if len(v.DeliveryPhases) == 0 {
		return v
	}

	// Build ID set and map
	idSet := make(map[string]bool, len(v.DeliveryPhases))
	idMap := make(map[string]*DeliveryPhase, len(v.DeliveryPhases))
	for i := range v.DeliveryPhases {
		id := strings.TrimSpace(v.DeliveryPhases[i].ID)
		if id == "" {
			// Generate from title
			id = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(v.DeliveryPhases[i].Title), " ", "-"))
			id = strings.Trim(id, "-")
		}
		// Normalize to kebab-case
		id = normalizePhaseID(id)
		// Ensure uniqueness
		base := id
		suffix := 2
		for idSet[id] {
			id = fmt.Sprintf("%s-%d", base, suffix)
			suffix++
		}
		idSet[id] = true
		v.DeliveryPhases[i].ID = id
		idMap[id] = &v.DeliveryPhases[i]
	}

	// Validate depends_on and qa_verify_command
	for i := range v.DeliveryPhases {
		p := &v.DeliveryPhases[i]

		// Validate depends_on - only keep references to existing phase IDs
		var validDeps []string
		for _, dep := range p.DependsOn {
			dep = strings.TrimSpace(dep)
			if dep == "" {
				continue
			}
			if idSet[dep] {
				validDeps = append(validDeps, dep)
			}
		}
		p.DependsOn = validDeps

		// Ensure qa_verify_command exists and has no placeholder
		cmd := strings.TrimSpace(p.QAVerifyCommand)
		if cmd == "" || strings.Contains(cmd, "no verify command inferred") {
			p.QAVerifyCommand = defaultQAVerifyForPhase(p, v.LayoutRoot)
		}
	}

	// Ensure Docker/compose files only in final phase (if multiple phases)
	if len(v.DeliveryPhases) > 1 {
		lastPhase := &v.DeliveryPhases[len(v.DeliveryPhases)-1]
		for i := 0; i < len(v.DeliveryPhases)-1; i++ {
			p := &v.DeliveryPhases[i]
			var filtered []string
			for _, f := range p.RequiredFiles {
				lower := strings.ToLower(f)
				if strings.Contains(lower, "dockerfile") ||
					strings.Contains(lower, "docker-compose") ||
					strings.Contains(lower, ".dockerignore") {
					// Move to last phase
					lastPhase.RequiredFiles = append(lastPhase.RequiredFiles, f)
					continue
				}
				filtered = append(filtered, f)
			}
			p.RequiredFiles = filtered
		}
		// Deduplicate last phase
		seen := make(map[string]bool, len(lastPhase.RequiredFiles))
		deduped := make([]string, 0, len(lastPhase.RequiredFiles))
		for _, f := range lastPhase.RequiredFiles {
			if !seen[f] {
				seen[f] = true
				deduped = append(deduped, f)
			}
		}
		lastPhase.RequiredFiles = deduped
	}

	return v
}

func normalizePhaseID(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	s = strings.Trim(b.String(), "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	if s == "" {
		s = "phase"
	}
	return s
}

func defaultQAVerifyForPhase(p *DeliveryPhase, layoutRoot string) string {
	hasGo := false
	hasPy := false
	hasTS := false
	hasJS := false
	hasTSConfig := false
	for _, f := range p.RequiredFiles {
		if strings.HasSuffix(f, "_test.go") || strings.HasSuffix(f, ".go") {
			hasGo = true
		}
		if strings.HasSuffix(f, ".py") || strings.HasPrefix(f, "tests/") {
			hasPy = true
		}
		if strings.HasSuffix(f, ".ts") || strings.HasSuffix(f, ".tsx") || strings.Contains(f, "frontend/") {
			hasTS = true
		}
		if strings.HasSuffix(f, ".js") || strings.HasSuffix(f, ".jsx") || strings.HasSuffix(f, ".mjs") || strings.HasSuffix(f, ".cjs") {
			hasJS = true
		}
		if strings.HasSuffix(f, "tsconfig.json") {
			hasTSConfig = true
		}
	}

	lr := layoutRoot
	if lr == "" {
		lr = "."
	}

	if hasGo {
		return fmt.Sprintf("cd %s && go test ./...", lr)
	}
	if hasPy {
		return fmt.Sprintf("cd %s && python -m pytest -v", lr)
	}
	if hasTS {
		return fmt.Sprintf("cd %s/frontend && npm install && npx tsc --noEmit", lr)
	}
	if hasJS && hasTSConfig {
		return fmt.Sprintf("cd %s && npm install --ignore-scripts && npx tsc --noEmit", lr)
	}
	if hasJS {
		return fmt.Sprintf("cd %s && npm install --ignore-scripts && npm test", lr)
	}
	return fmt.Sprintf("cd %s && echo 'verify ok (no automated tests for this phase)'", lr)
}
