package orchestrator

import (
	"path/filepath"
	"strings"
)

// WorkflowUsesPython reports Python/pytest workflows (not Go).
func WorkflowUsesPython(v WorkflowValidation) bool {
	return v.UsesPythonVenv()
}

// UsesPythonVenv reports whether gt-agent should create/use a project venv for pip/pytest.
func (v WorkflowValidation) UsesPythonVenv() bool {
	if WorkflowUsesGo(v) {
		return false
	}
	if v.PythonVenvRelDir() == "" {
		return false
	}
	return v.detectsPythonProject()
}

// PythonVenvRelDir returns the venv path relative to mayor/rig (default ".venv"; "" when disabled).
func (v WorkflowValidation) PythonVenvRelDir() string {
	d := strings.TrimSpace(v.PythonVenvDir)
	if strings.EqualFold(d, "off") {
		return ""
	}
	if d == "" {
		return DefaultPythonVenvDirName
	}
	d = filepath.ToSlash(d)
	if strings.Contains(d, "..") || filepath.IsAbs(d) {
		return DefaultPythonVenvDirName
	}
	return d
}

// DefaultPythonVenvDirName is the default venv folder under mayor/rig.
const DefaultPythonVenvDirName = ".venv"

func (v WorkflowValidation) detectsPythonProject() bool {
	if v.RequirementsFilePath() != "" {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(v.TestRunner), "pytest") {
		return true
	}
	q := strings.ToLower(strings.TrimSpace(v.QAVerifyCommand))
	if strings.Contains(q, "python") || strings.Contains(q, "pytest") {
		return true
	}
	if mod := strings.TrimSpace(v.UnittestModule); mod != "" {
		return true
	}
	for _, f := range v.RequiredFiles {
		if strings.HasSuffix(strings.ToLower(strings.TrimSpace(f)), ".py") {
			return true
		}
	}
	return false
}

// PythonVerifyCommand returns the verify shell chain for Python rigs.
func PythonVerifyCommand(v WorkflowValidation) string {
	if q := strings.TrimSpace(v.QAVerifyCommand); q != "" {
		return NormalizePytestCommand(q)
	}
	return v.UnittestCommandHint()
}
