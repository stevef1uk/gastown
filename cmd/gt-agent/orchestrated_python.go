package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/agentenv"
	"github.com/steveyegge/gastown/internal/orchestrator"
)

func isPipInstallRequirementsCommand(cmd string) bool {
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "pip install") &&
		(strings.Contains(lower, "-r ") || strings.Contains(lower, "install -r"))
}

func isPipInstallForActiveBead(cmd, townRoot, rig, activeBead string, v orchestrator.WorkflowValidation) bool {
	beadPath := orchestrator.ImplementBeadPathForID(townRoot, rig, activeBead, v)
	if beadPath == "" {
		return false
	}
	reqPath := v.RequirementsFilePath()
	if reqPath == "" {
		return false
	}
	return orchestrator.PathMatchesImplementWrite(beadPath, reqPath, v.RequiredFiles, v)
}

func validatePythonProjectSetupCommand(cmd string, v orchestrator.WorkflowValidation) error {
	lower := strings.ToLower(cmd)
	if strings.Contains(lower, "go mod") || strings.Contains(lower, "go test") || strings.Contains(lower, "go build") {
		return fmt.Errorf("do not run go toolchain in Python project_setup — use venv, pip, and setup verify")
	}
	if strings.Contains(lower, "bd close") {
		return fmt.Errorf("do not bd close in project_setup")
	}
	if strings.Contains(lower, "source ") && !strings.Contains(lower, "pip install") {
		return fmt.Errorf("do not use source/activate — gt-agent runs pip/pytest with the venv python automatically")
	}
	if strings.Contains(lower, "pip") && !strings.Contains(lower, ".venv/bin/python3 -m pip") &&
		!strings.Contains(lower, ".venv/bin/pip") {
		return fmt.Errorf("in project_setup, use .venv/bin/python3 -m pip install (not bare pip) so packages go into the venv")
	}
	if req := v.RequirementsFilePath(); req != "" && strings.Contains(lower, strings.ToLower(req)) {
		// In project_setup, allow echo/cat heredoc for requirements.txt; the implement bead
		// handles the proper WRITE: later. Setup just needs the file to exist for pip install.
		if strings.Contains(lower, "pip install") {
			return nil
		}
		if strings.Contains(lower, "echo ") || strings.Contains(lower, "<<") || strings.Contains(lower, "cat >") {
			return nil
		}
	}
	return nil
}

func pythonVerifyOutputSuggestsMissingDeps(output string) bool {
	if output == "" {
		return false
	}
	lower := strings.ToLower(output)
	return strings.Contains(lower, "no module named") ||
		strings.Contains(lower, "modulenotfounderror") ||
		strings.Contains(lower, "import error") ||
		strings.Contains(lower, "requires the") && strings.Contains(lower, "package")
}

func validatePythonProjectSetupArtifacts(townRoot, rig string, hadCmdFailure, verifyOK bool, v orchestrator.WorkflowValidation) error {
	if hadCmdFailure {
		return fmt.Errorf("project_setup had failed commands; fix errors before completing")
	}
	if !verifyOK {
		return fmt.Errorf("project_setup requires green verify: %s", orchestrator.PythonProjectSetupVerifyCommand(v))
	}
	workDir := rigMayorRigDir(townRoot, rig)
	venvPy := agentenv.VenvPython(workDir, v.PythonVenvRelDir())
	if _, err := os.Stat(venvPy); err != nil {
		return fmt.Errorf("python venv missing at %s — run python3 -m venv %s in project_setup", venvPy, v.PythonVenvRelDir())
	}
	if req := v.RequirementsFilePath(); req != "" {
		reqPath := filepath.Join(workDir, req)
		if _, err := os.Stat(reqPath); err != nil {
			return fmt.Errorf("%s missing after setup", req)
		}
	}
	return nil
}

