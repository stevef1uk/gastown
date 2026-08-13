package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildTemplateValues_multiService(t *testing.T) {
	plan := &ScaffoldPlan{
		Kind:  "multi-service",
		Stack: "go",
		Port:  8080,
		Services: []ScaffoldService{
			{Name: "web", BuildDir: ".", Port: 8080, Public: true},
		},
	}
	v := &WorkflowValidation{
		LayoutRoot:    "linkshelf",
		RequiredFiles: []string{"linkshelf/go.mod", "linkshelf/Dockerfile.web", "linkshelf/docker-compose.yml"},
	}
	vals := buildTemplateValues(plan, 8080, "multi-service", v)
	svc := vals["SERVICES_BLOCK"]
	if !strings.Contains(svc, "  web:") {
		t.Fatalf("SERVICES_BLOCK missing web service:\n%s", svc)
	}
	if !strings.Contains(svc, "dockerfile: Dockerfile.web") {
		t.Fatalf("SERVICES_BLOCK should reference Dockerfile.web:\n%s", svc)
	}
	if !strings.Contains(svc, "healthcheck:") {
		t.Fatalf("SERVICES_BLOCK missing healthcheck:\n%s", svc)
	}
	if !strings.Contains(svc, `"wget", "-qO-", "http://localhost:8080/"`) {
		t.Fatalf("SERVICES_BLOCK healthcheck should default to wget on the service port:\n%s", svc)
	}
	if !strings.Contains(svc, `- "8080:8080"`) {
		t.Fatalf("SERVICES_BLOCK missing port mapping:\n%s", svc)
	}
	if dep := vals["E2E_DEPENDS_ON"]; !strings.Contains(dep, "web: condition: service_healthy") {
		t.Fatalf("E2E_DEPENDS_ON wrong: %q", dep)
	}
	if base := vals["BASE_URL"]; base != "http://web:8080" {
		t.Fatalf("BASE_URL = %q, want http://web:8080", base)
	}
}

func TestBuildTemplateValues_multiService_noServicesFallback(t *testing.T) {
	plan := &ScaffoldPlan{Kind: "multi-service", Stack: "go", Port: 8080}
	v := &WorkflowValidation{
		LayoutRoot:    "linkshelf",
		RequiredFiles: []string{"linkshelf/go.mod", "linkshelf/Dockerfile.web", "linkshelf/docker-compose.yml"},
	}
	vals := buildTemplateValues(plan, 8080, "multi-service", v)
	if !strings.Contains(vals["SERVICES_BLOCK"], "  web:") {
		t.Fatalf("no-services fallback should synthesize a web service:\n%s", vals["SERVICES_BLOCK"])
	}
	if base := vals["BASE_URL"]; base != "http://web:8080" {
		t.Fatalf("BASE_URL = %q, want http://web:8080", base)
	}
}

func TestBuildTemplateValues_hostRunUnchanged(t *testing.T) {
	plan := &ScaffoldPlan{Kind: "host-run", Stack: "go", Port: 8080}
	vals := buildTemplateValues(plan, 8080, "host-run", nil)
	if base := vals["BASE_URL"]; base != "http://host.docker.internal:8080" {
		t.Fatalf("host-run BASE_URL = %q, want http://host.docker.internal:8080", base)
	}
	if vals["SERVICES_BLOCK"] != "" || vals["E2E_DEPENDS_ON"] != "" {
		t.Fatalf("host-run should not emit a services block: %q / %q", vals["SERVICES_BLOCK"], vals["E2E_DEPENDS_ON"])
	}
}

