package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGoCompileVerifyCommandForBead_storePackage(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{LayoutRoot: "linkshelf"}
	got := GoCompileVerifyCommandForBead(v, "linkshelf/internal/store/store.go")
	if got != "cd linkshelf && go mod tidy && go build ./internal/store/..." {
		t.Fatalf("got %q", got)
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
