package rig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResetLayoutPreImplementation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	layout := filepath.Join(dir, "linkshelf")
	for _, path := range []string{
		"linkshelf/go.mod",
		"linkshelf/go.sum",
		"linkshelf/internal/store/store.go",
		"linkshelf/web/index.html",
		"linkshelf/server",
		"linkshelf/linkshelf.db",
		"linkshelf/block:",
	} {
		p := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := ResetLayoutPreImplementation(dir, "linkshelf")
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) < 5 {
		t.Fatalf("removed = %v", removed)
	}
	for _, keep := range []string{"linkshelf/go.mod", "linkshelf/go.sum"} {
		if _, err := os.Stat(filepath.Join(dir, keep)); err != nil {
			t.Fatalf("should keep %s: %v", keep, err)
		}
	}
	if _, err := os.Stat(filepath.Join(layout, "internal", "store", "store.go")); !os.IsNotExist(err) {
		t.Fatal("store.go should be removed")
	}
}

func TestInferLayoutRootFromMayorRig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	spec := "# Spec\n\n## File layout\n\n```\nlinkshelf/\n├── go.mod\n```\n"
	if err := os.WriteFile(filepath.Join(dir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	if got := InferLayoutRootFromMayorRig(dir); got != "linkshelf" {
		t.Fatalf("got %q want linkshelf", got)
	}
}
