package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// rigInitTemplateDir is the embedded templates directory scanned for
// integration-test scaffolding (docker-compose, Dockerfile, Dockerfile.web, Playwright).
const rigInitTemplateDir = "town/templates/rig-init"

// ScaffoldRigIntegrationTemplates writes docker-compose + Playwright scaffolding
// (from the embedded rig-init templates) into a rig's layout root when its
// workflow profile has an integration-test phase that ships both a docker-compose
// file and Playwright files. If optPlan is non-nil, it overrides the profile-based
// detection. Returns the number of files written. Existing files are never
// overwritten (the agents may already have created them).
func ScaffoldRigIntegrationTemplates(townRoot, rig string, optPlan *ScaffoldPlan) (int, error) {
	if rig == "" || townRoot == "" {
		return 0, nil
	}

	// Determine layout root, port, and profile
	var layoutRoot string
	var port int
	var profile *WorkflowValidation

	if optPlan != nil {
		layoutRoot = optPlan.LayoutRoot
		port = optPlan.Port
	} else {
		v, ok, err := LoadRigWorkflowProfileFile(townRoot, rig)
		if err != nil {
			return 0, err
		}
		if !ok {
			return 0, nil
		}

		// Only scaffold when a delivery phase ships both docker-compose AND Playwright.
		scaffold := false
		for _, p := range v.DeliveryPhases {
			if phaseShipsDockerPlaywright(&p) {
				scaffold = true
				break
			}
		}
		if !scaffold {
			return 0, nil
		}

		layoutRoot = strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
		port = v.DevServerPort
		profile = &v
	}

	destDir := filepath.Join(townRoot, rig, "mayor", "rig")
	if layoutRoot != "" && layoutRoot != "." {
		destDir = filepath.Join(destDir, layoutRoot)
	}

	if port == 0 {
		port = 8080
	}

	// Select templates based on plan
	var templates map[string]string
	var kind string
	if optPlan != nil {
		templates = selectTemplatesForPlan(optPlan)
		kind = optPlan.Kind
	} else {
		templates, kind = selectDefaultTemplates(profile)
	}

	// Build template values
	vals := buildTemplateValues(optPlan, port, kind, profile)

	written := 0
	for srcName, dstRel := range templates {
		data, err := townAssets.ReadFile(rigInitTemplateDir + "/" + srcName)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return written, fmt.Errorf("read template %s: %w", srcName, err)
		}
		rendered := renderTemplate(string(data), vals)
		if err := writeIfMissing(destDir, dstRel, rendered); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

// buildTemplateValues builds the substitution map for templates.
func buildTemplateValues(plan *ScaffoldPlan, port int, kind string, v *WorkflowValidation) map[string]string {
	vals := map[string]string{
		"PLAYWRIGHT_IMAGE":   "playwright-go-test:latest",
		"PLAYWRIGHT_VERSION": PlaywrightNPMVersion(),
		"PLAYWRIGHT_WORKDIR": "/app",
		"PLAYWRIGHT_COMMAND": "npx playwright test --project=chromium",
		"APP_PORT":           fmt.Sprintf("%d", port),
		"WEB_PORT":           fmt.Sprintf("%d", port),
		"TEST_DIR":           "./e2e",
		"NODE_IMAGE":         "node:20-slim",
		"PY_IMAGE":           "python:3.11-slim",
		"FRONTEND_DIR":       "frontend",
		"BACKEND_DIR":        "backend",
		"STATIC_OUTPUT":      ".next/static",
		"UV_CMD":             "pip install -r requirements.txt",
		"RUN_CMD":            `["python", "app.py"]`,
		"HEALTHCHECK":        `"CMD", "curl", "-f", "http://localhost:${APP_PORT}/"`,
		"ENV_BLOCK":          "PORT=${APP_PORT}",
		"SERVICES_BLOCK":     "",
		"E2E_DEPENDS_ON":     "",
		"APP_BUILD":          ".",
	}

	// Determine base URL based on kind
	baseURL := ""
	if plan != nil && plan.BaseURL != "" {
		baseURL = plan.BaseURL
	}
	if baseURL == "" {
		switch kind {
		case "single-container", "multi-service":
			baseURL = fmt.Sprintf("http://app:%d", port)
		default:
			baseURL = fmt.Sprintf("http://host.docker.internal:%d", port)
		}
	}
	vals["PLAYWRIGHT_BASE_URL"] = baseURL
	vals["BASE_URL"] = baseURL

	// Derive Dockerfile values from stack
	if plan != nil {
		switch plan.Stack {
		case "python":
			vals["RUN_CMD"] = `["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "` + fmt.Sprintf("%d", port) + `"]`
		case "node":
			vals["RUN_CMD"] = `["npm", "start"]`
		case "hybrid":
			vals["UV_CMD"] = "uv sync --frozen 2>/dev/null || pip install -r requirements.txt"
			vals["STATIC_OUTPUT"] = "out"
		case "go":
			vals["RUN_CMD"] = `["./server"]`
		}

		// Build services block for multi-service
		if plan.Kind == "multi-service" {
			services := plan.Services
			if len(services) == 0 {
				// Deterministic fallback when the LLM extracted no services: if the
				// profile ships a Dockerfile* in the layout root, synthesize the web
				// service so the compose is never empty and QA has something to test.
				if multiServiceAppDockerfile(v) != "" {
					services = []ScaffoldService{{
						Name:     "web",
						BuildDir: ".",
						Port:     port,
						Public:   true,
					}}
				}
			}
			if len(services) > 0 {
				// Base URL must target the public service (e.g. http://web:8080), not
				// the hardcoded app: fallback, or the Playwright container cannot reach it.
				baseURL := multiServiceBaseURL(services, port)
				vals["PLAYWRIGHT_BASE_URL"] = baseURL
				vals["BASE_URL"] = baseURL
				// Reference the rig's non-default Dockerfile (e.g. Dockerfile.web) when
				// required; otherwise compose defaults to Dockerfile in the build context.
				dockerfile := multiServiceAppDockerfile(v)
				var svcLines []string
				var dependsLines []string
				for _, svc := range services {
					buildBlock := fmt.Sprintf("  %s:\n    build:\n      context: %s", svc.Name, svc.BuildDir)
					if dockerfile != "" {
						buildBlock += fmt.Sprintf("\n      dockerfile: %s", dockerfile)
					}
					buildBlock += fmt.Sprintf("\n    ports:\n      - \"%d:%d\"", svc.Port, svc.Port)
					// depends_on: condition: service_healthy needs a real healthcheck;
					// default to busybox wget on the service port (alpine-compatible).
					health := strings.TrimSpace(svc.Health)
					if health == "" {
						health = fmt.Sprintf(`["CMD", "wget", "-qO-", "http://localhost:%d/"]`, svc.Port)
					}
					buildBlock += "\n    healthcheck:\n      test: " + health + "\n      interval: 2s\n      timeout: 2s\n      retries: 10"
					svcLines = append(svcLines, buildBlock)
					dependsLines = append(dependsLines, svc.Name+": condition: service_healthy")
				}
				vals["SERVICES_BLOCK"] = strings.Join(svcLines, "\n")
				vals["E2E_DEPENDS_ON"] = strings.Join(dependsLines, "\n      ")
			}
		}
	}

	return vals
}

// multiServiceAppDockerfile returns the layout-root Dockerfile the rig requires
// (e.g. "Dockerfile.web") when one exists, so the multi-service compose builds the
// right image. Empty means the default "Dockerfile" is used by compose.
func multiServiceAppDockerfile(v *WorkflowValidation) string {
	if v == nil {
		return ""
	}
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	prefer := ""
	plain := ""
	for _, f := range v.RequiredFiles {
		f = filepath.ToSlash(strings.TrimSpace(f))
		base := filepath.Base(f)
		if !strings.HasPrefix(base, "Dockerfile") {
			continue
		}
		rel := strings.TrimPrefix(f, layout+"/")
		if strings.Contains(rel, "/") {
			continue // only layout-root Dockerfiles drive the app build
		}
		if base == "Dockerfile" {
			plain = base
		} else {
			prefer = base
		}
	}
	if prefer != "" {
		return prefer
	}
	return plain
}

// multiServiceBaseURL returns the base URL the Playwright runner should hit:
// the first Public service (e.g. http://web:8080), falling back to app:<port>.
func multiServiceBaseURL(services []ScaffoldService, fallbackPort int) string {
	for _, svc := range services {
		if svc.Public {
			return fmt.Sprintf("http://%s:%d", svc.Name, svc.Port)
		}
	}
	return fmt.Sprintf("http://app:%d", fallbackPort)
}

// selectTemplatesForPlan picks the right template files based on the scaffold plan.
func selectTemplatesForPlan(plan *ScaffoldPlan) map[string]string {
	templates := map[string]string{}

	// Dockerfile based on stack
	switch plan.Stack {
	case "hybrid":
		templates["Dockerfile.hybrid"] = "Dockerfile"
	case "python":
		templates["Dockerfile.python"] = "Dockerfile"
	case "node":
		templates["Dockerfile.node"] = "Dockerfile"
	default:
		templates["Dockerfile"] = "Dockerfile"
	}

	// Docker-compose based on kind
	switch plan.Kind {
	case "host-run":
		templates["docker-compose.host-run.yml"] = "docker-compose.yml"
	case "single-container":
		templates["docker-compose.single-container.yml"] = "test/docker-compose.test.yml"
	case "multi-service":
		templates["docker-compose.multi-service.yml"] = "test/docker-compose.test.yml"
	}

	// Playwright config + package.json
	templates["playwright.config.ts"] = "playwright.config.ts"
	templates["package.json"] = "package.json"

	return templates
}

// selectDefaultTemplates returns the legacy template selection (no plan).
// Detects hybrid stack and compose kind from profile when possible.
func selectDefaultTemplates(v *WorkflowValidation) (map[string]string, string) {
	templates := map[string]string{}
	kind := "host-run"

	if v != nil {
		hasFrontend := false
		hasBackend := false
		hasDockerfile := false
		hasTestCompose := false
		for _, f := range v.RequiredFiles {
			if strings.Contains(f, "frontend") || strings.Contains(f, "package.json") {
				hasFrontend = true
			}
			if strings.Contains(f, "backend") || strings.Contains(f, "pyproject.toml") {
				hasBackend = true
			}
			if strings.HasSuffix(f, "Dockerfile") {
				hasDockerfile = true
			}
			if strings.HasSuffix(f, "docker-compose.test.yml") || strings.HasSuffix(f, "docker-compose.test.yaml") {
				hasTestCompose = true
			}
		}

		// Stack detection
		if hasFrontend && hasBackend {
			templates["Dockerfile.hybrid"] = "Dockerfile"
		} else {
			templates["Dockerfile"] = "Dockerfile"
		}

		// Compose kind: single-container if both Dockerfile and test-compose are required
		if hasDockerfile && hasTestCompose {
			kind = "single-container"
			templates["docker-compose.single-container.yml"] = "test/docker-compose.test.yml"
		} else {
			// Host-run: write host-run compose to layout root
			kind = "host-run"
			templates["docker-compose.host-run.yml"] = "docker-compose.yml"
		}
	} else {
		templates["Dockerfile"] = "Dockerfile"
		templates["docker-compose.host-run.yml"] = "docker-compose.yml"
	}

	templates["package.json"] = "package.json"
	templates["playwright.config.ts"] = "playwright.config.ts"

	return templates, kind
}

// scaffoldTemplateValues derives template substitution values from the workflow
// profile. The web app runs on HOST; the Playwright container reaches it via
// host.docker.internal (mapped to the host through compose extra_hosts
// host-gateway), which works on Linux and macOS Docker Desktop alike.
func scaffoldTemplateValues(v WorkflowValidation, port int) map[string]string {
	layoutRoot := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	if layoutRoot == "" || layoutRoot == "." {
		layoutRoot = "app"
	}

	webBase := fmt.Sprintf("http://host.docker.internal:%d", port)
	playwrightCmd := "npx playwright test --project=chromium"

	return map[string]string{
		"PLAYWRIGHT_IMAGE":    "playwright-go-test:latest",
		"PLAYWRIGHT_WORKDIR":  "/app",
		"PLAYWRIGHT_COMMAND":  playwrightCmd,
		"PLAYWRIGHT_BASE_URL": webBase,
		"TEST_DIR":            "./e2e",
		"BASE_URL":            webBase,
	}
}

// baseImageForRunner returns (web image, install cmds, build cmds, run cmd) for
// the profile's test_runner stack.
func baseImageForRunner(testRunner string) (image, install, build, cmd string) {
	switch testRunner {
	case "python", "python3", "flask", "django", "fastapi":
		return "python:3.11-slim",
			"RUN pip install --no-cache-dir flask flask-cors",
			"COPY . .\nRUN pip install --no-cache-dir -r requirements.txt 2>/dev/null || true",
			`["python", "app.py"]`
	case "node", "nodejs", "typescript", "ts", "javascript", "js", "react", "vue", "angular":
		return "node:20-slim",
			"RUN npm install",
			"COPY . .\nRUN npm install",
			`["node", "server.js"]`
	default: // go
		return "golang:1.22-alpine",
			"RUN apk add --no-cache wget",
			"COPY go.mod go.sum ./\nRUN go mod download\nCOPY . .\nRUN CGO_ENABLED=0 go build -o /server ./cmd/server",
			`["./server"]`
	}
}

// renderTemplate substitutes {{KEY}} placeholders from vals.
func renderTemplate(t string, vals map[string]string) string {
	for k, v := range vals {
		t = strings.ReplaceAll(t, "{{"+k+"}}", v)
	}
	return t
}

// writeIfMissing writes content to destDir/rel only if the file does not exist.
func writeIfMissing(destDir, rel, content string) error {
	path := filepath.Join(destDir, filepath.FromSlash(rel))
	if _, err := os.Stat(path); err == nil {
		return nil // never clobber agent-created files
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
