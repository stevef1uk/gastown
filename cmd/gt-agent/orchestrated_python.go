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

func validatePythonProjectSetupCommand(cmd string, v orchestrator.WorkflowValidation) error {
	lower := strings.ToLower(cmd)
	if strings.Contains(lower, "go mod") || strings.Contains(lower, "go test") || strings.Contains(lower, "go build") {
		return fmt.Errorf("do not run go toolchain in Python project_setup — use venv, pip, and setup verify")
	}
	if strings.Contains(lower, "bd close") {
		return fmt.Errorf("do not bd close in project_setup")
	}
	if strings.Contains(lower, "source ") {
		return fmt.Errorf("do not use source/activate — gt-agent runs pip/pytest with the venv python automatically")
	}
	if req := v.RequirementsFilePath(); req != "" && strings.Contains(lower, strings.ToLower(req)) {
		if strings.Contains(lower, "echo ") || strings.Contains(lower, "<<") || strings.Contains(lower, "cat >") {
			return nil
		}
	}
	return nil
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
	if isPipInstallRequirementsCommand(cmd) {
		return fmt.Errorf("install dependencies in project_setup — venv and pip install already ran there")
	}
	if isBeadCloseCommand(cmd) && !verifyOK {
		mayorDir := rigMayorRigDir(townRoot, rig)
		beadPath := orchestrator.ImplementBeadPathForID(townRoot, rig, activeBead, v)
		hint := orchestrator.PythonImplementationVerifyCommandForBead(v, mayorDir, beadPath)
		if orchestrator.IsFrontendImplementPath(beadPath) && hint == "" {
			if err := orchestrator.ValidateBeadArtifactOnDisk(mayorDir, beadPath, v); err == nil {
				return nil
			}
		}
		return fmt.Errorf("run green verify before bd close: %s (in this session, since verify clears on restart)", hint)
	}
	return nil
}
