package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeHTTPContractFixture(t *testing.T, townRoot, rig string, indexHTML, handlersGo string) {
	t.Helper()
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	arch := `| Method | Path | Notes |
| GET | / | web/index.html |
| GET | /static/{file} | serves web/{file} |
| GET | /api/links | JSON array |
`
	if err := os.MkdirAll(filepath.Join(rigDir, "linkshelf", "web"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(rigDir, "linkshelf", "internal", "api"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "linkshelf", "web", "index.html"), []byte(indexHTML), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "linkshelf", "web", "app.js"), []byte("console.log('ok');\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "linkshelf", "internal", "api", "handlers.go"), []byte(handlersGo), 0644); err != nil {
		t.Fatal(err)
	}
}

func linkshelfHTTPProfile() WorkflowValidation {
	return WorkflowValidation{
		LayoutRoot: "linkshelf",
		RequiredFiles: []string{
			"linkshelf/web/index.html",
			"linkshelf/web/app.js",
			"linkshelf/internal/api/handlers.go",
			"linkshelf/cmd/server/main.go",
		},
		QAVerifyCommand: "cd linkshelf && go test ./...",
		TestRunner:      "custom",
	}
}

func TestValidateHTTPContract_rejectsWrongStaticPrefixInHTML(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "mockrig"
	v := linkshelfHTTPProfile()
	writeHTTPContractFixture(t, dir, rig,
		`<html><head><script src="/app.js"></script></head><body></body></html>`,
		`package api
import "net/http"
func Mount(mux *http.ServeMux, webRoot string) {
  mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {})
}
`)
	err := ValidateHTTPContract(dir, rig, v)
	if err == nil || !strings.Contains(err.Error(), "/static/app.js") {
		t.Fatalf("expected static prefix mismatch, got %v", err)
	}
}

func TestValidateHTTPContract_passesAlignedContract(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "mockrig"
	v := linkshelfHTTPProfile()
	writeHTTPContractFixture(t, dir, rig,
		`<html><head><script src="/static/app.js"></script></head><body></body></html>`,
		`package api
import (
  "net/http"
  "path/filepath"
  "strings"
)
func Mount(mux *http.ServeMux, webRoot string) {
  webRoot = filepath.Join(webRoot, "web")
  mux.HandleFunc("/static/", func(w http.ResponseWriter, r *http.Request) {
    if strings.Contains(r.URL.RequestURI(), "..") {
      http.NotFound(w, r)
      return
    }
  })
}
`)
	if err := ValidateHTTPContract(dir, rig, v); err != nil {
		t.Fatal(err)
	}
}

func TestValidateImplementWrittenContent_requiresModuleChdirInHandlerTest(t *testing.T) {
	t.Parallel()
	body := `package api
func TestX(t *testing.T) {
  mux := http.NewServeMux()
  RegisterHandlers(mux, db)
  httptest.NewRequest("GET", "/", nil)
}
`
	err := ValidateImplementWrittenContent("", "", t.TempDir(), "linkshelf/internal/api/handlers_test.go", body, linkshelfHTTPProfile())
	if err == nil || !strings.Contains(err.Error(), "os.Chdir") {
		t.Fatalf("got %v", err)
	}
}

func TestValidateImplementWrittenContent_singlePackageServerAllowsStdlibOnlyMain(t *testing.T) {
	t.Parallel()
	// pwtest-style: the architecture defines the handler inside cmd/server/main.go,
	// so the workflow has no local handler package to import. Stdlib-only main.go
	// that registers routes must be accepted (not "only imports standard library").
	v := WorkflowValidation{
		LayoutRoot: "pingapp",
		RequiredFiles: []string{
			"pingapp/cmd/server/main.go",
			"pingapp/cmd/server/main_test.go",
		},
		QAVerifyCommand: "cd pingapp && go test ./...",
		TestRunner:      "go",
	}
	body := `package main

import (
	"encoding/json"
	"log"
	"net/http"
)

func pingHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"message": "pong"})
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", pingHandler)
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatal(err)
	}
}
`
	err := ValidateImplementWrittenContent("", "", t.TempDir(), "pingapp/cmd/server/main.go", body, v)
	if err != nil {
		t.Fatalf("single-package stdlib-only main must be accepted, got: %v", err)
	}
}

func TestValidateImplementWrittenContent_handlerPackageRequiresImport(t *testing.T) {
	t.Parallel()
	// When the workflow has a handler package, a stdlib-only main.go that starts
	// a server must still be rejected.
	v := linkshelfHTTPProfile()
	body := `package main

import (
	"log"
	"net/http"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {})
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatal(err)
	}
}
`
	err := ValidateImplementWrittenContent("", "", t.TempDir(), "linkshelf/cmd/server/main.go", body, v)
	if err == nil || !strings.Contains(err.Error(), "only imports standard library") {
		t.Fatalf("expected stdlib-only main rejection when handler package exists, got %v", err)
	}
}

func TestFormatHTTPRoutingGuidanceForBead_includesWebLayout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "mockrig"
	writeHTTPContractFixture(t, dir, rig, "<html></html>", "package api\n")
	got := FormatHTTPRoutingGuidanceForBead(dir, rig, "linkshelf/internal/api/handlers_test.go", linkshelfHTTPProfile())
	for _, want := range []string{`filepath.Join("..", "..")`, "/static/", "web/", "profile:"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}
