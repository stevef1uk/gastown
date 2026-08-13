package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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
	if dep := vals["E2E_DEPENDS_ON"]; !strings.Contains(dep, "web:\n        condition: service_healthy") {
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

func TestSelectDefaultTemplates_multiServiceProfile(t *testing.T) {
	// No LLM plan (optPlan nil): the profile alone must still yield the
	// multi-service layout at the layout root for a profile that requires
	// docker-compose.yml + Dockerfile.web.
	v := &WorkflowValidation{
		LayoutRoot:    "linkshelf",
		DevServerPort: 8080,
		TestRunner:    "go",
		RequiredFiles: []string{
			"linkshelf/go.mod",
			"linkshelf/Dockerfile.web",
			"linkshelf/docker-compose.yml",
			"linkshelf/playwright.config.ts",
		},
	}
	tpl, kind := selectDefaultTemplates(v)
	if kind != "multi-service" {
		t.Fatalf("kind = %q, want multi-service", kind)
	}
	if tpl["docker-compose.multi-service.yml"] != "docker-compose.yml" {
		t.Fatalf("multi-service compose should go to layout root: %v", tpl)
	}
	if _, ok := tpl["Dockerfile"]; ok {
		t.Fatalf("must not emit a plain Dockerfile when Dockerfile.web is required: %v", tpl)
	}
	if tpl["docker-compose.host-run.yml"] != "" {
		t.Fatalf("must not emit host-run compose for a multi-service profile: %v", tpl)
	}

	vals := buildTemplateValues(&ScaffoldPlan{Kind: kind, Stack: "go", Port: 8080}, 8080, kind, v)
	if !strings.Contains(vals["SERVICES_BLOCK"], "  web:") {
		t.Fatalf("default multi-service should synthesize a web service:\n%s", vals["SERVICES_BLOCK"])
	}
	if base := vals["BASE_URL"]; base != "http://web:8080" {
		t.Fatalf("BASE_URL = %q, want http://web:8080", base)
	}
}

func TestMultiServiceComposeRender_noPlan(t *testing.T) {
	// Regression: when the LLM scaffold plan is nil (ExtractScaffoldPlan failed),
	// selectDefaultTemplates still resolves a multi-service profile to the
	// multi-service template, and buildTemplateValues must synthesize the web
	// service even though plan is nil.
	v := &WorkflowValidation{
		LayoutRoot:    "linkshelf",
		DevServerPort: 8080,
		TestRunner:    "go",
		RequiredFiles: []string{
			"linkshelf/go.mod",
			"linkshelf/Dockerfile.web",
			"linkshelf/docker-compose.yml",
			"linkshelf/playwright.config.ts",
		},
	}
	tpl, kind := selectDefaultTemplates(v)
	if kind != "multi-service" {
		t.Fatalf("kind = %q, want multi-service", kind)
	}
	if tpl["docker-compose.multi-service.yml"] != "docker-compose.yml" {
		t.Fatalf("multi-service compose should go to layout root: %v", tpl)
	}
	vals := buildTemplateValues(nil, 8080, "multi-service", v)
	if !strings.Contains(vals["SERVICES_BLOCK"], "  web:") {
		t.Fatalf("nil plan + multi-service should synthesize a web service:\n%s", vals["SERVICES_BLOCK"])
	}
	if base := vals["BASE_URL"]; base != "http://web:8080" {
		t.Fatalf("BASE_URL = %q, want http://web:8080", base)
	}
	if !strings.Contains(vals["SERVICES_BLOCK"], "dockerfile: Dockerfile.web") {
		t.Fatalf("services block should reference Dockerfile.web:\n%s", vals["SERVICES_BLOCK"])
	}

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
		"condition: service_healthy",
		"BASE_URL=http://web:8080",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered compose missing %q:\n%s", want, out)
		}
	}
	firstServices := strings.Index(out, "\nservices:\n")
	if firstServices < 0 {
		t.Fatalf("rendered compose has no services key:\n%s", out)
	}
	for _, line := range strings.Split(out[:firstServices], "\n") {
		if strings.TrimSpace(line) != "" && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			t.Fatalf("non-comment line leaked before services key (%q):\n%s", line, out)
		}
	}
}

