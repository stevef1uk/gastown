package orchestrator

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"
)

var (
	scaffoldFrontendSpecRE = regexp.MustCompile(`(?im)frontend[^\n]*(?:next\.?js|react|vite|node\.?js)`)
	scaffoldPythonSpecRE   = regexp.MustCompile(`(?i)\b(?:fastapi|uvicorn|flask|django)\b`)
	scaffoldUVSpecRE       = regexp.MustCompile(`(?i)\buv\b`)
	scaffoldSingleSpecRE   = regexp.MustCompile(`(?i)\bsingle (?:docker )?container\b`)
	scaffoldStaticCopyRE   = regexp.MustCompile(`(?im)\bCOPY\s+--from=([a-zA-Z][\w-]*)\s+(/[\w./-]+)\s+(/[\w./-]+)`)
	scaffoldImageRE        = regexp.MustCompile(`(?i)\b(node|python):([0-9][a-zA-Z0-9_.-]*)`)
	scaffoldCMDRE          = regexp.MustCompile("(?im)^\\s*(?:-\\s*)?CMD\\s*:?\\s+(\\[[^\\n]+\\]|[^\\n`]+)")
	scaffoldHealthRE       = regexp.MustCompile(`https?://(?:localhost|127\.0\.0\.1):[0-9]+(/[^\s"'\x60]*)`)
)

// scaffoldPlanForSpec supplements incomplete profile paths with explicit SPEC
// application evidence. Playwright's Node tooling alone is never a Node frontend.
// It changes scaffold selection only; it does not rewrite the workflow profile.
func scaffoldPlanForSpec(plan *ScaffoldPlan, v *WorkflowValidation, spec string) *ScaffoldPlan {
	inferred := defaultScaffoldPlan(v)
	resolved := *inferred
	if plan != nil {
		resolved = *plan
	}
	if v != nil {
		resolved.LayoutRoot = v.LayoutRoot
		if v.DevServerPort > 0 {
			resolved.Port = v.DevServerPort
		}
		if inferred.Stack != "go" || WorkflowUsesGo(*v) {
			resolved.Stack = inferred.Stack
		}
	}
	if scaffoldFrontendSpecRE.MatchString(spec) && scaffoldPythonSpecRE.MatchString(spec) {
		resolved.Stack = "hybrid"
	} else if resolved.Stack == "" || resolved.Stack == "generic" {
		resolved.Stack = inferred.Stack
	}
	if scaffoldSingleSpecRE.MatchString(spec) {
		resolved.Kind = "single-container"
	}
	return &resolved
}

// configurePythonScaffoldValues keeps installation, venv and module paths in the
// same project directory. Hybrid images copy the backend project into that cwd;
// Python-only images retain the complete layout and use --app-dir when needed.
func configurePythonScaffoldValues(vals map[string]string, stack string, v *WorkflowValidation, port int) {
	projectDir, appDir, entry, hasPyproject, hasLock := ".", ".", "app.main", false, false
	files := []string(nil)
	layout := "."
	if v != nil {
		files, layout = v.UnionRequiredFiles(), v.LayoutRootDir()
	}
	for _, f := range files {
		rel := layoutRel(f, layout)
		if path.Base(rel) == "pyproject.toml" {
			projectDir, hasPyproject = path.Dir(rel), true
			break
		}
	}
	if !hasPyproject {
		for _, f := range files {
			rel := layoutRel(f, layout)
			if path.Base(rel) == "requirements.txt" {
				projectDir = path.Dir(rel)
				break
			}
		}
	}
	if stack == "hybrid" && projectDir == "." && !hasPyproject {
		projectDir = "backend"
	}
	appDir = projectDir
	for _, f := range files {
		rel := layoutRel(f, layout)
		if rel == path.Join(projectDir, "uv.lock") {
			hasLock = true
		}
		if strings.HasSuffix(rel, "/app/main.py") || rel == "app/main.py" {
			appDir = path.Dir(path.Dir(rel))
			entry = "app.main"
		}
	}
	// Also support a project-level main.py/app.py, not only app/main.py.
	for _, f := range files {
		rel := layoutRel(f, layout)
		if rel == path.Join(projectDir, "main.py") || rel == path.Join(projectDir, "app.py") {
			entry = strings.TrimSuffix(path.Base(rel), ".py")
			break
		}
	}

	install := "python -m pip install --no-cache-dir -r requirements.txt"
	if hasPyproject {
		install = "python -m pip install --no-cache-dir uv && uv sync"
		if hasLock {
			install += " --frozen"
		}
		install += " --no-dev --no-install-project"
	}
	args := []string{"python", "-m", "uvicorn", entry + ":app"}
	if stack == "hybrid" {
		vals["BACKEND_DIR"] = projectDir
		vals["WORKDIR"] = path.Join("/app", projectDir)
	} else {
		if projectDir != "." {
			install = "cd " + bashSingleQuote(projectDir) + " && " + install
		}
		if appDir != "." {
			args = append(args, "--app-dir", appDir)
		}
	}
	args = append(args, "--host", "0.0.0.0", "--port", fmt.Sprint(port))
	cmd, _ := json.Marshal(args)
	vals["RUN_CMD"] = string(cmd)
	vals["PY_INSTALL"] = install
	vals["PY_VENV_BIN"] = path.Join("/app", projectDir, ".venv/bin")
	for _, f := range files {
		rel := layoutRel(f, layout)
		if path.Base(rel) == "package.json" && path.Dir(rel) != "." && !scaffoldHarnessPath(rel) {
			vals["FRONTEND_DIR"] = path.Dir(rel)
			break
		}
	}
}

