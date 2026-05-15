package orchestrator

import (
	"path/filepath"
	"strings"
)

// WorkflowValidation configures artifact checks for a workflow template (rig-flow, etc.).
// Operators edit these fields in orchestrator/templates/*.yaml when changing SPEC scope.
type WorkflowValidation struct {
	BeadTitleContains    string   `yaml:"bead_title_contains" json:"bead_title_contains"`
	UnittestModule       string   `yaml:"unittest_module" json:"unittest_module"`
	RequiredFiles        []string `yaml:"required_files" json:"required_files"`
	MinArchitectureBytes int64    `yaml:"min_architecture_bytes" json:"min_architecture_bytes"`
	MinPlanBytes         int64    `yaml:"min_plan_bytes" json:"min_plan_bytes"`
}

// DefaultWorkflowValidation returns rig-flow FizzBuzz defaults when YAML omits validation.
func DefaultWorkflowValidation() WorkflowValidation {
	return WorkflowValidation{
		BeadTitleContains:    "Implement backend/",
		UnittestModule:       "backend.test_fizzbuzz",
		RequiredFiles:        []string{"backend/fizzbuzz.py", "backend/main.py", "backend/test_fizzbuzz.py"},
		MinArchitectureBytes: 200,
		MinPlanBytes:         200,
	}
}

// WithDefaults fills empty fields from DefaultWorkflowValidation.
func (v WorkflowValidation) WithDefaults() WorkflowValidation {
	d := DefaultWorkflowValidation()
	if v.BeadTitleContains == "" {
		v.BeadTitleContains = d.BeadTitleContains
	}
	if v.UnittestModule == "" {
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
	if overlay.BeadTitleContains != "" {
		base.BeadTitleContains = overlay.BeadTitleContains
	}
	if overlay.UnittestModule != "" {
		base.UnittestModule = overlay.UnittestModule
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
	return base
}

// SubstituteVars replaces {{key}} in validation string fields.
func (v WorkflowValidation) SubstituteVars(vars map[string]string) WorkflowValidation {
	if len(vars) == 0 {
		return v
	}
	v.BeadTitleContains = SubstituteVars(v.BeadTitleContains, vars)
	v.UnittestModule = SubstituteVars(v.UnittestModule, vars)
	for i, f := range v.RequiredFiles {
		v.RequiredFiles[i] = SubstituteVars(f, vars)
	}
	return v
}

// PromptVars returns keys for {{bead_title_contains}}, {{unittest_module}}, etc. in prompt files.
func (v WorkflowValidation) PromptVars() map[string]string {
	return map[string]string{
		"bead_title_contains": v.BeadTitleContains,
		"unittest_module":     v.UnittestModule,
		"required_files":      strings.Join(v.RequiredFiles, ", "),
	}
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

// UnittestCommandHint returns the suggested QA unittest command for error messages.
func (v WorkflowValidation) UnittestCommandHint() string {
	mod := strings.TrimSpace(v.UnittestModule)
	if mod == "" {
		mod = DefaultWorkflowValidation().UnittestModule
	}
	return "python3 -m unittest " + mod
}
