package orchestrator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/telemetry"
)

// SkipImplementationRuntimeSmoke is set when GT_SKIP_IMPLEMENTATION_SMOKE=1 (tests/CI).
func SkipImplementationRuntimeSmoke() bool {
	return os.Getenv("GT_SKIP_IMPLEMENTATION_SMOKE") == "1"
}

// workflowHasGoWebAndServer reports scoped required_files includes web assets and cmd/server/main.go.
func workflowHasGoWebAndServer(v WorkflowValidation) bool {
	if !WorkflowUsesGo(v) {
		return false
	}
	files := v.RequiredFilesForSmokeScope()
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

func smokeProbeAPIPath(path string) bool {
	path = normalizeSmokePath(path)
	if path == "" || path == "/" {
		return false
	}
	return strings.HasPrefix(path, "/api/") || strings.Contains(path, "{")
}

// WorkflowNeedsQARuntimeSmoke reports whether QA must run a live server smoke CMD this session.
func WorkflowNeedsQARuntimeSmoke(townRoot, rig string, v WorkflowValidation) bool {
	if WorkflowUsesPython(v) {
		return pythonWorkflowNeedsQARuntimeSmoke(townRoot, rig, v)
	}
	if !workflowHasGoWebAndServer(v) {
		return false
	}
	spec, _ := LoadAPISmokeSpecFromRig(townRoot, rig, v)
	return specHasRuntimeSmokeProbes(spec)
}

func pythonWorkflowNeedsQARuntimeSmoke(townRoot, rig string, v WorkflowValidation) bool {
	if !pythonWorkflowHasServerEntry(v) {
		return false
	}
	spec, err := LoadAPISmokeSpecFromRig(townRoot, rig, v)
	if err != nil {
		return false
	}
	return specHasRuntimeSmokeProbes(spec)
}

func activePhaseHasFrontendFiles(v WorkflowValidation) bool {
	for _, f := range v.ForActivePhase().RequiredFiles {
		f = strings.ToLower(filepath.ToSlash(f))
		if strings.HasSuffix(f, ".html") || strings.HasSuffix(f, ".js") || strings.HasSuffix(f, ".css") {
			return true
		}
	}
	return false
}

func implementationModuleDir(townRoot, rig string, v WorkflowValidation) string {
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" {
		layout = "."
	}
	return filepath.Join(townRoot, rig, "mayor", "rig", layout)
}

// ImplementationRuntimeSmokeOK runs doc-derived server start + curl probes from layout_root.
func ImplementationRuntimeSmokeOK(townRoot, rig string, v WorkflowValidation) error {
	if SkipImplementationRuntimeSmoke() || !WorkflowNeedsRuntimeSmoke(townRoot, rig, v) {
		return nil
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	moduleDir := implementationModuleDir(townRoot, rig, v)
	if WorkflowUsesGo(v) {
		if _, err := os.Stat(filepath.Join(moduleDir, "go.mod")); err != nil {
			return fmt.Errorf("runtime smoke module %s: %w", moduleDir, err)
		}
		if !GoServerMainExists(rigDir, v) {
			return nil
		}
	}
	if WorkflowUsesPython(v) {
		if !pythonWorkflowHasServerEntry(v) {
			return nil
		}
	}
	spec, err := LoadAPISmokeSpecFromRig(townRoot, rig, v)
	if err != nil {
		return fmt.Errorf("load runtime smoke spec: %w", err)
	}
	if !specHasRuntimeSmokeProbes(spec) {
		return nil
	}
	script := BuildRuntimeSmokeShell(moduleDir, spec)
	if strings.TrimSpace(spec.ServerStart) == "" {
		return fmt.Errorf("runtime smoke: document server start under ## Runtime smoke server in SPEC/architecture, or include uvicorn/gunicorn/flask in qa_verify_command")
	}
	if strings.TrimSpace(script) == "" {
		return fmt.Errorf("empty runtime smoke script for %s", moduleDir)
	}
	_ = StopDevServersForRig(v, rigDir)
	cmd := exec.Command("/bin/bash", "-c", script)
	cmd.Dir = townRoot
	env := os.Environ()
	if WorkflowUsesPython(v) && v.UsesPythonVenv() {
		venvBin := filepath.Join(rigDir, v.PythonVenvRelDir(), "bin")
		for i, e := range env {
			if strings.HasPrefix(e, "PATH=") {
				env[i] = "PATH=" + venvBin + string(os.PathListSeparator) + e[5:]
				break
			}
		}
	}
	cmd.Env = env
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

// ImplementationPhaseVerifyOK runs module tests and, when the profile defines HTTP routes,
// doc-derived runtime smoke before implementation may complete (GT-VERIFY-002/009).
// Scopes checks to the active delivery phase so non-Go phases (web-static, web-shell) do not
// fail on missing Go packages.
func ImplementationPhaseVerifyOK(townRoot, rig string, v WorkflowValidation) error {
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	scoped := v.ForActivePhase()

	var err error

	defer func() {
		telemetry.RecordPhaseVerification(context.Background(), rig, "", "implementation", "ImplementationPhaseVerifyOK", err)
	}()

	if WorkflowUsesGo(scoped) {
		if err = ImplementationModuleCompileOK(rigDir, scoped); err != nil {
			return err
		}
	}
	if WorkflowUsesPython(scoped) {
		if err = ImplementationPythonModuleOK(rigDir, scoped); err != nil {
			return err
		}
	}
	if err = ImplementationRuntimeSmokeOK(townRoot, rig, scoped); err != nil {
		return err
	}
	return nil
}
