package orchestrator

import (
	"fmt"
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
	RequiredFiles        []string `yaml:"required_files" json:"required_files"`
	SpecSummary                string   `yaml:"spec_summary" json:"spec_summary"`
	MinArchitectureBytes       int64    `yaml:"min_architecture_bytes" json:"min_architecture_bytes"`
	MinPlanBytes               int64    `yaml:"min_plan_bytes" json:"min_plan_bytes"`
	MinImplementationFileBytes int64    `yaml:"min_implementation_file_bytes" json:"min_implementation_file_bytes"`
	MinSubstantiveLines        int      `yaml:"min_substantive_lines" json:"min_substantive_lines"`
}

// Artifact size guard defaults for rig-flow (gt rig spec-index / workflow-profile.json).
// LLMs often emit min_plan_bytes near the full SPEC size; ClampProfileValidation corrects that.
const (
	MinArtifactBytesFloor       int64 = 200
	DefaultMinPlanBytes         int64 = 2500
	MaxMinPlanBytes             int64 = 4096
	DefaultMinArchitectureBytes int64 = 8192
	MaxMinArchitectureBytes     int64 = 8192
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

// ClampProfileValidation normalizes min_*_bytes from spec-index LLM output or hand-edited profiles.
func ClampProfileValidation(v WorkflowValidation) WorkflowValidation {
	v.MinPlanBytes = clampArtifactBytes(v.MinPlanBytes, DefaultMinPlanBytes, MinArtifactBytesFloor, MaxMinPlanBytes)
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
	return v
}

// PromptVars returns keys for {{bead_title_contains}}, {{unittest_module}}, etc. in prompt files.
func (v WorkflowValidation) PromptVars() map[string]string {
	return map[string]string{
		"layout_root":             v.LayoutRoot,
		"bead_title_contains":     v.BeadTitleContains,
		"unittest_module":         v.UnittestModule,
		"qa_verify_command":       v.QAVerifyCommand,
		"test_runner":             v.TestRunner,
		"required_files":          strings.Join(v.RequiredFiles, ", "),
		"spec_summary":            v.SpecSummary,
		"unittest_command_hint":   v.UnittestCommandHint(),
		"min_architecture_bytes":        fmt.Sprintf("%d", v.MinArchitectureBytes),
		"min_plan_bytes":                fmt.Sprintf("%d", v.MinPlanBytes),
		"min_implementation_file_bytes": fmt.Sprintf("%d", StubCheckOptionsFromValidation(v).MinFileBytes),
		"min_substantive_lines":         fmt.Sprintf("%d", StubCheckOptionsFromValidation(v).MinSubstantiveLines),
		"bead_id_example":               beadIDExample(v),
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
// (e.g. backend/fizzbuzz.py → forbid fizzbuzz.py at rig root).
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
	if strings.Contains(lower, "python3 -m pytest") || strings.Contains(lower, "python -m pytest") {
		return cmd
	}
	re := regexp.MustCompile(`(?i)(^|[;&|]\s*|\s+)pytest\b`)
	return re.ReplaceAllString(cmd, `${1}python3 -m pytest`)
}
