package orchestrator

import (
	"strings"
	"testing"
)

func TestNewImplementWriteScopeError_wrongBeadHint(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "mockrig"
	v := DefaultWorkflowValidation()
	v.LayoutRoot = "linkshelf"
	v.BeadTitleContains = "Implement linkshelf/"
	v.RequiredFiles = []string{
		"linkshelf/internal/store/schema.go",
		"linkshelf/internal/api/handlers_test.go",
	}
	setListImplementBeadsByStatusHook(t, dir, rig, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "open" || status == "in_progress" {
			return []PlanBead{
				{ID: "te-8cz", Title: "Implement linkshelf/internal/store/schema.go per architecture"},
				{ID: "te-rnd", Title: "Implement linkshelf/internal/api/handlers_test.go per architecture"},
			}, nil
		}
		return nil, nil
	})

	err := NewImplementWriteScopeError(dir, rig, "te-8cz", "linkshelf/internal/store/schema.go",
		"linkshelf/internal/api/handlers_test.go", v)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, want := range []string{"te-rnd", "te-8cz", "handlers_test.go", "bd update te-rnd"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("missing %q in %q", want, msg)
		}
	}
}

func TestFailingVerifyTestPath_vitest(t *testing.T) {
	t.Parallel()
	v := DefaultWorkflowValidation()
	v.LayoutRoot = "personal-space"
	v.RequiredFiles = []string{
		"personal-space/src/server/app.ts",
		"personal-space/tests/unit/backend/theme.test.ts",
	}
	output := " FAIL  personal-space/tests/unit/backend/theme.test.ts > Theme API > GET /api/theme\nAssertionError: expected 404 to be 200"
	got := FailingVerifyTestPath(output, v)
	if got != "personal-space/tests/unit/backend/theme.test.ts" {
		t.Fatalf("got %q", got)
	}
}

func TestFailingVerifyTestPath_goTest(t *testing.T) {
	t.Parallel()
	v := DefaultWorkflowValidation()
	v.LayoutRoot = "linkshelf"
	v.RequiredFiles = []string{
		"linkshelf/internal/store/store_test.go",
		"linkshelf/internal/api/handlers_test.go",
	}
	output := "--- FAIL: TestList (linkshelf/internal/store/store_test.go:12)"
	got := FailingVerifyTestPath(output, v)
	if got != "linkshelf/internal/store/store_test.go" {
		t.Fatalf("got %q", got)
	}
}

func TestFailingVerifyTestPath_pytest(t *testing.T) {
	t.Parallel()
	v := DefaultWorkflowValidation()
	v.LayoutRoot = "myapp"
	v.RequiredFiles = []string{
		"myapp/tests/test_routes.py",
	}
	output := "FAILED myapp/tests/test_routes.py::test_theme"
	got := FailingVerifyTestPath(output, v)
	if got != "myapp/tests/test_routes.py" {
		t.Fatalf("got %q", got)
	}
}

func TestFailingVerifyTestPath_noFailLine(t *testing.T) {
	t.Parallel()
	v := DefaultWorkflowValidation()
	v.LayoutRoot = "linkshelf"
	v.RequiredFiles = []string{"linkshelf/internal/store/store_test.go"}
	got := FailingVerifyTestPath("ok (2 tests)", v)
	if got != "" {
		t.Fatalf("got %q, want empty", got)
	}
	got = FailingVerifyTestPath("", v)
	if got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestImplementWriteScopeVerifyHint_namesFailingTest(t *testing.T) {
	t.Parallel()
	v := DefaultWorkflowValidation()
	v.LayoutRoot = "personal-space"
	v.RequiredFiles = []string{
		"personal-space/src/server/app.ts",
		"personal-space/tests/unit/backend/theme.test.ts",
	}
	output := " FAIL  personal-space/tests/unit/backend/theme.test.ts > Theme API\nAssertionError: expected 404 to be 200"
	hint := ImplementWriteScopeVerifyHint(output, v)
	if hint == "" {
		t.Fatal("expected hint")
	}
	for _, want := range []string{"theme.test.ts", "read that test", "auto-rewinds"} {
		if !strings.Contains(hint, want) {
			t.Fatalf("missing %q in hint %q", want, hint)
		}
	}
}

func TestImplementWriteScopeVerifyHint_emptyWhenNoFail(t *testing.T) {
	t.Parallel()
	v := DefaultWorkflowValidation()
	v.LayoutRoot = "personal-space"
	v.RequiredFiles = []string{"personal-space/tests/unit/backend/theme.test.ts"}
	if hint := ImplementWriteScopeVerifyHint("", v); hint != "" {
		t.Fatalf("got %q, want empty", hint)
	}
	if hint := ImplementWriteScopeVerifyHint("ok (2 tests)", v); hint != "" {
		t.Fatalf("got %q, want empty", hint)
	}
}