func scaffoldHarnessPath(rel string) bool {
	lower := strings.ToLower("/" + rel)
	return IsE2ETestPath(rel) || strings.Contains(lower, "/test/") || strings.Contains(lower, "/tests/")
}

// applyScaffoldSpecValues honors concrete Docker instructions in SPEC instead of
// guessing a different image version, static export destination or startup CMD.
// Defaults remain usable when the SPEC specifies only the application stack.
func applyScaffoldSpecValues(vals map[string]string, spec string, port int) {
	for _, image := range scaffoldImageRE.FindAllStringSubmatch(spec, -1) {
		switch strings.ToLower(image[1]) {
		case "node":
			vals["NODE_IMAGE"] = "node:" + image[2]
			if strings.Contains(image[2], "alpine") {
				vals["NODE_SYSTEM_DEPS"] = "apk add --no-cache curl"
			}
		case "python":
			vals["PY_IMAGE"] = "python:" + image[2]
			if strings.Contains(image[2], "alpine") {
				vals["PY_SYSTEM_DEPS"] = "apk add --no-cache curl"
			}
		}
	}
	if strings.Contains(strings.ToLower(spec), "pyproject.toml") && scaffoldUVSpecRE.MatchString(spec) {
		install := "python -m pip install --no-cache-dir uv && uv sync"
		if strings.Contains(strings.ToLower(spec), "uv.lock") || strings.Contains(strings.ToLower(spec), "uv sync --frozen") {
			install += " --frozen"
		}
		install += " --no-dev --no-install-project"
		if existing := vals["PY_INSTALL"]; strings.HasPrefix(existing, "cd ") {
			prefix, _, _ := strings.Cut(existing, " && ")
			install = prefix + " && " + install
		}
		vals["PY_INSTALL"] = install
	}
	if copy := scaffoldStaticCopyRE.FindStringSubmatch(spec); len(copy) > 0 {
		vals["FRONTEND_STAGE"] = copy[1]
		vals["FRONTEND_COPY_SOURCE"] = copy[2]
		vals["FRONTEND_BUILD_DIR"] = path.Dir(copy[2])
		vals["STATIC_DESTINATION"] = copy[3]
	}
	for _, match := range scaffoldCMDRE.FindAllStringSubmatch(spec, -1) {
		var args []string
		command := strings.TrimSpace(match[1])
		if strings.HasPrefix(command, "[") {
			if json.Unmarshal([]byte(command), &args) != nil {
				continue
			}
		} else {
			args = strings.Fields(command)
		}
		if len(args) == 0 {
			continue
		}
		switch args[0] {
		case "uvicorn", "gunicorn", "python", "python3", "npm", "node":
			cmd, _ := json.Marshal(args)
			vals["RUN_CMD"] = string(cmd)
		}
	}
	for _, match := range scaffoldHealthRE.FindAllStringSubmatch(spec, -1) {
		if strings.Contains(strings.ToLower(match[1]), "health") || strings.Contains(strings.ToLower(match[1]), "ready") {
			vals["HEALTHCHECK"] = fmt.Sprintf(`"CMD", "curl", "-f", "http://localhost:%d%s"`, port, strings.TrimRight(match[1], ").,;"))
			break
		}
	}
}