func validateCustomImplementationCommand(cmd, townRoot, rig, activeBead string, v orchestrator.WorkflowValidation, verifyOK bool) error {
	if orchestrator.WorkflowUsesGo(v) || orchestrator.WorkflowUsesPython(v) {
		return nil
	}
	if isBeadCloseCommand(cmd) && !verifyOK {
		mayorDir := rigMayorRigDir(townRoot, rig)
		beadPath := orchestrator.ImplementBeadPathForID(townRoot, rig, activeBead, v)
		if beadPath != "" {
			fullPath := filepath.Join(mayorDir, beadPath)
			if info, err := os.Stat(fullPath); err == nil && info.Size() > 0 {
				return nil
			}
		}
		hint := orchestrator.ImplementationVerifyCommandForBead(v, mayorDir, beadPath)
		return fmt.Errorf("run green verify before bd close: %s (in this session, since verify clears on restart)", hint)
	}
	return nil
}

func validatePythonImplementationCommand(cmd, townRoot, rig, activeBead string, v orchestrator.WorkflowValidation, verifyOK bool) error {
	if !orchestrator.WorkflowUsesPython(v) {
		return nil
	}
	lower := strings.ToLower(cmd)
	if strings.Contains(lower, "go mod") || strings.Contains(lower, "go test") || strings.Contains(lower, "go build") {
		return fmt.Errorf("do not run go toolchain on Python rig — use pip/pytest/compileall per Next bead verify")
	}
	if isPipInstallRequirementsCommand(cmd) && verifyOK {
		return fmt.Errorf("install dependencies in project_setup — venv and pip install already ran there")
	}
	if isBeadCloseCommand(cmd) && !verifyOK {
		mayorDir := rigMayorRigDir(townRoot, rig)
		beadPath := orchestrator.ImplementBeadPathForID(townRoot, rig, activeBead, v)
		if orchestrator.IsTestImplementPath(beadPath) || orchestrator.WorkflowUsesPython(v) {
			return allowBeadCloseWhenVerifyIsPointless(mayorDir, beadPath, activeBead, v)
		}
		hint := orchestrator.PythonImplementationVerifyCommandForBead(v, mayorDir, beadPath)
		if orchestrator.IsFrontendImplementPath(beadPath) {
			if err := orchestrator.ValidateBeadArtifactOnDisk(mayorDir, beadPath, v); err != nil {
				return fmt.Errorf("cannot bd close %s: %w — fix the file with EDIT:/WRITE: first", activeBead, err)
			}
			return fmt.Errorf("bd close %s requires a successful EDIT:/WRITE: to %s in this session", activeBead, beadPath)
		}
		return fmt.Errorf("run green verify before bd close: %s (in this session, since verify clears on restart)", hint)
	}
	return nil
}

// allowBeadCloseWhenVerifyIsPointless lets bd close through when the per-bead
// verify is a no-op (compileall for source bead when test file doesn't exist yet,
// or pytest for a test bead that hasn't been written). This avoids the deadlock
// where verifyOK can't be set because the auto-verify chain has no matching handler.
func allowBeadCloseWhenVerifyIsPointless(mayorDir, beadPath, activeBead string, v orchestrator.WorkflowValidation) error {
	if orchestrator.IsTestImplementPath(beadPath) {
		fullPath := filepath.Join(mayorDir, beadPath)
		info, err := os.Stat(fullPath)
		if err != nil {
			return fmt.Errorf("cannot bd close %s: test file %s does not exist — write it first with EDIT:/WRITE:", activeBead, beadPath)
		}
		if info.Size() == 0 {
			return fmt.Errorf("cannot bd close %s: test file %s is empty", activeBead, beadPath)
		}
		return nil
	}
	fullPath := filepath.Join(mayorDir, beadPath)
	if _, err := os.Stat(fullPath); err != nil {
		return fmt.Errorf("cannot bd close %s: source file %s does not exist", activeBead, beadPath)
	}
	return nil
}
