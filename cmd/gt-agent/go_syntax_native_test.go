package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestValidateNativeGoContent_rejectsSyntaxError(t *testing.T) {
	src := "package store\n\nfunc f() {}\n}n err\n"
	_, err := validateAndNormalizeNativeGoContent("linkshelf/internal/store/store.go", src)
	if err == nil {
		t.Fatal("expected syntax rejection")
	}
	if !strings.Contains(err.Error(), "syntax invalid") {
		t.Fatalf("err=%v", err)
	}
}

func TestValidateNativeGoContent_rejectsMergeFragments(t *testing.T) {
	src := "package store\n\nfunc f() {}\n}; if err != nil {\n"
	_, err := validateAndNormalizeNativeGoContent("store.go", src)
	if err == nil {
		t.Fatal("expected merge-fragment rejection")
	}
	if !strings.Contains(err.Error(), "merged patch fragments") {
		t.Fatalf("err=%v", err)
	}
}

func TestValidateNativeGoContent_acceptsValidPackage(t *testing.T) {
	src := "package store\n\nfunc List() {}\n"
	if _, err := validateAndNormalizeNativeGoContent("store.go", src); err != nil {
		t.Fatal(err)
	}
}

func TestValidateNativeGoContent_rejectsNonPackageSnippet(t *testing.T) {
	_, err := validateAndNormalizeNativeGoContent("notes.go", "not go at all\n")
	if err == nil {
		t.Fatal("expected rejection for non-package Go content")
	}
	if !strings.Contains(err.Error(), "Go syntax invalid") {
		t.Fatalf("err=%v", err)
	}
}

func TestAppendGoCompileSourceContext_storeIsolationHint(t *testing.T) {
	dir := t.TempDir()
	layout := filepath.Join(dir, "linkshelf", "internal", "store")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	storeGo := `package store

func OpenDB() (*Store, error) {
	return nil, sql.Open("sqlite3", "./links.db")
}
`
	testGo := `package store

func setup(t *testing.T) {
	_ = t.TempDir()
	OpenDB()
}
`
	if err := os.WriteFile(filepath.Join(layout, "store.go"), []byte(storeGo), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "store_test.go"), []byte(testGo), 0644); err != nil {
		t.Fatal(err)
	}
	out := `FAIL	linkshelf/internal/store [build failed]
--- FAIL: TestAddBookmark (0.00s)
    store_test.go:41: expected 1 bookmark, got 69
FAIL
exit status 1
`
	var b strings.Builder
	v := orchestrator.WorkflowValidation{LayoutRoot: "linkshelf"}
	appendGoCompileSourceContext(&b, "", "", dir, "linkshelf", "linkshelf/internal/store/store.go", v,
		"go test -count=1 ./internal/store/...", out)
	got := b.String()
	if !strings.Contains(got, "SQLite test isolation") {
		t.Fatalf("want isolation hint, got:\n%s", got)
	}
}

func TestAppendGoCompileSourceContext_corruptedFileRecoveryHint(t *testing.T) {
	dir := t.TempDir()
	storeDir := filepath.Join(dir, "linkshelf", "internal", "store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "store.go"), []byte("package store\n}n err\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	v := orchestrator.WorkflowValidation{LayoutRoot: "linkshelf"}
	appendGoCompileSourceContext(&b, "", "", dir, "linkshelf", "linkshelf/internal/store/store.go", v,
		"go build ./internal/store/...", "linkshelf/internal/store/store.go:2:1: syntax error: unexpected name n")
	got := b.String()
	if !strings.Contains(got, "Corrupted Go file recovery") {
		t.Fatalf("want recovery hint, got:\n%s", got)
	}
	if !strings.Contains(got, "WRITE:") {
		t.Fatal("recovery hint should mention WRITE")
	}
}

func TestApplyNativeSearchReplace_rejectsInvalidGoResult(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "store.go")
	if err := os.WriteFile(path, []byte("package store\n\nconst x = 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := applyNativeSearchReplace(path, "const x = 1", "const x = 1\n}n err\n", false)
	if err == nil {
		t.Fatal("expected rejection when replace breaks Go syntax")
	}
}
