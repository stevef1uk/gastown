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

// workflowHasGoWebAndServer reports required_files includes web assets and cmd/server/main.go.
func workflowHasGoWebAndServer(v WorkflowValidation) bool {
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

// WorkflowNeedsRuntimeSmoke reports Go web+server profiles that may run HTTP smoke
// during implementation verify (GT-VERIFY-002/009). Probe paths come from SPEC only.
func WorkflowNeedsRuntimeSmoke(v WorkflowValidation) bool {
	return workflowHasGoWebAndServer(v)
}

// APISmokeHasHTTPAPI reports whether rig docs define HTTP API paths beyond serving "/".
func APISmokeHasHTTPAPI(spec APISmokeSpec) bool {
	for _, p := range spec.GETPaths {
		if smokeProbeAPIPath(p) {
			return true
		}
	}
	return len(spec.POSTProbes) > 0
}

func smokeProbeAPIPath(path string) bool {
	path = normalizeSmokePath(path)
	if path == "" || path == "/" {
		return false
	}
	return strings.HasPrefix(path, "/api/") || strings.Contains(path, "{")
}

func smokeHasNonRootGET(spec APISmokeSpec) bool {
	for _, p := range spec.GETPaths {
		if normalizeSmokePath(p) != "" && normalizeSmokePath(p) != "/" {
			return true
		}
	}
	return false
}

// WorkflowNeedsQARuntimeSmoke reports whether QA must run a live server smoke CMD this session.
// Python rigs use pytest unless SPEC documents HTTP. Go rigs skip API curls when SPEC has no API table.
func WorkflowNeedsQARuntimeSmoke(townRoot, rig string, v WorkflowValidation) bool {
	if WorkflowUsesPython(v) {
		return pythonWorkflowNeedsQARuntimeSmoke(townRoot, rig, v)
	}
	if !workflowHasGoWebAndServer(v) {
		return false
	}
	spec, _ := LoadAPISmokeSpecFromRig(townRoot, rig, v)
	if APISmokeHasHTTPAPI(spec) || len(spec.StaticAssets) > 0 || smokeHasNonRootGET(spec) {
		return true
	}
	for _, p := range spec.GETPaths {
		if normalizeSmokePath(p) == "/" {
			return true
		}
	}
	return false
}

func pythonWorkflowNeedsQARuntimeSmoke(townRoot, rig string, v WorkflowValidation) bool {
	spec, err := LoadAPISmokeSpecFromRig(townRoot, rig, v)
	if err != nil || !APISmokeHasHTTPAPI(spec) {
		return false
	}
	for _, f := range append(append([]string(nil), v.RequiredFiles...), v.UnionRequiredFiles()...) {
		lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(f)))
		switch {
		case strings.HasSuffix(lower, "app.py"),
			strings.HasSuffix(lower, "main.py"),
			strings.Contains(lower, "/api/"),
			strings.Contains(lower, "wsgi"),
			strings.Contains(lower, "asgi"),
			strings.Contains(lower, "server.py"):
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
