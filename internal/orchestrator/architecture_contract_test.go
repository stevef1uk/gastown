package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatArchitectureContractForBead_httpFromSpecTable(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "rig", "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	spec := "# App\n\n## HTTP API\n\n| Method | Path | Response |\n| GET | /api/items | 200, JSON array |\n| POST | /api/items | 201 |\n"
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:      "myapp",
		QAVerifyCommand: "cd myapp && go test ./...",
	}
	got := FormatArchitectureContractForBead(dir, "rig", "myapp/internal/api/handlers.go", v)
	for _, want := range []string{"Architecture contract", "GET /api/items", "POST /api/items"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "NewHandler") || strings.Contains(got, "AddBookmark") {
		t.Fatalf("rig-specific anti-patterns should not appear:\n%s", got)
	}
}

func TestValidateImplementExportedSymbols_rejectsUndocumentedExport(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "rig", "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	spec := "## Store\n```go\nfunc List(ctx context.Context) ([]Item, error)\nfunc Create(ctx context.Context, name string) (Item, error)\n```\n"
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:      "myapp",
		QAVerifyCommand: "cd myapp && go test ./...",
	}
	body := "package store\n\nfunc InventedAPI() error { return nil }\n"
	err := ValidateImplementExportedSymbols(rigDir, "myapp/internal/store/store.go", body, v)
	if err == nil || !strings.Contains(err.Error(), "InventedAPI") {
		t.Fatalf("got %v", err)
	}
}

func TestValidateImplementExportedSymbols_allowsSpecNames(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "rig", "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	spec := "## Store\n```go\nfunc List(ctx context.Context) ([]Item, error)\n```\n"
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:      "myapp",
		QAVerifyCommand: "cd myapp && go test ./...",
	}
	body := "package store\n\nfunc List(ctx context.Context) ([]Item, error) { return nil, nil }\n"
	if err := ValidateImplementExportedSymbols(rigDir, "myapp/internal/store/store.go", body, v); err != nil {
		t.Fatalf("got %v", err)
	}
}

func TestFormatSpecStoreContractBlock_generic(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "rig", "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte("## Store\n```go\nfunc List() error\n```\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:      "myapp",
		QAVerifyCommand: "cd myapp && go test ./...",
	}
	got := FormatSpecStoreContractBlock(dir, "rig", "myapp/internal/store/store.go", v)
	if strings.Contains(got, "AddBookmark") {
		t.Fatalf("should not mention bookmark-specific names: %s", got)
	}
	if !strings.Contains(got, "List") {
		t.Fatalf("want SPEC content: %s", got)
	}
}
