package orchestrator

import (
	"strings"
	"testing"
)

func TestGoTestOutputSuggestsHandlerWebCwd404(t *testing.T) {
	t.Parallel()
	out := `--- FAIL: TestRegisterHandlers (0.00s)
    handlers_test.go:41: GET / returned 404, want 200
FAIL`
	if !GoTestOutputSuggestsHandlerWebCwd404("", "", WorkflowValidation{}, out) {
		t.Fatal("expected cwd 404 detection")
	}
}

func TestChdirExprToModuleRootFromTest(t *testing.T) {
	t.Parallel()
	got := ChdirExprToModuleRootFromTest("linkshelf/internal/api/handlers_test.go", "linkshelf")
	want := `filepath.Join("..", "..")`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestFormatHandlerTestCwdHint(t *testing.T) {
	t.Parallel()
	out := `--- FAIL: TestRegisterHandlers (0.00s)
    handlers_test.go:41: GET / returned 404, want 200
FAIL`
	h := FormatHandlerTestCwdHint("", "", "linkshelf/internal/api/handlers_test.go", out, WorkflowValidation{LayoutRoot: "linkshelf"})
	if !strings.Contains(h, "working directory") || !strings.Contains(h, `filepath.Join("..", "..")`) {
		t.Fatalf("missing cwd guidance:\n%s", h)
	}
}

func TestFormatHandlerRoot404ServeWebHint_handlersBead(t *testing.T) {
	t.Parallel()
	out := `--- FAIL: TestRegisterHandlers (0.00s)
    handlers_test.go:41: GET / returned 404, want 200
FAIL`
	h := FormatHandlerRoot404ServeWebHint("", "", "linkshelf/internal/api/handlers.go", out, WorkflowValidation{LayoutRoot: "linkshelf"})
	if !strings.Contains(h, "serveWebFile") || !strings.Contains(h, "first line") {
		t.Fatalf("missing serveWebFile guidance:\n%s", h)
	}
}

func TestHandlerTestMissingModuleChdirIssues(t *testing.T) {
	t.Parallel()
	body := `func TestRegisterHandlers(t *testing.T) {
		mux := http.NewServeMux()
		RegisterHandlers(mux, db)
		req := httptest.NewRequest("GET", "/", nil)
	}`
	v := WorkflowValidation{LayoutRoot: "linkshelf"}
	issues := HandlerTestMissingModuleChdirIssues("", "", "linkshelf/internal/api/handlers_test.go", body, v)
	if len(issues) == 0 {
		t.Fatal("expected chdir issue")
	}
	body2 := `func TestX(t *testing.T) {
		os.Chdir(filepath.Join("..", ".."))
		httptest.NewRequest("GET", "/", nil)
	}`
	if issues := HandlerTestMissingModuleChdirIssues("", "", "linkshelf/internal/api/handlers_test.go", body2, v); len(issues) != 0 {
		t.Fatalf("unexpected issues: %v", issues)
	}
	body3 := `func TestStatic(t *testing.T) {
		os.Chdir(t.TempDir())
	}`
	if issues := HandlerTestMissingModuleChdirIssues("", "", "linkshelf/internal/api/handlers_test.go", body3, v); len(issues) == 0 {
		t.Fatal("expected reject chdir to t.TempDir()")
	}
}


