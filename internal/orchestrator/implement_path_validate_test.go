package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSanitizeNativeEditRelPath(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, want string }{
		{"`linkshelf/go.mod`", "linkshelf/go.mod"},
		{"**linkshelf/foo.go**", "linkshelf/foo.go"},
		{"testgt3/mayor/rig/linkshelf/web/style.css", "linkshelf/web/style.css"},
		{"` command to create the file.", "command to create the file."},
	}
	for _, tc := range tests {
		if got := SanitizeNativeEditRelPath(tc.in); got != tc.want {
			t.Fatalf("SanitizeNativeEditRelPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIsValidImplementBeadPath_rejectsProsePaths(t *testing.T) {
	t.Parallel()
	bad := []string{
		"linkshelf/` command to create the file.",
		"linkshelf/** to create it per architecture and plan acceptance.",
		"linkshelf/ command to create this file.",
	}
	for _, p := range bad {
		if IsValidImplementBeadPath(p) {
			t.Fatalf("want invalid %q", p)
		}
	}
}

func TestIsMalformedLayoutArtifact(t *testing.T) {
	t.Parallel()
	if !IsMalformedLayoutArtifact("linkshelf/` command to create the file.") {
		t.Fatal("expected malformed")
	}
	if IsMalformedLayoutArtifact("linkshelf/go.mod") {
		t.Fatal("go.mod should not be malformed")
	}
}

func TestRemoveMalformedLayoutArtifactFiles(t *testing.T) {
	dir := t.TempDir()
	layout := filepath.Join(dir, "linkshelf")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	junk := filepath.Join(layout, "` command to create the file.")
	if err := os.WriteFile(junk, []byte("junk"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "go.mod"), []byte("module x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{LayoutRoot: "linkshelf"}
	removed, err := RemoveMalformedLayoutArtifactFiles(dir, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 {
		t.Fatalf("removed = %v", removed)
	}
	if _, err := os.Stat(junk); !os.IsNotExist(err) {
		t.Fatalf("junk still exists: %v", err)
	}
}
