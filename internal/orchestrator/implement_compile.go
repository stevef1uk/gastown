package orchestrator

import (
	"fmt"
	"os/exec"
	"strings"
)

// ImplementationModuleCompileOK runs the profile Go verify chain (go mod tidy + tests)
// from mayor/rig. Used before treating implementation as complete or skipping bead reopen.
func ImplementationModuleCompileOK(rigDir string, v WorkflowValidation) error {
	if !WorkflowUsesGo(v) {
		return nil
	}
	cmd := strings.TrimSpace(GoVerifyCommandWithTidy(v.ForActivePhase()))
	if cmd == "" {
		return nil
	}
	c := exec.Command("bash", "-lc", cmd)
	c.Dir = rigDir
	out, err := c.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(out))
		if text == "" {
			text = err.Error()
		}
		return fmt.Errorf("module compile/test failed: %w\n%s", err, text)
	}
	return nil
}
