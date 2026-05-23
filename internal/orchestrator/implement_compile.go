package orchestrator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GoToolchainMismatch reports broken local Go installs (compiler vs go tool version skew).
func GoToolchainMismatch(err error, output string) bool {
	text := ""
	if err != nil {
		text = err.Error()
	}
	text += "\n" + output
	return strings.Contains(text, "does not match go tool version")
}

// goTestArgsFromVerify extracts `go test` arguments from a profile qa_verify_command.
func goTestArgsFromVerify(v WorkflowValidation) []string {
	cmd := strings.TrimSpace(v.QAVerifyCommand)
	lower := strings.ToLower(cmd)
	if idx := strings.Index(lower, "go test"); idx >= 0 {
		rest := strings.Fields(cmd[idx+len("go test"):])
		if len(rest) > 0 {
			return append([]string{"test"}, rest...)
		}
	}
	return []string{"test", "./..."}
}

// ImplementationModuleCompileOK runs the profile Go verify chain (go mod tidy + tests)
// from mayor/rig. Used before treating implementation as complete or skipping bead reopen.
func ImplementationModuleCompileOK(rigDir string, v WorkflowValidation) error {
	if !WorkflowUsesGo(v) {
		return nil
	}
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" {
		layout = "."
	}
	moduleDir := filepath.Join(rigDir, layout)
	if _, err := os.Stat(filepath.Join(moduleDir, "go.mod")); err != nil {
		return fmt.Errorf("module %s: %w", moduleDir, err)
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		return fmt.Errorf("go not in PATH: %w", err)
	}
	var combined strings.Builder
	for _, args := range [][]string{{"mod", "tidy"}, goTestArgsFromVerify(v)} {
		cmd := exec.Command(goBin, args...)
		cmd.Dir = moduleDir
		cmd.Env = os.Environ()
		out, runErr := cmd.CombinedOutput()
		if len(out) > 0 {
			combined.Write(out)
		}
		if runErr != nil {
			text := strings.TrimSpace(combined.String())
			if text == "" {
				text = runErr.Error()
			}
			return fmt.Errorf("module compile/test failed: %w\n%s", runErr, text)
		}
	}
	return nil
}
