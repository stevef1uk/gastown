package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCodeindexIndexNeedsRefresh_missingIndex(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if !codeindexIndexNeedsRefresh(filepath.Join(dir, "codeindex.json"), dir) {
		t.Fatal("want refresh when index missing")
	}
}

func TestCodeindexIndexNeedsRefresh_staleIndex(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	src := filepath.Join(dir, "pkg")
	if err := os.MkdirAll(src, 0755); err != nil {
		t.Fatal(err)
	}
	goFile := filepath.Join(src, "a.go")
	if err := os.WriteFile(goFile, []byte("package pkg\n"), 0644); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(dir, "codeindex.json")
	if err := os.WriteFile(index, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(index, past, past); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Minute)
	if err := os.Chtimes(goFile, future, future); err != nil {
		t.Fatal(err)
	}
	if !codeindexIndexNeedsRefresh(index, dir) {
		t.Fatal("want refresh when sources newer than index")
	}
}

func TestFormatCodeindexContextForBead_disabled(t *testing.T) {
	t.Setenv("GT_CODEINDEX", "0")
	got := FormatCodeindexContextForBead(t.TempDir(), "linkshelf/foo.go", WorkflowValidation{LayoutRoot: "linkshelf"})
	if got != "" {
		t.Fatalf("want empty when disabled, got %q", got)
	}
}

func TestTruncateCodeindexText(t *testing.T) {
	t.Parallel()
	got := truncateCodeindexText("abcdef", 4)
	if got != "abcd\n…" {
		t.Fatalf("got %q", got)
	}
}
