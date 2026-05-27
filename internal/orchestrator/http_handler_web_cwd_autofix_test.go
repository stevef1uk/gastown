package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoTestOutputSuggestsHandlerWebCwd404_legacyServeIndex404(t *testing.T) {
	out := `--- FAIL: TestServeIndex (0.00s)
    handlers_test.go:52: expected status 200, got 404
FAIL`
	if !goTestOutputSuggestsHandlerWebCwd404Legacy(out) {
		t.Fatal("expected legacy cwd 404 detection")
	}
}

func TestTryAutoFixHandlerWebCwd404_handlersAndTests(t *testing.T) {
	dir := t.TempDir()
	layout := "linkshelf"
	webDir := filepath.Join(dir, layout, "web")
	apiDir := filepath.Join(dir, layout, "internal", "api")
	if err := os.MkdirAll(webDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(apiDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, layout, "go.mod"), []byte("module linkshelf\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<html></html>\n"), 0644); err != nil {
		t.Fatal(err)
	}
	handlers := filepath.Join(apiDir, "handlers.go")
	handlersSrc := `package api

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
)

func serveIndex(w http.ResponseWriter, r *http.Request) {
	wd, err := os.Getwd()
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	f, err := os.Open(filepath.Join(wd, "web", "index.html"))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer f.Close()
	_, _ = io.Copy(w, f)
}
`
	if err := os.WriteFile(handlers, []byte(handlersSrc), 0644); err != nil {
		t.Fatal(err)
	}
	testPath := filepath.Join(apiDir, "handlers_test.go")
	testSrc := `package api

import (
	"os"
	"path/filepath"
	"testing"
)

func changeDirToRepoRoot(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Chdir(root)
}

func TestMain(m *testing.M) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		panic(err)
	}
	_ = os.Chdir(root)
	os.Exit(m.Run())
}

func TestServeIndex(t *testing.T) {
	changeDirToRepoRoot(t)
}
`
	if err := os.WriteFile(testPath, []byte(testSrc), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:    layout,
		RequiredFiles: []string{layout + "/internal/api/handlers.go", layout + "/web/index.html"},
	}
	cmdOut := `handlers_test.go:52: expected status 200, got 404
--- FAIL: TestServeIndex`
	if !ShouldAutoFixHandlerWebCwd404(dir, "", "", v, cmdOut) {
		t.Fatal("ShouldAutoFixHandlerWebCwd404 = false")
	}
	h, testRel := HandlerHTTPPathsForAutoFix(dir, v)
	if h == "" {
		t.Fatalf("HandlerHTTPPathsForAutoFix: handlers path missing (test=%q)", testRel)
	}
	if !serveIndexGetwdOpenRE.MatchString(handlersSrc) {
		t.Fatal("serveIndexGetwdOpenRE does not match fixture handlers")
	}
	fixed, err := TryAutoFixHandlerWebCwd404(dir, "", "", v, cmdOut)
	if err != nil {
		t.Fatal(err)
	}
	if len(fixed) != 2 {
		t.Fatalf("fixed paths = %v, want 2", fixed)
	}
	hData, _ := os.ReadFile(handlers)
	if !strings.Contains(string(hData), "runtime.Caller") {
		t.Fatalf("handlers.go not patched: %s", hData)
	}
	td, _ := os.ReadFile(testPath)
	if !strings.Contains(string(td), "findModuleRootWithWeb") {
		t.Fatalf("handlers_test.go not patched: %s", td)
	}
}
