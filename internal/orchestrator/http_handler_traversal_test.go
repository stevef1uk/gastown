package orchestrator

import (
	"strings"
	"testing"
)

func TestGoTestOutputSuggestsTraversalRedirect(t *testing.T) {
	t.Parallel()
	out := `--- FAIL: TestRegisterHandlers (0.00s)
    handlers_test.go:72: traversal request returned 307, want 404
FAIL`
	if !GoTestOutputSuggestsTraversalRedirect("", "", WorkflowValidation{}, out) {
		t.Fatal("expected traversal redirect detection")
	}
	if GoTestOutputSuggestsTraversalRedirect("", "", WorkflowValidation{}, "got 404, want 200") {
		t.Fatal("unexpected match")
	}
}

func TestFormatHandlerTraversalRedirectHint_includesReplaceMarker(t *testing.T) {
	t.Parallel()
	InvalidateHTTPProfileCacheForTest()
	h := FormatHandlerTraversalRedirectHint("", "", "linkshelf/internal/api/handlers.go", WorkflowValidation{LayoutRoot: "linkshelf"})
	for _, want := range []string{"/static/", "RequestURI", ">>>>>>> REPLACE", "../../web"} {
		if !strings.Contains(h, want) {
			t.Fatalf("missing %q in:\n%s", want, h)
		}
	}
}

func TestHandlerStaticServePatternIssues(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{LayoutRoot: "linkshelf"}
	InvalidateHTTPProfileCacheForTest()
	body := `mux.HandleFunc("/static", func(w http.ResponseWriter, r *http.Request) {})
path := filepath.Join("..", "..", "web", name)`
	issues := HandlerStaticServePatternIssues("", "", body, v)
	if len(issues) < 2 {
		t.Fatalf("want >=2 issues, got %v", issues)
	}
}

func TestValidateImplementWrittenContent_rejectsBadWebJoin(t *testing.T) {
	t.Parallel()
	body := `package api
func x() {
  p := filepath.Join("..", "..", "web", "x")
}
`
	err := ValidateImplementWrittenContent("", "", t.TempDir(), "linkshelf/internal/api/handlers.go", body, WorkflowValidation{LayoutRoot: "linkshelf"})
	if err == nil || !strings.Contains(err.Error(), "web") {
		t.Fatalf("got %v", err)
	}
}

