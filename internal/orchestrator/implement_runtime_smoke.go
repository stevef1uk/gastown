package orchestrator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SkipImplementationRuntimeSmoke is set when GT_SKIP_IMPLEMENTATION_SMOKE=1 (tests/CI).
func SkipImplementationRuntimeSmoke() bool {
	return os.Getenv("GT_SKIP_IMPLEMENTATION_SMOKE") == "1"
}

// WorkflowNeedsRuntimeSmoke reports Go web+server profiles that require HTTP smoke
// before implementation can complete or match QA runtime checks (GT-VERIFY-002/009).
func WorkflowNeedsRuntimeSmoke(v WorkflowValidation) bool {
	if !WorkflowUsesGo(v) {
		return false
	}
	files := append([]string(nil), v.RequiredFiles...)
	files = append(files, v.UnionRequiredFiles()...)
	hasWeb := false
	hasServer := false
	for _, f := range files {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if strings.Contains(f, "/web/") && (strings.HasSuffix(f, ".html") || strings.HasSuffix(f, ".js") || strings.HasSuffix(f, ".css")) {
			hasWeb = true
		}
		if strings.HasSuffix(f, "/cmd/server/main.go") {
			hasServer = true
		}
	}
	return hasWeb && hasServer
}

func implementationModuleDir(townRoot, rig string, v WorkflowValidation) string {
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" {
		layout = "."
	}
	return filepath.Join(townRoot, rig, "mayor", "rig", layout)
}

// ImplementationRuntimeSmokeOK runs profile-derived go run + curl smoke from layout_root.
func ImplementationRuntimeSmokeOK(townRoot, rig string, v WorkflowValidation) error {
	if SkipImplementationRuntimeSmoke() || !WorkflowNeedsRuntimeSmoke(v) {
		return nil
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	moduleDir := implementationModuleDir(townRoot, rig, v)
	if _, err := os.Stat(filepath.Join(moduleDir, "go.mod")); err != nil {
		return fmt.Errorf("runtime smoke module %s: %w", moduleDir, err)
	}
	if !GoServerMainExists(rigDir, v) {
		return nil
	}
	spec, err := LoadAPISmokeSpecFromRig(townRoot, rig, v)
	if err != nil {
		return fmt.Errorf("load runtime smoke spec: %w", err)
	}
	script := BuildRuntimeSmokeShell(moduleDir, spec)
	if strings.TrimSpace(script) == "" {
		return fmt.Errorf("empty runtime smoke script for %s", moduleDir)
	}
	_ = StopDevServersForRig(v, rigDir)
	cmd := exec.Command("/bin/bash", "-c", script)
	cmd.Dir = townRoot
	cmd.Env = os.Environ()
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		text := strings.TrimSpace(string(out))
		if text == "" {
			text = runErr.Error()
		}
		return fmt.Errorf("implementation runtime smoke failed: %w\n%s", runErr, text)
	}
	return nil
}

// ImplementationPhaseVerifyOK runs go mod tidy + tests and, for web+server Go profiles,
// profile runtime smoke before implementation may complete (GT-VERIFY-002/009).
func ImplementationPhaseVerifyOK(townRoot, rig string, v WorkflowValidation) error {
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	if err := ImplementationModuleCompileOK(rigDir, v); err != nil {
		return err
	}
	if err := ImplementationRuntimeSmokeOK(townRoot, rig, v); err != nil {
		return err
	}
	return nil
}
