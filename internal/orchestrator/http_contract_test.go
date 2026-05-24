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
)
func Mount(mux *http.ServeMux, webRoot string) {
  webRoot = filepath.Join(webRoot, "web")
  mux.HandleFunc("/static/", func(w http.ResponseWriter, r *http.Request) {})
}
`)
	if err := ValidateHTTPContract(dir, rig, v); err != nil {
		t.Fatal(err)
	}
}

func TestValidateImplementWrittenContent_rejectsChdirInHandlerTest(t *testing.T) {
	t.Parallel()
	body := `package api
import "os"
func TestX(t *testing.T) {
  os.Chdir(t.TempDir())
}
`
	err := ValidateImplementWrittenContent(t.TempDir(), "linkshelf/internal/api/handlers_test.go", body, linkshelfHTTPProfile())
	if err == nil || !strings.Contains(err.Error(), "os.Chdir") {
		t.Fatalf("got %v", err)
	}
}

func TestFormatHTTPRoutingGuidanceForBead_includesWebLayout(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "mockrig"
	writeHTTPContractFixture(t, dir, rig, "<html></html>", "package api\n")
	got := FormatHTTPRoutingGuidanceForBead(dir, rig, "linkshelf/internal/api/handlers_test.go", linkshelfHTTPProfile())
	for _, want := range []string{"os.Chdir", "/static/", "web/", "table-driven"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}
