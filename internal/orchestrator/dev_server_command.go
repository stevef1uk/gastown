package orchestrator

import (
	"fmt"
	"path/filepath"
	"strings"
)

// DevServerCommand returns the webServer.command for a generated
// playwright.config.ts, chosen generically across Go, Python, and Node rigs.
// The command runs with cwd = layoutDir (the directory the config lives in).
// It returns "" when no dev server command can be derived (the generated config
// then omits webServer and relies on an externally started server).
func DevServerCommand(layoutDir string, v WorkflowValidation) string {
	if v.DevServerPort <= 0 {
		return ""
	}
	files := v.UnionRequiredFiles()
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")

	switch {
	case WorkflowUsesGo(v):
		if target := goServerRunTarget(files, layout); target != "" {
			return "go run " + target
		}
	case WorkflowUsesPython(v):
		if mod := pythonUvicornModule(files, layout); mod != "" {
			return pythonFromLayout(layoutDir, v) + " -m uvicorn " + mod +
				":app --host 0.0.0.0 --port " + fmt.Sprintf("%d", v.DevServerPort)
		}
	default:
		// Node and everything else: the standard `npm run dev` convention.
		return "npm run dev"
	}
	return ""
}

// layoutRel returns a required-file path relative to the layout root (stripping
// a leading layout_root prefix such as "personal-space/").
func layoutRel(path, layout string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	path = strings.TrimPrefix(path, "./")
	layout = strings.Trim(filepath.ToSlash(layout), "/")
	if layout != "" && layout != "." {
		path = strings.TrimPrefix(path, layout+"/")
	}
	return path
}

// goServerRunTarget returns the "go run" target for a Go rig, preferring the
// conventional ./cmd/server entrypoint, else the layout-root main.go.
func goServerRunTarget(files []string, layout string) string {
	for _, f := range files {
		rel := layoutRel(f, layout)
		if strings.HasPrefix(rel, "cmd/server/") || rel == "cmd/server/main.go" {
			return "./cmd/server"
		}
	}
	for _, f := range files {
		if layoutRel(f, layout) == "main.go" {
			return "."
		}
	}
	return ""
}

// pythonUvicornModule derives the uvicorn module (e.g. "backend.main") for a
// Python rig from its entrypoint file, preferring backend/main.py.
func pythonUvicornModule(files []string, layout string) string {
	best := ""
	for _, f := range files {
		rel := layoutRel(f, layout)
		lower := strings.ToLower(rel)
		if !strings.HasSuffix(lower, ".py") {
			continue
		}
		base := filepath.Base(lower)
		if base != "main.py" && base != "app.py" {
			continue
		}
		mod := strings.TrimSuffix(filepath.ToSlash(rel), ".py")
		mod = strings.TrimPrefix(mod, "./")
		mod = strings.ReplaceAll(mod, "/", ".")
		if strings.HasPrefix(mod, "backend.") {
			return mod
		}
		if best == "" || len(mod) < len(best) {
			best = mod
		}
	}
	return best
}

// pythonFromLayout returns the venv python binary path relative to layoutDir
// (e.g. ".venv/bin/python3" for a layout-root venv), or "python3" as fallback.
func pythonFromLayout(layoutDir string, v WorkflowValidation) string {
	venvRel := strings.TrimSpace(v.PythonVenvRelDir())
	if venvRel == "" {
		return "python3"
	}
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	venvFromLayout := filepath.ToSlash(venvRel)
	if layout != "" && layout != "." {
		venvFromLayout = strings.Repeat("../", strings.Count(layout, "/")+1) + venvRel
	}
	return filepath.ToSlash(filepath.Join(venvFromLayout, "bin", "python3"))
}
