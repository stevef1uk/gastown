package main

import (
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func taskValidation(task *orchestrator.Task) orchestrator.WorkflowValidation {
	if task == nil {
		return orchestrator.DefaultWorkflowValidation()
	}
	v := orchestrator.ClampProfileValidation(task.Validation.WithDefaults())
	return v.ForActivePhase()
}

func isProjectSetupVerifyCommandOK(cmd string, v orchestrator.WorkflowValidation) bool {
	if orchestrator.WorkflowUsesPython(v) {
		if commandMatchesQAVerify(cmd, orchestrator.PythonProjectSetupVerifyCommand(v)) {
			return true
		}
		return isPipInstallRequirementsCommand(cmd)
	}
	if !orchestrator.WorkflowUsesGo(v) {
		return isQATestCommandOK(cmd, v)
	}
	lower := strings.ToLower(strings.Join(strings.Fields(cmd), " "))
	if strings.Contains(lower, "go build") || strings.Contains(lower, "go run") ||
		strings.Contains(lower, "go test") || strings.Contains(lower, "curl ") {
		return false
	}
	return strings.Contains(lower, "go mod tidy") || strings.Contains(lower, "go mod init") ||
		strings.Contains(lower, "go get ")
}

func isQATestCommandOK(cmd string, v orchestrator.WorkflowValidation) bool {
	if strings.TrimSpace(v.QAVerifyCommand) != "" {
		return commandMatchesQAVerify(cmd, v.QAVerifyCommand)
	}
	return isUnittestCommand(cmd, v.UnittestModule)
}

func isImplementationVerifyCommandOK(cmd, townRoot, rig, activeBead string, v orchestrator.WorkflowValidation) bool {
	if isQATestCommandOK(cmd, v) {
		return true
	}
	if !orchestrator.WorkflowUsesGo(v) || rig == "" {
		return false
	}
	mayorDir := filepath.Join(townRoot, rig, "mayor", "rig")
	beadPath := orchestrator.ImplementBeadPathForID(townRoot, rig, activeBead, v)
	impl := orchestrator.ImplementationVerifyCommandForBead(v, mayorDir, beadPath)
	if commandMatchesQAVerify(cmd, impl) {
		return true
	}
	return goVerifyCommandMatches(cmd, impl, v)
}

// goVerifyCommandMatches accepts agent commands that cd into layout via rig/mayor/rig paths
// while verify hints use layout-relative cd (cd linkshelf && go mod tidy).
func goVerifyCommandMatches(cmd, verify string, v orchestrator.WorkflowValidation) bool {
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" {
		return false
	}
	lower := strings.ToLower(cmd)
	if !strings.Contains(lower, layout) {
		return false
	}
	steps := goToolchainStepsFromVerify(verify)
	if len(steps) == 0 {
		return false
	}
	for _, step := range steps {
		if strings.HasPrefix(step, "go build ") {
			target := strings.TrimSpace(strings.TrimPrefix(step, "go build"))
			if target != "" && !strings.Contains(lower, target) {
				return false
			}
			if !strings.Contains(lower, "go build") {
				return false
			}
			continue
		}
		if !strings.Contains(lower, step) {
			return false
		}
	}
	return true
}

func goToolchainStepsFromVerify(verify string) []string {
	var steps []string
	for _, part := range strings.Split(verify, "&&") {
		p := strings.ToLower(strings.TrimSpace(part))
		if strings.HasPrefix(p, "go ") {
			steps = append(steps, p)
		}
	}
	return steps
}

func commandMatchesQAVerify(cmd, verify string) bool {
	c := strings.ToLower(strings.TrimSpace(cmd))
	v := strings.ToLower(strings.TrimSpace(verify))
	if v == "" {
		return false
	}
	cNorm := strings.Join(strings.Fields(c), " ")
	vNorm := strings.Join(strings.Fields(v), " ")
	if strings.Contains(cNorm, vNorm) {
		return true
	}
	return strings.Contains(c, v)
}
