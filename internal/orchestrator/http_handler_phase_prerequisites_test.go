package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureHandlerTestMainStoreDB_patchesMissingAssignment(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "mayor", "rig")
	testRel := "linkshelf/internal/api/handlers_test.go"
	abs := filepath.Join(rigDir, filepath.FromSlash(testRel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	src := `package api

import (
	"database/sql"
	"log"
	"os"
	"testing"

	"github.com/example/linkshelf/internal/store"
	_ "github.com/mattn/go-sqlite3"
)

func TestMain(m *testing.M) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		log.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := store.InitSchema(db); err != nil {
		log.Fatalf("failed to initialize schema: %v", err)
	}
	os.Exit(m.Run())
}
`
	if err := os.WriteFile(abs, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{LayoutRoot: "linkshelf"}
	fixed, err := ensureHandlerTestMainStoreDB(rigDir, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(fixed) != 1 || fixed[0] != testRel {
		t.Fatalf("fixed = %v", fixed)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "store.DB = db") {
		t.Fatalf("expected store.DB assignment, got:\n%s", data)
	}
}

func TestTryScaffoldWebAssetsForHandlerTestFailure(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "mayor", "rig")
	v := handlerPrereqProfile()
	out := `--- FAIL: TestHandleRoot (0.00s)
    handlers_test.go:40: expected status 200, got 404
FAIL`
	created, err := TryScaffoldWebAssetsForHandlerTestFailure(rigDir, v, out)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 3 {
		t.Fatalf("created = %v", created)
	}
}

func TestFormatHandlerDeleteNotFoundHint(t *testing.T) {
	out := `--- FAIL: TestHandleDeleteLinkNotFound (0.00s)
    handlers_test.go:164: expected status 404, got 204`
	h := FormatHandlerDeleteNotFoundHint(out, WorkflowValidation{LayoutRoot: "linkshelf"})
	if h == "" || !strings.Contains(h, "RowsAffected") {
		t.Fatalf("hint = %q", h)
	}
}

func TestEnsureMinimalWebAssetsForHandlerTests(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "mayor", "rig")
	v := handlerPrereqProfile()
	created, err := ensureMinimalWebAssetsForHandlerTests(rigDir, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 3 {
		t.Fatalf("created = %v", created)
	}
	for _, rel := range []string{
		"linkshelf/web/index.html",
		"linkshelf/web/app.js",
		"linkshelf/web/style.css",
	} {
		if _, err := os.Stat(filepath.Join(rigDir, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("missing %s: %v", rel, err)
		}
	}
}
