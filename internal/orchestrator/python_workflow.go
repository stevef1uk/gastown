package orchestrator

import (
	"os"
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

// IsPythonImportCheckCommand reports python -c 'import pytest' setup verify (must not be pytest-normalized).
func IsPythonImportCheckCommand(cmd string) bool {
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "-c ") && strings.Contains(lower, "import pytest")
}

// PythonVerifyCommand returns the verify shell chain for Python rigs (QA/implementation).
func PythonVerifyCommand(v WorkflowValidation) string {
	base := strings.TrimSpace(v.QAVerifyCommand)
	if base == "" {
		base = v.UnittestCommandHint()
	} else {
		base = NormalizePytestCommand(base)
	}
	
	cmd := pythonVerifyWithLayout(base, v)

	if v.UsesPythonVenv() && !strings.Contains(cmd, "source ") && !strings.Contains(cmd, ". ") && !strings.Contains(cmd, ".venv/") && !strings.Contains(cmd, "pipenv") && !strings.Contains(cmd, "poetry") {
		venv := v.PythonVenvRelDir()
		cmd = ". " + venv + "/bin/activate && " + cmd
	}

	return cmd
}

// PythonProjectSetupVerifyCommand is the green check for project_setup only: venv
// exists and deps (pytest) are importable — not a full pytest run (no tests exist yet).
func PythonProjectSetupVerifyCommand(v WorkflowValidation) string {
	venv := v.PythonVenvRelDir()
	py := venv + "/bin/python3"
	return "test -x " + py + " && " + py + " -c 'import pytest'"
}

// ImplementationVerifyCommandForBead picks per-bead verify for the active implement path.
func ImplementationVerifyCommandForBead(v WorkflowValidation, mayorRigDir, beadPath string) string {
	if WorkflowUsesGo(v) {
		return GoImplementationVerifyCommandForBead(v, mayorRigDir, beadPath)
	}
	if WorkflowUsesPython(v) {
		return PythonImplementationVerifyCommandForBead(v, mayorRigDir, beadPath)
	}
	if WorkflowUsesDocker(v) {
		return DockerImplementationVerifyCommandForBead(v, mayorRigDir, beadPath)
	}
	return strings.TrimSpace(v.UnittestCommandHint())
}

// PythonImplementationVerifyCommandForBead returns verify scoped to the active implement path.
func PythonImplementationVerifyCommandForBead(v WorkflowValidation, mayorRigDir, beadPath string) string {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	venv := v.PythonVenvRelDir()
	py := venv + "/bin/python3"

	if req := v.RequirementsFilePath(); req != "" && pathMatchesRequired(beadPath, []string{req}) {
		return "test -x " + py + " && " + py + " -c 'import pytest'"
	}

	if IsTestImplementPath(beadPath) && strings.HasSuffix(beadPath, ".py") {
		return py + " -m pytest -v " + beadPath
	}

	if strings.HasSuffix(beadPath, ".py") {
		if testPath := CorrelatedTestPathForSource(beadPath, v.LayoutRoot); testPath != "" {
			if info, err := os.Stat(filepath.Join(mayorRigDir, testPath)); err == nil && info.Size() > 0 {
				return py + " -m pytest -v " + testPath
			}
		}
		return py + " -m compileall -q " + beadPath
	}

	return PythonVerifyCommand(v)
}

func pythonVerifyWithLayout(cmd string, v WorkflowValidation) string {
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" || layout == "." {
		return cmd
	}
	lower := strings.ToLower(cmd)
	testScope := layout + "/tests"
	
	isPytest := strings.Contains(lower, "pytest")
	hasCD := strings.Contains(lower, "cd "+strings.ToLower(layout))
	
	if isPytest {
		if hasCD {
			return cmd
		}
		// Run pytest from mayor/rig (venv lives there); collect only layout/tests.
		if !strings.Contains(lower, testScope) {
			return strings.TrimSpace(cmd) + " " + testScope
		}
		return cmd
	}
	
	if hasCD {
		return cmd
	}
	return "cd " + layout + " && " + cmd
}