func TestBuildTemplateValues_multiService_filtersInvalidServices(t *testing.T) {
	plan := &ScaffoldPlan{
		Kind:  "multi-service",
		Stack: "go",
		Port:  8080,
		Services: []ScaffoldService{
			{Name: "web", BuildDir: ".", Port: 8080, Public: true, Health: "curl -f http://localhost:8080/api/links"},
			{Name: "playwright", BuildDir: ".", Port: 8080}, // reserved name — must be dropped
			{Name: "api", BuildDir: ".", Port: 0},           // placeholder port — must be dropped
		},
	}
	v := &WorkflowValidation{
		LayoutRoot:    "linkshelf",
		RequiredFiles: []string{"linkshelf/go.mod", "linkshelf/Dockerfile.web", "linkshelf/docker-compose.yml"},
	}
	vals := buildTemplateValues(plan, 8080, "multi-service", v)
	svc := vals["SERVICES_BLOCK"]
	if strings.Count(svc, "  web:") != 1 {
		t.Fatalf("expected exactly one web service in SERVICES_BLOCK:\n%s", svc)
	}
	if strings.Contains(svc, "  playwright:") {
		t.Fatalf("reserved 'playwright' service must not be emitted as a build service:\n%s", svc)
	}
	if strings.Contains(svc, `"0:0"`) {
		t.Fatalf("port-0 placeholder service must be dropped:\n%s", svc)
	}
	// Scalar health is not a bracketed list — fall back to the wget default.
	if !strings.Contains(svc, `["CMD", "wget", "-qO-", "http://localhost:8080/"]`) {
		t.Fatalf("scalar health should fall back to wget default:\n%s", svc)
	}
	if dep := vals["E2E_DEPENDS_ON"]; strings.Contains(dep, "playwright:") {
		t.Fatalf("E2E_DEPENDS_ON must not reference the reserved name: %q", dep)
	}
}

// assertValidMultiServiceCompose parses the rendered compose as YAML and checks
// the structural contract: exactly one web app service, a playwright runner that
// depends on the app with service_healthy, and the app built via Dockerfile.web.
func assertValidMultiServiceCompose(t *testing.T, out string) {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("rendered multi-service compose is not valid YAML: %v\n%s", err, out)
	}
	services, ok := doc["services"].(map[string]any)
	if !ok {
		t.Fatalf("no services mapping:\n%s", out)
	}
	web, ok := services["web"].(map[string]any)
	if !ok {
		t.Fatalf("no web service (got: %v):\n%s", doc["services"], out)
	}
	build, _ := web["build"].(map[string]any)
	if build["dockerfile"] != "Dockerfile.web" {
		t.Fatalf("web build should use Dockerfile.web:\n%s", out)
	}
	if _, ok := services["playwright"]; !ok {
		t.Fatalf("no playwright runner service:\n%s", out)
	}
	if _, dup := services["playwright"]; dup && len(services) != 2 {
		t.Fatalf("unexpected extra services (want web + playwright only): %v", keys(services))
	}
	runner, _ := services["playwright"].(map[string]any)
	depends, _ := runner["depends_on"].(map[string]any)
	if _, ok := depends["web"]; !ok {
		t.Fatalf("playwright must depend on web:\n%s", out)
	}
}

func keys(m map[string]any) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return ks
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
		"condition: service_healthy",
		"BASE_URL=http://web:8080",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered multi-service compose missing %q:\n%s", want, out)
		}
	}
	// The SERVICES_BLOCK / E2E_DEPENDS_ON substitutions must not leak into the
	// header comment (which would break YAML). Everything before the first
	// `services:` key must be comment lines.
	firstServices := strings.Index(out, "\nservices:\n")
	if firstServices < 0 {
		t.Fatalf("rendered multi-service compose has no services key:\n%s", out)
	}
	head := out[:firstServices]
	for _, line := range strings.Split(head, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			t.Fatalf("non-comment line leaked before services key (%q):\n%s", line, out)
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
