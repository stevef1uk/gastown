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
// file and Playwright files. Returns the number of files written. Existing files
// are never overwritten (the agents may already have created them).
func ScaffoldRigIntegrationTemplates(townRoot, rig string) (int, error) {
	if rig == "" || townRoot == "" {
		return 0, nil
	}
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

	layoutRoot := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	destDir := filepath.Join(townRoot, rig, "mayor", "rig")
	if layoutRoot != "" && layoutRoot != "." {
		destDir = filepath.Join(destDir, layoutRoot)
	}

	port := v.DevServerPort
	if port == 0 {
		port = 8080
	}
	vals := scaffoldTemplateValues(v, port)

	entries, err := townAssets.ReadDir(rigInitTemplateDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read embedded rig-init templates: %w", err)
	}

	written := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		rel := filepath.ToSlash(e.Name())
		rel, ok = mapRigInitTemplate(rel)
		if !ok {
			continue
		}
		data, err := townAssets.ReadFile(rigInitTemplateDir + "/" + e.Name())
		if err != nil {
			return written, fmt.Errorf("read template %s: %w", e.Name(), err)
		}
		rendered := renderTemplate(string(data), vals)
		if err := writeIfMissing(destDir, rel, rendered); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}

// mapRigInitTemplate maps a template filename to its destination path under the
// layout root. The e2e spec is excluded: it is app-specific and the agents write
// it during the integration-test phase.
func mapRigInitTemplate(name string) (string, bool) {
	switch name {
	case "docker-compose.yml", "package.json", "playwright.config.ts", "Dockerfile":
		return name, true
	default:
		return "", false
	}
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
		"PLAYWRIGHT_IMAGE":   "playwright-go-test:latest",
		"PLAYWRIGHT_WORKDIR": "/app",
		"PLAYWRIGHT_COMMAND": playwrightCmd,
		"PLAYWRIGHT_BASE_URL": webBase,
		"TEST_DIR":           "./e2e",
		"BASE_URL":           webBase,
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
