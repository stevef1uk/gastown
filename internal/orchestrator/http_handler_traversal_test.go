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

func TestHandlerStaticServePatternIssues_requiresRequestURIGuard(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{LayoutRoot: "linkshelf"}
	InvalidateHTTPProfileCacheForTest()
	body := `mux.HandleFunc("/static/", func(w http.ResponseWriter, r *http.Request) {
		file := filepath.Join("web", r.URL.Path[len("/static/"):])
		http.ServeFile(w, r, file)
	})`
	issues := HandlerStaticServePatternIssues("", "", body, v)
	found := false
	for _, iss := range issues {
		if strings.Contains(iss, "RequestURI") {
			found = true
		}
	}
	if !found {
		t.Fatalf("want RequestURI guard issue (no .. check at all), got %v", issues)
	}
}

func TestHandlerStaticServePatternIssues_acceptsRequestURIGuard(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{LayoutRoot: "linkshelf"}
	InvalidateHTTPProfileCacheForTest()
	body := `mux.HandleFunc("/static/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RequestURI(), "..") {
			http.NotFound(w, r)
			return
		}
	})`
	if issues := HandlerStaticServePatternIssues("", "", body, v); len(issues) != 0 {
		t.Fatalf("unexpected issues: %v", issues)
	}
}

func TestHandlerStaticServePatternIssues_rejectsLateRequestURIGuard(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{LayoutRoot: "linkshelf"}
	InvalidateHTTPProfileCacheForTest()
	body := `mux.HandleFunc("/static/", func(w http.ResponseWriter, r *http.Request) {
		serveWebFile(w, r, "x")
	})
	func serveWebFile(w http.ResponseWriter, r *http.Request, name string) {
		if strings.Contains(r.URL.RequestURI(), "..") { return }
	}`
	issues := HandlerStaticServePatternIssues("", "", body, v)
	found := false
	for _, iss := range issues {
		if strings.Contains(iss, "ServeMux") || strings.Contains(iss, "first lines") {
			found = true
		}
	}
	if !found {
		t.Fatalf("want late-guard issue, got %v", issues)
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

func TestValidateImplementWrittenContent_rejectsStaticWithoutRequestURI(t *testing.T) {
	t.Parallel()
	body := `package api
func RegisterHandlers(mux *http.ServeMux, db *sql.DB) {
	mux.HandleFunc("/static/", func(w http.ResponseWriter, r *http.Request) {
		file := strings.TrimPrefix(r.URL.Path, "/static/")
		if strings.Contains(file, "..") {
			http.NotFound(w, r)
			return
		}
	})
}
`
	err := ValidateImplementWrittenContent("", "", t.TempDir(), "linkshelf/internal/api/handlers.go", body, WorkflowValidation{LayoutRoot: "linkshelf"})
	if err == nil || !strings.Contains(err.Error(), "RequestURI") {
		t.Fatalf("got %v", err)
	}
}
