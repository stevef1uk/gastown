package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateArchitectureDocAlignment_catchesArchDrift(t *testing.T) {
	dir := t.TempDir()
	spec := `# Linkshelf MVP
| GET | / | 200, serve web/index.html | — |
| GET | /static/{file} | 200, file under web/ | 404 |
| GET | /api/links | 200, JSON array | — |

## Store
` + "```go\nfunc InitSchema(db *sql.DB) error\nfunc List(ctx context.Context) ([]Link, error)\nfunc Create(ctx context.Context, title, url string) (Link, error)\nfunc Delete(ctx context.Context, id int64) error\n```" + `

module linkshelf
`
	arch := `# Architecture
| GET | /web/* | static |
Handlers: GetLinks, DeleteLink. type Store struct. InitDB(db).
`
	if err := os.WriteFile(filepath.Join(dir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{LayoutRoot: "linkshelf"}
	err := ValidateArchitectureDocAlignment(dir, v)
	if err == nil {
		t.Fatal("expected architecture alignment error")
	}
	msg := err.Error()
	for _, want := range []string{"/web", "GetLinks", "Store struct", "InitDB"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
}

func TestValidateArchitectureDocAlignment_passesAlignedArch(t *testing.T) {
	dir := t.TempDir()
	spec := `# Spec
| GET | /api/links | 200, JSON array | — |
## Store
` + "```go\nfunc List() ([]Link, error)\nfunc Create(title, url string) (Link, error)\n```" + `
module linkshelf
`
	arch := `Routes GET/POST /api/links. Package store: List, Create, InitSchema.`
	for name, body := range map[string]string{"SPEC.md": spec, "architecture.md": arch} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := ValidateArchitectureDocAlignment(dir, WorkflowValidation{LayoutRoot: "linkshelf"}); err != nil {
		t.Fatal(err)
	}
}

func TestValidatePlanningDocAlignment_catchesPlanHTTPAndStoreDrift(t *testing.T) {
	dir := t.TempDir()
	spec := `# Linkshelf MVP
## HTTP
| GET | / | 200, serve web/index.html | — |
| GET | /api/links | 200, JSON array | — |
| POST | /api/links | 201 | — |

## Store
Package-level API (no Store struct):
` + "```go\nvar DB *sql.DB\nfunc InitSchema(db *sql.DB) error\nfunc List() ([]Link, error)\nfunc Create(title, url string) (Link, error)\nfunc Delete(id int64) error\n```" + `

module linkshelf
`
	arch := `# Architecture
| GET | /api/links | list |
| POST | /api/links | create |
Store: List, Create, Delete, InitSchema on package DB.
`
	plan := `# Plan
module github.com/example/linkshelf

Handlers register GET /links and POST /links.
Store uses ListLinks and CreateLink.
httptest is mandatory for every bead.
`
	for name, body := range map[string]string{"SPEC.md": spec, "architecture.md": arch, "plan.md": plan} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	v := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		RequiredFiles: []string{"linkshelf/cmd/server/main.go", "linkshelf/internal/store/store.go"},
	}
	err := ValidatePlanningDocAlignment(dir, v)
	if err == nil {
		t.Fatal("expected alignment error")
	}
	msg := err.Error()
	for _, want := range []string{"/links", "ListLinks", "github.com/example", "integration contract", "httptest"} {
		if !strings.Contains(strings.ToLower(msg), strings.ToLower(want)) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
}

func TestValidatePlanningDocAlignment_allowsQualifiedStoreRefs(t *testing.T) {
	dir := t.TempDir()
	spec := `# Spec
## Store
` + "```go\nfunc InitSchema(db *sql.DB) error\nfunc List() ([]Link, error)\n```" + `
| GET | /api/links | JSON array | — |
`
	arch := `# Architecture
Call schema.InitSchema from main, then store.List and store.Create.
`
	plan := `# Plan
## Integration contract
main calls InitSchema; registers GET /api/links; handlers use store.List and store.Create.
## Bead map
### te-1: linkshelf/cmd/server/main.go
- Acceptance: wire store.List
`
	for name, body := range map[string]string{"SPEC.md": spec, "architecture.md": arch, "plan.md": plan} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	v := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		RequiredFiles: []string{"linkshelf/cmd/server/main.go"},
	}
	if err := ValidatePlanningDocAlignment(dir, v); err != nil {
		t.Fatal(err)
	}
}

func TestValidateArchitectureDocAlignment_rejectsBareModulePaths(t *testing.T) {
	dir := t.TempDir()
	spec := `# Spec
| GET | /api/links | 200 | — |
## Store
` + "```go\nfunc List() ([]Link, error)\n```" + `
module linkshelf
`
	arch := `# Architecture
- internal/store/schema.go: Link struct and InitSchema
- internal/store/store.go: List, Create, Delete
`
	for name, body := range map[string]string{"SPEC.md": spec, "architecture.md": arch} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	v := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		RequiredFiles: []string{"linkshelf/internal/store/schema.go", "linkshelf/internal/store/store.go"},
	}
	err := ValidateArchitectureDocAlignment(dir, v)
	if err == nil {
		t.Fatal("expected layout path prefix error")
	}
	if !strings.Contains(err.Error(), "internal/store/schema.go") || !strings.Contains(err.Error(), "linkshelf/internal/store/schema.go") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidatePlanMDBeadPathAlignment_rejectsFlatPathWithRealBeadID(t *testing.T) {
	town := t.TempDir()
	rig := "testgt3"
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	beadsDir := filepath.Join(town, rig, ".beads")
	for _, d := range []string{rigDir, beadsDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	plan := "### te-9oq: linkshelf/handlers.go\n- Scope: wrong flat path\n"
	if err := os.WriteFile(filepath.Join(rigDir, "plan.md"), []byte(plan), 0644); err != nil {
		t.Fatal(err)
	}
	// Simulate open bead with correct path (no bd required — validation reads titles from ListOpenImplementBeads)
	// Use WritePlanningPlanMD path: we need beads in DB or mock ListOpenImplementBeads — use integration with bd if available
	v := WorkflowValidation{
		LayoutRoot:        "linkshelf",
		BeadTitleContains: "Implement linkshelf/",
		RequiredFiles:     []string{"linkshelf/internal/api/handlers.go"},
	}
	// Without bd, skip — test checkPlanBeadMapExactPaths path only
	err := ValidatePlanMDBeadPathAlignment(town, rig, v)
	if err == nil {
		t.Fatal("expected error for flat plan path")
	}
	if !strings.Contains(err.Error(), "linkshelf/handlers.go") {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestValidateArchitectureDocAlignment_allowsPackageRefsWithoutBarePathLint(t *testing.T) {
	dir := t.TempDir()
	spec := `# Spec
| GET | /api/links | 200 | — |
## Store
` + "```go\nfunc List() ([]Link, error)\nfunc Create(title, url string) (Link, error)\n```" + `
module linkshelf
`
	arch := `# Architecture
## Integration
1. linkshelf/cmd/server/main.go opens linkshelf.db, calls schema.InitSchema, assigns store.DB.
2. Handlers use store.List and store.Create.
3. Run ` + "`cd linkshelf && go run ./cmd/server`" + ` for smoke.
## Files
- linkshelf/internal/store/schema.go
- linkshelf/internal/store/store.go
- linkshelf/cmd/server/main.go
`
	for name, body := range map[string]string{"SPEC.md": spec, "architecture.md": arch} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	v := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		RequiredFiles: []string{"linkshelf/cmd/server/main.go", "linkshelf/internal/store/store.go"},
	}
	if err := ValidateArchitectureDocAlignment(dir, v); err != nil {
		t.Fatalf("package refs must not trigger bare internal/ or cmd/ path lint: %v", err)
	}
}

func TestValidateArchitectureDocAlignment_acceptsLayoutPrefixedPaths(t *testing.T) {
	dir := t.TempDir()
	spec := `# Spec
| GET | /api/links | 200 | — |
module linkshelf
`
	arch := `# Architecture
- linkshelf/internal/store/schema.go: schema
- linkshelf/internal/store/store.go: CRUD
`
	for name, body := range map[string]string{"SPEC.md": spec, "architecture.md": arch} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	v := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		RequiredFiles: []string{"linkshelf/internal/store/schema.go"},
	}
	if err := ValidateArchitectureDocAlignment(dir, v); err != nil {
		t.Fatal(err)
	}
}

func TestValidatePlanningDocAlignment_rejectsPlanBareModulePaths(t *testing.T) {
	dir := t.TempDir()
	spec := `# Spec
| GET | /api/links | 200 | — |
module linkshelf
`
	arch := `Routes GET /api/links. Store in linkshelf/internal/store.`
	plan := `# Plan
## Bead map
### te-1: internal/store/schema.go
- Scope: schema
### te-2: internal/store/store.go
- Scope: store
`
	for name, body := range map[string]string{"SPEC.md": spec, "architecture.md": arch, "plan.md": plan} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	v := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		RequiredFiles: []string{"linkshelf/internal/store/schema.go", "linkshelf/internal/store/store.go"},
	}
	err := ValidatePlanningDocAlignment(dir, v)
	if err == nil {
		t.Fatal("expected plan layout path error")
	}
	if !strings.Contains(err.Error(), "plan.md references") || !strings.Contains(err.Error(), "internal/store/schema.go") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidatePlanningDocAlignment_rejectsFlattenedBeadMapPaths(t *testing.T) {
	dir := t.TempDir()
	spec := `# Spec
| GET | /api/links | 200 | — |
module linkshelf
`
	arch := `Routes GET /api/links.`
	plan := `# Plan
## Integration contract
Registers GET /api/links on DefaultServeMux.

## Bead map
### te-1: Implement linkshelf/schema.go per architecture
- Scope: schema
### te-2: Implement linkshelf/main.go per architecture
- Scope: main
`
	for name, body := range map[string]string{"SPEC.md": spec, "architecture.md": arch, "plan.md": plan} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	v := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		RequiredFiles: []string{"linkshelf/internal/store/schema.go", "linkshelf/cmd/server/main.go"},
	}
	err := ValidatePlanningDocAlignment(dir, v)
	if err == nil {
		t.Fatal("expected reject flattened plan bead map paths")
	}
	if !strings.Contains(err.Error(), "linkshelf/schema.go") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidatePlanningDocAlignment_passesAlignedDocs(t *testing.T) {
	dir := t.TempDir()
	spec := `# Spec
| GET | /api/links | 200, JSON array | — |
| POST | /api/links | 201 | — |
## Store
` + "```go\nfunc List() ([]Link, error)\nfunc Create(title, url string) (Link, error)\n```" + `
module linkshelf
`
	arch := `Routes: GET/POST /api/links. Store: List, Create.`
	plan := `# Plan
## Integration contract
Entrypoint imports store; registers GET /api/links and POST /api/links; exports List, Create.

## Bead map
### fi-1: linkshelf/cmd/server/main.go
- Acceptance: wire List, Create from store package
`
	for name, body := range map[string]string{"SPEC.md": spec, "architecture.md": arch, "plan.md": plan} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	v := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		RequiredFiles: []string{"linkshelf/cmd/server/main.go"},
	}
	if err := ValidatePlanningDocAlignment(dir, v); err != nil {
		t.Fatal(err)
	}
}

func TestCheckArchitectureDockerSection_requiresSubstantiveSection(t *testing.T) {
	dir := t.TempDir()
	spec := "# Spec\nDocker project.\n"
	arch := "# Architecture\nMinimal.\n"
	for name, body := range map[string]string{"SPEC.md": spec, "architecture.md": arch} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	v := WorkflowValidation{
		LayoutRoot:      ".",
		TestRunner:      "custom",
		QAVerifyCommand: "docker build .",
		RequiredFiles:   []string{"Dockerfile"},
	}
	if err := ValidateArchitectureDocAlignment(dir, v); err == nil {
		t.Fatal("expected error for missing Docker & Deployment section")
	} else if !strings.Contains(err.Error(), "## Docker & Deployment") {
		t.Fatalf("unexpected error: %v", err)
	}

	// Now write a substantive section and verify it passes.
	arch = `# Architecture

## Docker & Deployment
Multi-stage build: Stage 1 uses node:20-slim to build the frontend assets (npm ci && npm run build). Stage 2 uses python:3.12-slim to install backend dependencies via pip install -r requirements.txt. The final image exposes port 8000 and runs the application with CMD ["uvicorn", "backend.main:app", "--host", "0.0.0.0", "--port", "8000"]. The docker-compose.yml defines services for app, postgres, and redis with appropriate volumes and networks for local development and CI environments.
`
	if err := os.WriteFile(filepath.Join(dir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateArchitectureDocAlignment(dir, v); err != nil {
		t.Fatalf("expected pass, got: %v", err)
	}
}

func TestCheckArchitectureDockerSection_ignoredForNonDocker(t *testing.T) {
	dir := t.TempDir()
	spec := "# Spec\nPython project.\n"
	arch := "# Architecture\nMinimal.\n"
	for name, body := range map[string]string{"SPEC.md": spec, "architecture.md": arch} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	v := WorkflowValidation{
		LayoutRoot:    ".",
		TestRunner:    "pytest",
		RequiredFiles: []string{"main.py"},
	}
	if err := ValidateArchitectureDocAlignment(dir, v); err != nil {
		t.Fatalf("expected no Docker section error for Python project, got: %v", err)
	}
}

func TestCheckArchitectureIntegrationTestingSection_requiresHeadingForTestFiles(t *testing.T) {
	arch := "# Architecture\nMinimal.\n"
	v := WorkflowValidation{RequiredFiles: []string{"linkshelf/internal/store/store_test.go"}}
	issues := checkArchitectureIntegrationTestingSection(arch, v)
	if len(issues) == 0 || !strings.Contains(issues[0], "## Integration and testing") {
		t.Fatalf("expected missing integration testing section error, got: %v", issues)
	}
}

func TestCheckArchitectureIntegrationTestingSection_rejectsEmptySection(t *testing.T) {
	arch := "# Architecture\n\n## Integration and testing\n\n"
	v := WorkflowValidation{QAVerifyCommand: "cd linkshelf && go test ./..."}
	issues := checkArchitectureIntegrationTestingSection(arch, v)
	if len(issues) == 0 || !strings.Contains(issues[0], "empty") {
		t.Fatalf("expected empty section error, got: %v", issues)
	}
}

func TestCheckArchitectureIntegrationTestingSection_rejectsVagueSection(t *testing.T) {
	arch := `# Architecture

## Integration and testing
We will run some tests.
`
	v := WorkflowValidation{RequiredFiles: []string{"tasklist/tests/test_store.py"}, QAVerifyCommand: "pytest -v"}
	issues := checkArchitectureIntegrationTestingSection(arch, v)
	if len(issues) == 0 || !strings.Contains(issues[0], "too brief") {
		t.Fatalf("expected too brief section error, got: %v", issues)
	}
}

func TestCheckArchitectureIntegrationTestingSection_acceptsSubstantiveSection(t *testing.T) {
	arch := `# Architecture

## Integration and testing
We run unit tests with pytest for the store modules. After deployment we run a smoke test against the health endpoint to ensure the server starts correctly and responds to requests. We also run integration tests against the API endpoints using a test database.
`
	v := WorkflowValidation{RequiredFiles: []string{"tasklist/tests/test_store.py"}}
	if issues := checkArchitectureIntegrationTestingSection(arch, v); len(issues) > 0 {
		t.Fatalf("expected pass, got: %v", issues)
	}
}

func TestCheckArchitectureIntegrationTestingSection_ignoredWithoutTestEvidence(t *testing.T) {
	arch := "# Architecture\nMinimal.\n"
	v := WorkflowValidation{RequiredFiles: []string{"linkshelf/cmd/server/main.go"}}
	if issues := checkArchitectureIntegrationTestingSection(arch, v); len(issues) > 0 {
		t.Fatalf("expected no issues without test files or test command, got: %v", issues)
	}
}

func TestCheckArchitectureE2ETestingSection_requiresHeadingForE2EFiles(t *testing.T) {
	arch := "# Architecture\nMinimal.\n"
	v := WorkflowValidation{RequiredFiles: []string{"linkshelf/docker-compose.yml"}}
	issues := checkArchitectureE2ETestingSection(arch, v)
	if len(issues) == 0 || !strings.Contains(issues[0], "## E2E / integration testing") {
		t.Fatalf("expected missing e2e testing section error, got: %v", issues)
	}
}

func TestCheckArchitectureE2ETestingSection_rejectsEmptySection(t *testing.T) {
	arch := "# Architecture\n\n## E2E / integration testing\n\n"
	v := WorkflowValidation{RequiredFiles: []string{"linkshelf/e2e/home.spec.ts"}}
	issues := checkArchitectureE2ETestingSection(arch, v)
	if len(issues) == 0 || !strings.Contains(issues[0], "empty") {
		t.Fatalf("expected empty section error, got: %v", issues)
	}
}

func TestCheckArchitectureE2ETestingSection_rejectsVagueSection(t *testing.T) {
	arch := `# Architecture

## E2E / integration testing
We will run some e2e tests.
`
	v := WorkflowValidation{RequiredFiles: []string{"linkshelf/playwright.config.ts"}}
	issues := checkArchitectureE2ETestingSection(arch, v)
	if len(issues) == 0 || !strings.Contains(issues[0], "too brief") {
		t.Fatalf("expected too brief section error, got: %v", issues)
	}
}

func TestCheckArchitectureE2ETestingSection_acceptsSubstantiveSection(t *testing.T) {
	arch := `# Architecture

## E2E / integration testing
Start the application stack with docker compose up. The Playwright test suite is executed with npx playwright test against the running services.
Tests cover the login flow, page selectors, and core user workflows like adding items to cart.
The test environment uses a seeded database with known test data to ensure consistent results.
The docker-compose.test.yml file defines the test services including the app and Playwright runner.
`
	v := WorkflowValidation{RequiredFiles: []string{"linkshelf/docker-compose.yml", "linkshelf/e2e/login.spec.ts"}}
	if issues := checkArchitectureE2ETestingSection(arch, v); len(issues) > 0 {
		t.Fatalf("expected pass, got: %v", issues)
	}
}

func TestCheckArchitectureE2ETestingSection_ignoredWithoutE2EFiles(t *testing.T) {
	arch := "# Architecture\nMinimal.\n"
	v := WorkflowValidation{RequiredFiles: []string{"linkshelf/cmd/server/main.go"}}
	if issues := checkArchitectureE2ETestingSection(arch, v); len(issues) > 0 {
		t.Fatalf("expected no issues without e2e files, got: %v", issues)
	}
}

func TestCheckPlanBeadMapExactPaths_normalizesPaths(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:    "finally",
		RequiredFiles: []string{"finally/backend/pyproject.toml", "finally/backend/app/main.py"},
	}
	planDoc := strings.Join([]string{
		"# Implementation plan",
		"## Bead map",
		"### te-1: finally/backend/pyproject.toml",
		"- Scope: pyproject",
	}, "\n")
	issues := checkPlanBeadMapExactPaths(planDoc, v, "finally")
	if len(issues) > 0 {
		t.Fatalf("expected no issues with normalized paths, got: %v", issues)
	}
	badPlan := strings.Join([]string{
		"# Implementation plan",
		"## Bead map",
		"### te-1: finally/backend/wrong_name.py",
		"- Scope: wrong",
	}, "\n")
	issues = checkPlanBeadMapExactPaths(badPlan, v, "finally")
	if len(issues) == 0 {
		t.Fatal("expected issue for path not in required_files")
	}
}