func TestResolveComposeKind_profileDriven(t *testing.T) {
	plan := &ScaffoldPlan{Kind: "multi-service", Stack: "go", Port: 8080}

	multiSvc := &WorkflowValidation{
		LayoutRoot:    "linkshelf",
		RequiredFiles: []string{"linkshelf/go.mod", "linkshelf/Dockerfile.web", "linkshelf/docker-compose.yml"},
	}
	kind, dst := resolveComposeKind(plan, multiSvc)
	if kind != "multi-service" || dst != "docker-compose.yml" {
		t.Fatalf("root compose + Dockerfile.web: got kind=%q dst=%q, want multi-service/docker-compose.yml", kind, dst)
	}

	hostRun := &WorkflowValidation{
		LayoutRoot:    "pingapp",
		RequiredFiles: []string{"pingapp/go.mod", "pingapp/docker-compose.yml"},
	}
	kind, dst = resolveComposeKind(plan, hostRun)
	if kind != "host-run" || dst != "docker-compose.yml" {
		t.Fatalf("root compose alone: got kind=%q dst=%q, want host-run/docker-compose.yml", kind, dst)
	}

	single := &WorkflowValidation{
		LayoutRoot:    "finally",
		RequiredFiles: []string{"finally/Dockerfile", "finally/test/docker-compose.test.yml"},
	}
	kind, dst = resolveComposeKind(plan, single)
	if kind != "single-container" || dst != "test/docker-compose.test.yml" {
		t.Fatalf("test compose: got kind=%q dst=%q, want single-container/test/docker-compose.test.yml", kind, dst)
	}

	// Plan fallback when no profile is available.
	kind, dst = resolveComposeKind(plan, nil)
	if kind != "multi-service" || dst != "docker-compose.yml" {
		t.Fatalf("no profile: got kind=%q dst=%q, want multi-service/docker-compose.yml", kind, dst)
	}
}

func TestSelectTemplatesForPlan_profileDriven(t *testing.T) {
	plan := &ScaffoldPlan{Kind: "multi-service", Stack: "go", Port: 8080}
	multiSvc := &WorkflowValidation{
		LayoutRoot:    "linkshelf",
		RequiredFiles: []string{"linkshelf/go.mod", "linkshelf/Dockerfile.web", "linkshelf/docker-compose.yml"},
	}
	tpl := selectTemplatesForPlan(plan, multiSvc)
	if tpl["docker-compose.multi-service.yml"] != "docker-compose.yml" {
		t.Fatalf("multi-service compose should go to layout root: %v", tpl)
	}
	if _, ok := tpl["Dockerfile"]; ok {
		t.Fatalf("must not drop a stray plain Dockerfile when Dockerfile.web is required: %v", tpl)
	}
	if _, ok := tpl["Dockerfile.go"]; ok {
		t.Fatalf("stack Dockerfile templates must not be emitted when Dockerfile.web is required: %v", tpl)
	}

	hostRun := &WorkflowValidation{
		LayoutRoot:    "pingapp",
		RequiredFiles: []string{"pingapp/go.mod", "pingapp/docker-compose.yml"},
	}
	tpl = selectTemplatesForPlan(plan, hostRun)
	if tpl["docker-compose.host-run.yml"] != "docker-compose.yml" {
		t.Fatalf("host-run compose should go to layout root: %v", tpl)
	}
	if tpl["Dockerfile"] != "Dockerfile" {
		t.Fatalf("host-run with no non-default Dockerfile should still emit the stack Dockerfile: %v", tpl)
	}
}

func TestMultiServiceComposeRender(t *testing.T) {
	plan := &ScaffoldPlan{
		Kind:  "multi-service",
		Stack: "go",
		Port:  8080,
		Services: []ScaffoldService{
			{Name: "web", BuildDir: ".", Port: 8080, Public: true},
		},
	}
	v := &WorkflowValidation{
		LayoutRoot:    "linkshelf",
		RequiredFiles: []string{"linkshelf/go.mod", "linkshelf/Dockerfile.web", "linkshelf/docker-compose.yml"},
	}
	vals := buildTemplateValues(plan, 8080, "multi-service", v)
	data, err := townAssets.ReadFile(rigInitTemplateDir + "/docker-compose.multi-service.yml")
	if err != nil {
		t.Fatal(err)
	}
	out := renderTemplate(string(data), vals)
	for _, want := range []string{
		"  web:",
		"dockerfile: Dockerfile.web",
		"healthcheck:",
		"  playwright:",
		"image: playwright-go-test:latest",
		"web: condition: service_healthy",
		"BASE_URL=http://web:8080",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered multi-service compose missing %q:\n%s", want, out)
		}
	}
}

