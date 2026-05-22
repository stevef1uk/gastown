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
		"before `internal/store/store.go`",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in:\n%s", want, joined)
		}
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
	if strings.Contains(joined, "InitSchema") {
		t.Fatalf("store.go should not get schema-only bullets:\n%s", joined)
	}
	if !strings.Contains(joined, "go test") {
		t.Fatal("expected correlated test bullet for store.go")
	}
}
