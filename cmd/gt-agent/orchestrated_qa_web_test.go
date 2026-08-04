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
		DevServerPort:   8080,
	}
}

func writeLinkshelfArchitecture(t *testing.T, rigDir string, withStaticPrefix bool) {
	t.Helper()
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	var arch string
	if withStaticPrefix {
		arch = `# Link Shelf HTTP

| Method | Path | Notes |
| GET | / | web/index.html |
| GET | /static/{file} | serves web/{file} |
| GET | /api/links | JSON array |
`
	} else {
		arch = `# Link Shelf HTTP

| Method | Path | Notes |
| GET | / | web/index.html |
| GET | /api/bookmarks | JSON array |
`
	}
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
}

func writeLinkshelfWebFixture(t *testing.T, townRoot, rig string, indexHTML string) {
	writeLinkshelfWebFixtureArch(t, townRoot, rig, indexHTML, false)
}

func writeLinkshelfWebFixtureArch(t *testing.T, townRoot, rig string, indexHTML string, staticPrefixArch bool) {
	t.Helper()
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeLinkshelfArchitecture(t, rigDir, staticPrefixArch)
	webDir := filepath.Join(rigDir, "linkshelf", "web")
	if err := os.MkdirAll(webDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte(indexHTML), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webDir, "app.js"), []byte("// frontend app.js\n"), 0644); err != nil {
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

func TestValidateWebStaticReferences_acceptsStaticPrefixFromArchitecture(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "mockrig"
	writeLinkshelfWebFixtureArch(t, dir, rig, `<!DOCTYPE html>
<html><head><script src="/static/app.js"></script></head><body></body></html>
`, true)
	v := linkshelfWebProfile()
	if err := validateWebStaticReferences(dir, rig, v); err != nil {
		t.Fatalf("expected pass for /static/app.js mapped to web/app.js: %v", err)
	}
}

func TestValidateWebStaticReferences_rejectsRootPathWhenArchitectureUsesStatic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "mockrig"
	writeLinkshelfWebFixtureArch(t, dir, rig, `<!DOCTYPE html>
<html><head><script src="/app.js"></script></head><body></body></html>
`, true)
	v := linkshelfWebProfile()
	err := validateWebStaticReferences(dir, rig, v)
	if err == nil {
		t.Fatal("expected reject for /app.js when architecture defines /static/{file}")
	}
	if !strings.Contains(err.Error(), "/static/app.js") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateWebStaticReferences_acceptsRootPathWithoutStaticArch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "mockrig"
	writeLinkshelfWebFixture(t, dir, rig, `<!DOCTYPE html>
<html><head><script src="/app.js"></script></head><body></body></html>
`)
	v := linkshelfWebProfile()
	if err := validateWebStaticReferences(dir, rig, v); err != nil {
		t.Fatalf("expected pass for /app.js when architecture has no /static/ route: %v", err)
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
	dir := t.TempDir()
	rig := "mockrig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeLinkshelfArchitecture(t, rigDir, true)
	v := linkshelfWebProfile()
	if !requiresQARuntimeSmoke(dir, rig, v) {
		t.Fatal("expected runtime smoke when SPEC/architecture define API routes")
	}
	v2 := orchestrator.WorkflowValidation{LayoutRoot: "linkshelf", RequiredFiles: []string{"linkshelf/cmd/server/main.go"}}
	if requiresQARuntimeSmoke(dir, rig, v2) {
		t.Fatal("expected no smoke without web assets")
	}
}

func TestRequiresQARuntimeSmoke_skipsAPIWhenSpecHasNoAPI(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "mockrig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	arch := `# Static site only
| GET | / | index.html |
`
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	v := linkshelfWebProfile()
	if !requiresQARuntimeSmoke(dir, rig, v) {
		t.Fatal("expected static web smoke when web+server profile")
	}
	spec, _ := orchestrator.LoadAPISmokeSpecFromRig(dir, rig, v)
	if orchestrator.APISmokeHasHTTPAPI(spec) {
		t.Fatalf("expected no API paths in spec: %+v", spec)
	}
}

func TestIsQARuntimeSmokeCommandOK(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "mockrig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeLinkshelfArchitecture(t, rigDir, false)
	v := linkshelfWebProfile()
	ok := `cd mockrig/mayor/rig/linkshelf && go run ./cmd/server & sleep 1 && curl -sf http://127.0.0.1:8080/ && curl -s http://127.0.0.1:8080/api/bookmarks | grep -q '[]' && curl -sf -X POST -H 'Content-Type: application/json' -d '{"title":"x","url":"https://a"}' http://127.0.0.1:8080/api/bookmarks`
	if !isQARuntimeSmokeCommandOK(ok, dir, rig, v) {
		t.Fatal("expected full smoke CMD to qualify")
	}
	bad := []string{
		"curl http://127.0.0.1:8080/api/bookmarks",
		"go run ./cmd/server",
		"go run ./cmd/server && curl http://localhost:8080/",
	}
	for _, cmd := range bad {
		if isQARuntimeSmokeCommandOK(cmd, dir, rig, v) {
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
	appJS := strings.Repeat(`async function loadBookmarks() {
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
	err := validateQAArtifacts(dir, rig, "all_passed", false, true, true, false, false, v)
	if err == nil || !strings.Contains(err.Error(), "this gt-agent session") {
		t.Fatalf("expected live smoke requirement this session, got: %v", err)
	}
}
