package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

// linkshelfWebProfile matches a typical rig-flow web+API layout (see t.txt bug report).
func linkshelfWebProfile() orchestrator.WorkflowValidation {
	return orchestrator.WorkflowValidation{
		LayoutRoot: "linkshelf",
		RequiredFiles: []string{
			"linkshelf/web/index.html",
			"linkshelf/web/app.js",
			"linkshelf/cmd/server/main.go",
			"linkshelf/internal/store/store.go",
		},
		QAVerifyCommand: "cd linkshelf && go test ./...",
		TestRunner:      "custom",
	}
}

func writeLinkshelfWebFixture(t *testing.T, townRoot, rig string, indexHTML string) {
	t.Helper()
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	webDir := filepath.Join(rigDir, "linkshelf", "web")
	if err := os.MkdirAll(webDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte(indexHTML), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webDir, "app.js"), []byte("export {};\n"), 0644); err != nil {
		t.Fatal(err)
	}
	serverDir := filepath.Join(rigDir, "linkshelf", "cmd", "server")
	if err := os.MkdirAll(serverDir, 0755); err != nil {
		t.Fatal(err)
	}
	mainGo := `package main
import "net/http"
func main() {
  http.HandleFunc("/api/bookmarks", func(w http.ResponseWriter, r *http.Request) {})
  http.ListenAndServe(":8080", nil)
}
`
	if err := os.WriteFile(filepath.Join(serverDir, "main.go"), []byte(mainGo), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestValidateWebStaticReferences_rejectsWrongStaticPrefix(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "mockrig"
	// Bug from t.txt: /static/app.js when server serves web/ at /
	writeLinkshelfWebFixture(t, dir, rig, `<!DOCTYPE html>
<html><head><script src="/static/app.js"></script></head><body></body></html>
`)
	v := linkshelfWebProfile()
	err := validateWebStaticReferences(dir, rig, v)
	if err == nil {
		t.Fatal("expected reject for /static/app.js when asset is /app.js")
	}
	if !strings.Contains(err.Error(), "static") && !strings.Contains(err.Error(), "app.js") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateWebStaticReferences_acceptsCorrectAssetPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "mockrig"
	writeLinkshelfWebFixture(t, dir, rig, `<!DOCTYPE html>
<html><head><script src="/app.js"></script></head><body></body></html>
`)
	v := linkshelfWebProfile()
	if err := validateWebStaticReferences(dir, rig, v); err != nil {
		t.Fatalf("expected pass for /app.js: %v", err)
	}
}

func TestValidateWebStaticReferences_rejectsSPARouteWithoutServerHandler(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "mockrig"
	// Bug from t.txt: href="/bookmarks" on SPA with no /bookmarks route
	writeLinkshelfWebFixture(t, dir, rig, `<!DOCTYPE html>
<html><body><nav><a href="/bookmarks">Bookmarks</a></nav></body></html>
`)
	v := linkshelfWebProfile()
	err := validateWebStaticReferences(dir, rig, v)
	if err == nil {
		t.Fatal("expected reject for /bookmarks without server route")
	}
	if !strings.Contains(err.Error(), "bookmarks") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateWebStaticReferences_acceptsSPAInPageAnchor(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "mockrig"
	writeLinkshelfWebFixture(t, dir, rig, `<!DOCTYPE html>
<html><body><nav><a href="/#bookmarks">Bookmarks</a></nav></body></html>
`)
	v := linkshelfWebProfile()
	if err := validateWebStaticReferences(dir, rig, v); err != nil {
		t.Fatalf("expected pass for /#bookmarks: %v", err)
	}
}

func TestValidateWebStaticReferences_acceptsRouteDefinedInGo(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "mockrig"
	writeLinkshelfWebFixture(t, dir, rig, `<!DOCTYPE html>
<html><body><a href="/api/bookmarks">API</a></body></html>
`)
	v := linkshelfWebProfile()
	if err := validateWebStaticReferences(dir, rig, v); err != nil {
		t.Fatalf("expected pass when Go defines /api/bookmarks: %v", err)
	}
}

func TestRequiresQARuntimeSmoke_webAndServer(t *testing.T) {
	t.Parallel()
	v := linkshelfWebProfile()
	if !requiresQARuntimeSmoke(v) {
		t.Fatal("expected runtime smoke for web+server profile")
	}
	v2 := orchestrator.WorkflowValidation{LayoutRoot: "linkshelf", RequiredFiles: []string{"linkshelf/cmd/server/main.go"}}
	if requiresQARuntimeSmoke(v2) {
		t.Fatal("expected no smoke without web assets")
	}
}

func TestIsQARuntimeSmokeCommandOK(t *testing.T) {
	t.Parallel()
	v := linkshelfWebProfile()
	ok := `cd mockrig/mayor/rig/linkshelf && go run ./cmd/server & sleep 1 && curl -sf http://127.0.0.1:8080/ && curl -s http://127.0.0.1:8080/api/bookmarks | grep -q '[]' && curl -sf -X POST -H 'Content-Type: application/json' -d '{"title":"x","url":"https://a"}' http://127.0.0.1:8080/api/bookmarks`
	if !isQARuntimeSmokeCommandOK(ok, v) {
		t.Fatal("expected full smoke CMD to qualify")
	}
	bad := []string{
		"curl http://127.0.0.1:8080/api/bookmarks",
		"go run ./cmd/server",
		"go run ./cmd/server && curl http://localhost:8080/",
	}
	for _, cmd := range bad {
		if isQARuntimeSmokeCommandOK(cmd, v) {
			t.Fatalf("expected reject: %q", cmd)
		}
	}
}

func TestValidateQAArtifacts_requiresRuntimeSmokeWhenProfileHasWebAPI(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "mockrig"
	indexHTML := `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Link Shelf</title>
  <script src="/app.js" defer></script>
</head>
<body>
  <header><h1>Link Shelf</h1></header>
  <nav><a href="/#bookmarks">Bookmarks</a></nav>
  <main id="bookmarks">
    <p>Your saved links appear below.</p>
    <ul id="list"></ul>
  </main>
  <form id="add">
    <label>Title <input name="title" required></label>
    <label>URL <input name="url" type="url" required></label>
    <button type="submit">Add bookmark</button>
  </form>
</body>
</html>
`
	writeLinkshelfWebFixture(t, dir, rig, indexHTML)
	v := linkshelfWebProfile()
	v = orchestrator.ClampProfileValidation(v)
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	appJS := strings.Repeat(`export async function loadBookmarks() {
  const res = await fetch("/api/bookmarks");
  const data = await res.json();
  return data.map(b => b.title);
}
`, 3)
	if err := os.WriteFile(filepath.Join(rigDir, "linkshelf", "web", "app.js"), []byte(appJS), 0644); err != nil {
		t.Fatal(err)
	}
	storeDir := filepath.Join(rigDir, "linkshelf", "internal", "store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	storeGo := strings.Repeat("package store\n\n// Bookmark model and persistence with CRUD.\nfunc List() []*Bookmark { return make([]*Bookmark, 0) }\n", 15)
	if err := os.WriteFile(filepath.Join(storeDir, "store.go"), []byte(storeGo), 0644); err != nil {
		t.Fatal(err)
	}
	schemaGo := strings.Repeat("package store\n\n// DDL and InitSchema for SQLite persistence.\nfunc InitSchema() error { return nil }\n", 3)
	if err := os.WriteFile(filepath.Join(storeDir, "schema.go"), []byte(schemaGo), 0644); err != nil {
		t.Fatal(err)
	}
	err := validateQAArtifacts(dir, rig, "all_passed", false, true, true, false, v)
	if err == nil || !strings.Contains(err.Error(), "live smoke") {
		t.Fatalf("expected live smoke requirement, got: %v", err)
	}
}
