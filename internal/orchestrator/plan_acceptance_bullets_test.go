package orchestrator

import (
	"strings"
	"testing"
)

func TestPlanAcceptanceBullets_sqliteSchemaPath(t *testing.T) {
	t.Parallel()
	bullets := planAcceptanceBullets("linkshelf/internal/store/schema.go", WorkflowValidation{LayoutRoot: "linkshelf"})
	if len(bullets) < 3 {
		t.Fatalf("bullets: %v", bullets)
	}
	joined := strings.Join(bullets, "\n")
	for _, want := range []string{
		"InitSchema",
		"CREATE TABLE",
		"before `linkshelf/internal/store/store.go`",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in:\n%s", want, joined)
		}
	}
}

func TestPlanAcceptanceBullets_noBareModulePathsWithLayoutRoot(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		RequiredFiles: []string{"linkshelf/internal/store/schema.go", "linkshelf/internal/store/store.go"},
	}
	doc := strings.Join(planAcceptanceBullets("linkshelf/internal/store/schema.go", v), "\n")
	if issues := checkDocLayoutPathPrefix("plan.md", doc, v); len(issues) > 0 {
		t.Fatalf("layout path lint: %v", issues)
	}
}

func TestPlanAcceptanceBullets_storeGoUsesDefaultBullets(t *testing.T) {
	t.Parallel()
	bullets := planAcceptanceBullets("linkshelf/internal/store/store.go", WorkflowValidation{
		LayoutRoot:      "linkshelf",
		TestRunner:      "go",
		QAVerifyCommand: "cd linkshelf && go test ./...",
	})
	joined := strings.Join(bullets, "\n")
	if strings.Contains(joined, "CREATE TABLE") || strings.Contains(joined, "before `internal/store/store.go`") {
		t.Fatalf("store.go should not get schema-bead-only bullets:\n%s", joined)
	}
	if strings.Contains(joined, "store_test.go") {
		t.Fatalf("MVP required_files has no *_test.go — plan must not mandate correlated test path:\n%s", joined)
	}
	if !strings.Contains(joined, ":memory:") {
		t.Fatalf("expected store API bullet:\n%s", joined)
	}
}

func TestPlanAcceptanceBullets_webFilesUseGenericBullets(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{LayoutRoot: "frontend", TestRunner: "go", SpecSummary: "Interactive bookmark manager"}
	for _, path := range []string{
		"frontend/web/index.html",
		"frontend/web/app.js",
		"frontend/web/style.css",
	} {
		joined := strings.Join(planAcceptanceBullets(path, v), "\n")
		for _, unwanted := range []string{
			"Kanban", "TaskBoard", "/api/projects", "project creation input/button",
			"project selector", "`todo`", "`done`", "frontend/static/",
		} {
			if strings.Contains(joined, unwanted) {
				t.Errorf("planAcceptanceBullets(%q) leaked domain-specific contract %q:\n%s", path, unwanted, joined)
			}
		}
		wants := []string{"File exists at", "Matches layout and naming"}
		if strings.HasSuffix(path, ".html") || strings.HasSuffix(path, ".js") {
			wants = append(wants, "real interactive controls")
		}
		for _, want := range wants {
			if !strings.Contains(joined, want) {
				t.Errorf("planAcceptanceBullets(%q) missing generic bullet %q:\n%s", path, want, joined)
			}
		}
	}
}

func TestPlanAcceptanceBullets_pythonTestsUseGenericBullets(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{LayoutRoot: "service", TestRunner: "pytest"}
	for _, path := range []string{
		"service/tests/test_api.py",
		"service/tests/test_repository.py",
		"service/tests/test_ui_content.py",
	} {
		joined := strings.Join(planAcceptanceBullets(path, v), "\n")
		for _, unwanted := range []string{
			"project create/list/delete", "project deletion cascades", "TaskBoard",
			"GET /api/projects", "Kanban",
		} {
			if strings.Contains(joined, unwanted) {
				t.Errorf("planAcceptanceBullets(%q) leaked domain-specific contract %q:\n%s", path, unwanted, joined)
			}
		}
		if !strings.Contains(joined, "functional requirements") {
			t.Errorf("planAcceptanceBullets(%q) missing generic test bullet:\n%s", path, joined)
		}
	}
}
