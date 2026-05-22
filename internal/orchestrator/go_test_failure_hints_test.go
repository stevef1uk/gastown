package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSourcePathForCorrelatedTest(t *testing.T) {
	t.Parallel()
	got := SourcePathForCorrelatedTest("linkshelf/internal/store/store_test.go", "linkshelf")
	if got != "linkshelf/internal/store/store.go" {
		t.Fatalf("got %q", got)
	}
}

func TestGoTestFailureProductionPaths(t *testing.T) {
	t.Parallel()
	out := "--- FAIL: TestStore_List_Empty (0.00s)\n    store_test.go:31: List returned nil slice, want empty slice\nFAIL\tlinkshelf/internal/store\t0.003s\n"
	paths := GoTestFailureProductionPaths(out, "linkshelf")
	if len(paths) != 1 || paths[0] != "linkshelf/internal/store/store.go" {
		t.Fatalf("paths = %v", paths)
	}
}

func TestCompileErrorPathsIncludingClosedDeps_goTestIncludesClosedStore(t *testing.T) {
	t.Parallel()
	townRoot := t.TempDir()
	rig := "rig"
	v := WorkflowValidation{
		LayoutRoot:        "linkshelf",
		BeadTitleContains: "Implement linkshelf/",
		RequiredFiles: []string{
			"linkshelf/internal/store/schema.go",
			"linkshelf/internal/store/store.go",
			"linkshelf/internal/store/store_test.go",
		},
	}
	setListImplementBeadsByStatusHook(t, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "closed" {
			return []PlanBead{{ID: "te-uam", Title: "Implement linkshelf/internal/store/store.go per architecture"}}, nil
		}
		return []PlanBead{{ID: "te-8cz", Title: "Implement linkshelf/internal/store/schema.go per architecture"}}, nil
	})

	out := "--- FAIL: TestStore_List_Empty (0.00s)\n    store_test.go:31: List returned nil slice, want empty slice\nFAIL\tlinkshelf/internal/store\t0.003s\n"
	paths := CompileErrorPathsIncludingClosedDeps(townRoot, rig, "linkshelf/internal/store/schema.go", nil, out, v)
	found := false
	for _, p := range paths {
		if strings.Contains(p, "store.go") && !strings.Contains(p, "_test") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected store.go in paths, got %v", paths)
	}
}

func TestGoCompileVerifyCommandForBead_schemaScopedWhenStoreTestPresent(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot: "linkshelf",
		RequiredFiles: []string{
			"linkshelf/internal/store/schema.go",
			"linkshelf/internal/store/store.go",
			"linkshelf/internal/store/store_test.go",
		},
	}
	dir := t.TempDir()
	storeDir := filepath.Join(dir, "linkshelf/internal/store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	schemaTest := `package store

import "testing"

func TestInitSchema(t *testing.T) {}
`
	if err := os.WriteFile(filepath.Join(storeDir, "schema_test.go"), []byte(schemaTest), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "store_test.go"), []byte("package store\nfunc TestStore_List_Empty(t *testing.T) {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got := GoCompileVerifyCommandForBead(v, dir, "linkshelf/internal/store/schema.go")
	if !strings.Contains(got, "-run 'TestInitSchema'") {
		t.Fatalf("expected scoped -run for schema bead, got %q", got)
	}
	if strings.Contains(got, "go build") {
		t.Fatalf("expected go test not build when schema_test exists: %q", got)
	}
}

func TestFormatGoTestFailureHints_nilSlice(t *testing.T) {
	t.Parallel()
	out := `store_test.go:31: List returned nil slice, want empty slice`
	hint := FormatGoTestFailureHints("", "", "linkshelf/internal/store/schema.go", out, nil, WorkflowValidation{LayoutRoot: "linkshelf"})
	if !strings.Contains(hint, "make([]") {
		t.Fatalf("hint = %q", hint)
	}
}
