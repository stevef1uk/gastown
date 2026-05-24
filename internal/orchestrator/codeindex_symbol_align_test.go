package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchitectureContractSymbolNames_fromSpec(t *testing.T) {
	rigDir := t.TempDir()
	spec := "## Store\n```go\nfunc List(ctx context.Context) ([]Item, error)\nfunc Create(ctx context.Context, name string) (Item, error)\n```\n"
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{LayoutRoot: "myapp", QAVerifyCommand: "cd myapp && go test ./..."}
	got := ArchitectureContractSymbolNames(rigDir, "myapp/internal/store/store.go", v)
	if len(got) < 2 || got[0] != "Create" || got[1] != "List" {
		t.Fatalf("got %v", got)
	}
}

func TestFormatCodeindexSymbolAlignmentSection_flagsMismatch(t *testing.T) {
	rigDir := t.TempDir()
	storeDir := filepath.Join(rigDir, "myapp", "internal", "store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte("## Store\n```go\nfunc List() error\n```\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "store.go"), []byte("package store\n"), 0644); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(rigDir, codeindexIndexName)
	payload := `{"nodes":[{"id":"internal/store","symbols":[
		{"name":"List","kind":"function","exported":true},
		{"name":"InventedOnDisk","kind":"function","exported":true}
	]}]}`
	if err := os.WriteFile(index, []byte(payload), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{LayoutRoot: "myapp", QAVerifyCommand: "cd myapp && go test ./..."}
	bead := "myapp/internal/store/store.go"
	got := FormatCodeindexSymbolAlignmentSection(rigDir, bead, v, index, codeindexImpactCandidates(bead, v))
	if !strings.Contains(got, "Symbol alignment (codeindex vs architecture)") {
		t.Fatalf("want mismatch section:\n%s", got)
	}
	if !strings.Contains(got, "InventedOnDisk") {
		t.Fatalf("want index-only symbol flagged:\n%s", got)
	}
}

func TestFormatCodeindexContextForBead_defaultsToArchitectureWhenIndexEmpty(t *testing.T) {
	if !CodeindexEnabled() {
		t.Skip("codeindex not on PATH")
	}
	rigDir := t.TempDir()
	spec := "## Store\n```go\nfunc List(ctx context.Context) ([]Item, error)\nfunc Create(ctx context.Context, name string) (Item, error)\n```\n"
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(rigDir, codeindexIndexName)
	emptyIndex := `{"nodes":[{"id":"internal/store","symbols":[]}]}`
	if err := os.WriteFile(index, []byte(emptyIndex), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{LayoutRoot: "myapp", QAVerifyCommand: "cd myapp && go test ./..."}
	got := FormatCodeindexContextForBead(rigDir, "myapp/internal/store/store.go", v)
	for _, want := range []string{"architecture / SPEC (default)", "List (from architecture/SPEC)", "Create (from architecture/SPEC)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}