func TestScaffoldRigIntegrationTemplates_FinAlly(t *testing.T) {
	t.Parallel()

	finSpec := `# FinAlly - AI Trading Workstation

## 1. Vision
A single-user AI trading workstation. The user runs a single Docker command. Browser opens to http://localhost:8000.

## 3. Architecture Overview
Single Docker container: multi-stage build (Node -> Python). Static Next.js export served by FastAPI on port 8000.

## 4. Directory Structure
finally/
- frontend/          # Next.js TypeScript project (static export)
- backend/           # FastAPI uv project (Python)
- test/              # Playwright E2E tests + docker-compose.test.yml
- scripts/
- Dockerfile         # Multi-stage build (Node -> Python)
- docker-compose.yml # Optional convenience wrapper
- .env

## 10. Docker & Deployment
services:
  app:
    build: .
    ports:
      - "8000:8000"
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8000/api/health"]
    volumes:
      - finally_db:/app/db
`

	tmpDir := t.TempDir()
	rigDir := filepath.Join(tmpDir, "fin", "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(finSpec), 0o644); err != nil {
		t.Fatal(err)
	}

	profPath := filepath.Join(rigDir, ".gastown", "workflow-profile.json")
	if err := os.MkdirAll(filepath.Dir(profPath), 0o755); err != nil {
		t.Fatal(err)
	}
	profData := `{
  "version": 1,
  "validation": {
    "layout_root": "finally",
    "dev_server_port": 8000,
    "test_runner": "pytest",
    "delivery_phases": [{"id": "test", "required_files": ["finally/test/package.json", "finally/test/playwright.config.ts", "finally/test/e2e.spec.ts", "finally/Dockerfile", "finally/docker-compose.yml", "finally/test/docker-compose.test.yml"]}],
    "required_files": ["finally/backend/pyproject.toml", "finally/frontend/package.json"]
  }
}`
	if err := os.WriteFile(profPath, []byte(profData), 0o644); err != nil {
		t.Fatal(err)
	}

	written, err := ScaffoldRigIntegrationTemplates(tmpDir, "fin", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Written: %d files", written)

	filepath.Walk(filepath.Join(tmpDir, "fin", "mayor", "rig"), func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			t.Logf("Created: %s", strings.TrimPrefix(path, filepath.Join(tmpDir, "fin", "mayor", "rig")))
		}
		return nil
	})

	dfPath := filepath.Join(tmpDir, "fin", "mayor", "rig", "finally", "Dockerfile")
	if _, err := os.Stat(dfPath); os.IsNotExist(err) {
		t.Errorf("Dockerfile NOT created at finally/Dockerfile")
	} else {
		df, _ := os.ReadFile(dfPath)
		dfStr := string(df)
		if !strings.Contains(dfStr, "FROM node:") || !strings.Contains(dfStr, "FROM python:") {
			t.Errorf("Dockerfile should be hybrid (node + python stages)")
		}
		if !strings.Contains(dfStr, "frontend") || !strings.Contains(dfStr, "backend") {
			t.Errorf("Dockerfile should reference frontend/backend dirs")
		}
	}

	testCompPath := filepath.Join(tmpDir, "fin", "mayor", "rig", "finally", "test", "docker-compose.test.yml")
	if _, err := os.Stat(testCompPath); os.IsNotExist(err) {
		t.Errorf("test harness not created at test/docker-compose.test.yml")
	} else {
		tc, _ := os.ReadFile(testCompPath)
		tcStr := string(tc)
		if !strings.Contains(tcStr, "app:") || !strings.Contains(tcStr, "playwright:") {
			t.Errorf("test compose missing app or playwright service")
		}
		if !strings.Contains(tcStr, "condition: service_healthy") {
			t.Errorf("test compose missing healthcheck dependency")
		}
		if !strings.Contains(tcStr, "http://app:8000") {
			t.Errorf("BASE_URL should be http://app:8000")
		}
	}
}
