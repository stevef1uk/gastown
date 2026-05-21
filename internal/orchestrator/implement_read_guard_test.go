package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractImplementReadPathsFromCmd(t *testing.T) {
	t.Parallel()
	paths := ExtractImplementReadPathsFromCmd(
		"cd rig/mayor/rig && cat linkshelf/internal/store/store_test.go && head linkshelf/internal/store/store.go",
		"linkshelf",
	)
	if len(paths) != 2 {
		t.Fatalf("paths = %v", paths)
	}
	if paths[0] != "linkshelf/internal/store/store_test.go" {
		t.Fatalf("first = %q", paths[0])
	}
}

func TestValidateImplementReadMissingFile_separateTestBead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "rig"
	store := filepath.Join(dir, rig, "mayor", "rig", "linkshelf", "internal", "store")
	if err := os.MkdirAll(store, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "store.go"), []byte("package store\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		TestRunner:    "go",
		RequiredFiles: []string{
			"linkshelf/internal/store/store.go",
			"linkshelf/internal/store/store_test.go",
		},
	}
	err := ValidateImplementReadMissingFile(dir, rig, "te-store", "linkshelf/internal/store/store.go", "linkshelf/internal/store/store_test.go", v)
	if err == nil {
		t.Fatal("expected error for missing test file on production bead")
	}
	if !strings.Contains(err.Error(), "separate implement bead") {
		t.Fatalf("error = %v", err)
	}
}

func TestEnsureTestBeadSkeletonForPath_go(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "rig"
	testPath := filepath.Join(dir, rig, "mayor", "rig", "linkshelf", "internal", "store", "store_test.go")
	v := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		TestRunner:    "go",
		RequiredFiles: []string{"linkshelf/internal/store/store_test.go"},
	}

	rel, created, err := EnsureTestBeadSkeletonForPath(dir, rig, "linkshelf/internal/store/store_test.go", v)
	if err != nil || !created || rel != "linkshelf/internal/store/store_test.go" {
		t.Fatalf("EnsureTestBeadSkeleton = %q %v %v", rel, created, err)
	}
	data, err := os.ReadFile(testPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "package store") || !strings.Contains(string(data), "TestPlaceholder") {
		t.Fatalf("skeleton = %s", data)
	}
}
