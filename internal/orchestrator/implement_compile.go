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

// phaseIsGoModOnly is an alias for PhaseIsGoModOnly (package-local call sites).
func phaseIsGoModOnly(v WorkflowValidation) bool { return PhaseIsGoModOnly(v) }

// PhaseRequiresGoPackages reports whether scoped required_files include Go source (not go.mod-only).
func PhaseRequiresGoPackages(v WorkflowValidation) bool {
	for _, f := range v.RequiredFiles {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if strings.HasSuffix(f, ".go") {
			return true
		}
	}
	return false
}

// ImplementationModuleCompileOK runs phase-appropriate compile checks from mayor/rig layout_root.
// Go-mod-only phases use go mod download (no tidy — empty modules drop SPEC requires).
// Phases with .go files run go mod tidy + go test per QAVerifyCommand.
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
	if phaseIsGoModOnly(v) {
		if err := ValidateGoModFile(rigDir, v); err != nil {
			return err
		}
		script := strings.TrimSpace(GoModScaffoldVerifyCommand(v, rigDir))
		if script == "" {
			return nil
		}
		cmd := exec.Command("/bin/bash", "-c", script)
		cmd.Dir = rigDir
		cmd.Env = os.Environ()
		out, runErr := cmd.CombinedOutput()
		if runErr != nil {
			text := strings.TrimSpace(string(out))
			if text == "" {
				text = runErr.Error()
			}
			return fmt.Errorf("module scaffold verify failed: %w\n%s", runErr, text)
		}
		return nil
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

// ImplementationQueueGreen reports no open/in_progress implement beads and go mod tidy + go test pass under layout_root.
// FormatImplementBeadCompileFailureBlock returns prompt text when the bead's Go package does not build.
func FormatImplementBeadCompileFailureBlock(mayorRigDir, beadPath string, v WorkflowValidation) string {
	if !WorkflowUsesGo(v) || !strings.HasSuffix(filepath.ToSlash(beadPath), ".go") {
		return ""
	}
	pkg := GoBuildRelPackage(v.LayoutRoot, beadPath)
	if pkg == "" {
		return ""
	}
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	if layout == "" {
		layout = "."
	}
	moduleDir := filepath.Join(mayorRigDir, layout)
	goBin, err := exec.LookPath("go")
	if err != nil {
		return ""
	}
	cmd := exec.Command(goBin, "build", "./"+pkg+"/...")
	cmd.Dir = moduleDir
	cmd.Env = os.Environ()
	out, runErr := cmd.CombinedOutput()
	if runErr == nil {
		return ""
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		text = runErr.Error()
	}
	if len(text) > 2000 {
		text = text[:2000] + "\n... (truncated)\n"
	}
	return fmt.Sprintf("### Package does not compile yet\n\n**%s** — `go build ./%s/...` failed. Do **not** send failure JSON without **EDIT:**/**WRITE:** fix work in this session.\n\n```\n%s\n```",
		filepath.ToSlash(beadPath), pkg, text)
}

func ImplementationQueueGreen(townRoot, rig string, v WorkflowValidation) bool {
	if townRoot == "" || rig == "" {
		return false
	}
	active, err := ListImplementBeadsOpenOrInProgress(townRoot, rig, v.ForActivePhase())
	if err != nil || len(active) > 0 {
		return false
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	return ImplementationModuleCompileOK(rigDir, v.ForActivePhase()) == nil
}
