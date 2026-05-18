package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

// writesGoModuleFilesViaHeredoc reports heredoc/redirect writes to go.mod or go.sum.
func writesGoModuleFilesViaHeredoc(cmd string) bool {
	lower := strings.ToLower(cmd)
	if !strings.Contains(lower, "<<") && !strings.Contains(lower, "cat >") && !strings.Contains(lower, "cat>>") {
		return false
	}
	return strings.Contains(lower, "go.mod") || strings.Contains(lower, "go.sum")
}

func isGoModInitCommand(cmd string) bool {
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "go mod init")
}

func isGoModTidyCommand(cmd string) bool {
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "go mod tidy")
}

func validateProjectSetupCommand(cmd, rig string, v orchestrator.WorkflowValidation) error {
	lower := strings.ToLower(cmd)
	if strings.Contains(lower, "```") {
		return fmt.Errorf("do not wrap commands in markdown code fences")
	}
	if strings.Contains(lower, "gt bd ") {
		return fmt.Errorf("use bare `bd` from %s", rigMayorRigPath(rig))
	}
	if orchestrator.WorkflowUsesPython(v) {
		if err := validatePythonProjectSetupCommand(cmd); err != nil {
			return err
		}
		return nil
	}
	if !orchestrator.WorkflowUsesGo(v) {
		return nil
	}
	if writesGoModuleFilesViaHeredoc(cmd) {
		return fmt.Errorf("do not write go.mod or go.sum via heredoc — use go mod init and go mod tidy")
	}
	if strings.Contains(lower, "python3") || strings.Contains(lower, "pip install") {
		return fmt.Errorf("project_setup is for Go scaffold and beads only — no Python in this step")
	}
	return nil
}

func validateProjectSetupArtifacts(townRoot, rig string, hadCmdFailure, verifyOK bool, v orchestrator.WorkflowValidation) error {
	if orchestrator.WorkflowUsesPython(v) {
		return validatePythonProjectSetupArtifacts(townRoot, rig, hadCmdFailure, verifyOK, v)
	}
	if !orchestrator.WorkflowUsesGo(v) {
		return nil
	}
	if hadCmdFailure {
		return fmt.Errorf("project_setup had failed commands; fix errors before completing")
	}
	if !verifyOK {
		return fmt.Errorf("project_setup requires green verify: %s", orchestrator.GoVerifyCommandWithTidy(v))
	}
	layout := strings.TrimSpace(v.LayoutRoot)
	if layout == "" {
		layout = "."
	}
	goMod := filepath.Join(rigMayorRigDir(townRoot, rig), layout, "go.mod")
	if _, err := os.Stat(goMod); err != nil {
		return fmt.Errorf("go.mod missing at %s after setup", goMod)
	}
	return nil
}

func validateGoImplementationCommand(cmd string, v orchestrator.WorkflowValidation, verifyOK bool) error {
	if !orchestrator.WorkflowUsesGo(v) {
		return nil
	}
	if writesGoModuleFilesViaHeredoc(cmd) {
		return fmt.Errorf("do not write go.mod or go.sum via heredoc — use go mod init, go get, and go mod tidy in project_setup or before bd close")
	}
	if isBeadCloseCommand(cmd) && !verifyOK {
		return fmt.Errorf("run green verify before bd close: %s", orchestrator.GoVerifyCommandWithTidy(v))
	}
	return nil
}
