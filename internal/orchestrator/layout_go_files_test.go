package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGoCompileVerifyCommandForBead_frontendReturnsEmpty(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot: "linkshelf",
		RequiredFiles: []string{
			"linkshelf/web/index.html",
			"linkshelf/web/style.css",
			"linkshelf/web/app.js",
		},
	}
	dir := t.TempDir()
	for _, path := range []string{
		"linkshelf/web/index.html",
		"linkshelf/web/style.css",
		"linkshelf/web/app.js",
	} {
		got := GoCompileVerifyCommandForBead(v, dir, path)
		if got != "" {
			t.Errorf("frontend path %q should return empty verify cmd, got %q", path, got)
		}
	}
}

func TestGoCompileVerifyCommandForBead_storePackage(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot: "linkshelf",
		RequiredFiles: []string{
			"linkshelf/internal/store/store.go",
			"linkshelf/internal/store/store_test.go",
		},
	}
	dir := t.TempDir()
	got := GoCompileVerifyCommandForBead(v, dir, "linkshelf/internal/store/store.go")
	if got != "cd linkshelf && go mod tidy && go build ./internal/store/..." {
		t.Fatalf("production bead before test file exists: got %q", got)
	}
	testFile := filepath.Join(dir, "linkshelf/internal/store/store_test.go")
	if err := os.MkdirAll(filepath.Dir(testFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(testFile, []byte("package store\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got = GoCompileVerifyCommandForBead(v, dir, "linkshelf/internal/store/store.go")
	if got != "cd linkshelf && go mod tidy && go test -count=1 ./internal/store/..." {
		t.Fatalf("production bead after test file exists: got %q", got)
	}
}

func TestPruneStaleLayoutGoFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "rig"
	layout := filepath.Join(dir, rig, "mayor", "rig", "linkshelf", "internal", "store")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "store.go"), []byte("package store\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "sqlite.go"), []byte("package store\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot: "linkshelf",
		RequiredFiles: []string{
			"linkshelf/internal/store/store.go",
		},
	}
	removed, err := PruneStaleLayoutGoFiles(dir, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "linkshelf/internal/store/sqlite.go" {
		t.Fatalf("removed = %v", removed)
	}
	if _, err := os.Stat(filepath.Join(layout, "sqlite.go")); !os.IsNotExist(err) {
		t.Fatal("sqlite.go should be gone")
	}
}

func TestPruneStaleLayoutGoFiles_keepsCorrelatedTest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "rig"
	layout := filepath.Join(dir, rig, "mayor", "rig", "linkshelf", "internal", "store")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"store.go":      "package store\n",
		"store_test.go": "package store\n",
		"extra.go":      "package store\n",
	} {
		if err := os.WriteFile(filepath.Join(layout, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	v := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		QAVerifyCommand: "cd linkshelf && go test ./...",
		RequiredFiles: []string{"linkshelf/internal/store/store.go"},
	}
	removed, err := PruneStaleLayoutGoFiles(dir, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "linkshelf/internal/store/extra.go" {
		t.Fatalf("removed = %v", removed)
	}
	if _, err := os.Stat(filepath.Join(layout, "store_test.go")); err != nil {
		t.Fatalf("store_test.go should remain: %v", err)
	}
}

func TestLayoutGoRelPathsProtectedFromPrune_includesCorrelatedTest(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		QAVerifyCommand: "cd linkshelf && go test ./...",
		RequiredFiles: []string{"linkshelf/internal/store/schema.go"},
	}
	got := layoutGoRelPathsProtectedFromPrune(v)
	if !got["internal/store/schema.go"] || !got["internal/store/schema_test.go"] {
		t.Fatalf("protected = %v", got)
	}
}
