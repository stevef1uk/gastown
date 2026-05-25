package orchestrator

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestArchitectureContractOwnedSymbolNames_ownsColumnOnly(t *testing.T) {
	t.Parallel()
	rigDir := t.TempDir()
	arch := `## Go package bead ownership
| Package | File | Owns (exported) | Depends on |
| --- | --- | --- | --- |
| datastore | myapp/internal/store/schema.go | ` + "`func InitSchema(db *sql.DB) error` `type Widget struct{}`" + ` | |
| datastore | myapp/internal/store/store.go | ` + "`func List(context.Context) ([]Widget, error)` `var DB *sql.DB`" + ` | ` + "`type Widget` `func InitSchema`" + ` |
`
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{LayoutRoot: "myapp", QAVerifyCommand: "cd myapp && go test ./..."}

	schemaOwned := ArchitectureContractOwnedSymbolNames(rigDir, "myapp/internal/store/schema.go", v)
	for _, want := range []string{"InitSchema", "Widget"} {
		if !slices.Contains(schemaOwned, want) {
			t.Fatalf("schema owns %q, got %v", want, schemaOwned)
		}
	}

	storeOwned := ArchitectureContractOwnedSymbolNames(rigDir, "myapp/internal/store/store.go", v)
	if slices.Contains(storeOwned, "InitSchema") {
		t.Fatalf("InitSchema is a dependency on store.go, not owned: %v", storeOwned)
	}
	for _, want := range []string{"List"} {
		if !slices.Contains(storeOwned, want) {
			t.Fatalf("store owns %q, got %v", want, storeOwned)
		}
	}
}

func TestValidateImplementCrossBeadContent_allowsInitSchemaWithOwnershipTableDeps(t *testing.T) {
	t.Parallel()
	rigDir := t.TempDir()
	arch := `| File | Owns (exported) | Depends on |
| --- | --- | --- |
| myapp/internal/store/schema.go | ` + "`func InitSchema(db *sql.DB) error` `type Item struct{}`" + ` | |
| myapp/internal/store/store.go | ` + "`func List() error`" + ` | ` + "`func InitSchema` `type Item`" + ` |
`
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:      "myapp",
		QAVerifyCommand: "cd myapp && go test ./...",
		RequiredFiles: []string{
			"myapp/internal/store/schema.go",
			"myapp/internal/store/store.go",
		},
	}
	body := "package store\n\nfunc InitSchema(db *sql.DB) error { return nil }\n"
	if err := ValidateImplementCrossBeadContent(rigDir, "myapp/internal/store/schema.go", body, v); err != nil {
		t.Fatalf("got %v", err)
	}
}

func TestOwnershipTableColumnRoles_threeColumnDesignTemplate(t *testing.T) {
	t.Parallel()
	cells := []string{"File", "Owns (exported)", "Must not define"}
	owns, dep := ownershipTableColumnRoles(cells)
	if owns != 1 || dep != 2 {
		t.Fatalf("owns=%d dep=%d", owns, dep)
	}
}

func TestLineMatchesOwnershipTableRow_storeStemDoesNotMatchSchemaPath(t *testing.T) {
	t.Parallel()
	cells := []string{"datastore", "myapp/internal/store/schema.go", "`type Widget struct{}`", ""}
	if lineMatchesOwnershipTableRow(
		"| datastore | myapp/internal/store/schema.go | `type widget` | |",
		cells,
		"myapp/internal/store/store.go",
	) {
		t.Fatal("store.go bead must not match schema.go ownership row")
	}
	if !lineMatchesOwnershipTableRow(
		"| datastore | myapp/internal/store/store.go | `func List()` | |",
		[]string{"datastore", "myapp/internal/store/store.go", "`func List()`"},
		"myapp/internal/store/store.go",
	) {
		t.Fatal("expected store.go row match")
	}
}

func TestOwnershipTableHeaderRow(t *testing.T) {
	t.Parallel()
	if !ownershipTableHeaderRow("| package | file | owns | depends |") {
		t.Fatal("expected header")
	}
	if ownershipTableHeaderRow("| store | store.go | `func List` | `func InitSchema` |") {
		t.Fatal("data row should not be header")
	}
}

func TestArchitectureContractOwnedSymbolNames_noTableFallsBackEmpty(t *testing.T) {
	t.Parallel()
	rigDir := t.TempDir()
	v := WorkflowValidation{LayoutRoot: "myapp"}
	got := ArchitectureContractOwnedSymbolNames(rigDir, "myapp/internal/store/store.go", v)
	if len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestSymbolsOwnedByLaterSiblings_ownsColumnNotDepends(t *testing.T) {
	t.Parallel()
	rigDir := t.TempDir()
	arch := strings.Join([]string{
		"| File | Owns (exported) | Depends on |",
		"| --- | --- | --- |",
		"| myapp/internal/store/schema.go | `type Item struct{}` | |",
		"| myapp/internal/store/store.go | `func List() error` | `func InitSchema` |",
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:      "myapp",
		QAVerifyCommand: "cd myapp && go test ./...",
		RequiredFiles: []string{
			"myapp/internal/store/schema.go",
			"myapp/internal/store/store.go",
		},
	}
	later := symbolsOwnedByLaterSiblings(rigDir, "myapp/internal/store/schema.go", v)
	if !slices.Contains(later.Funcs, "List") {
		t.Fatalf("want List in later funcs, got %v", later.Funcs)
	}
	if slices.Contains(later.Funcs, "InitSchema") {
		t.Fatalf("InitSchema is a dependency, not owned by store: %v", later.Funcs)
	}
}

func TestValidateImplementCrossBeadContent_rejectsListOnSchemaFromOwnershipTable(t *testing.T) {
	t.Parallel()
	rigDir := t.TempDir()
	arch := strings.Join([]string{
		"| File | Owns (exported) | Depends on |",
		"| --- | --- | --- |",
		"| myapp/internal/store/schema.go | `type Item struct{}` | |",
		"| myapp/internal/store/store.go | `func List() error` | |",
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:      "myapp",
		QAVerifyCommand: "cd myapp && go test ./...",
		RequiredFiles: []string{
			"myapp/internal/store/schema.go",
			"myapp/internal/store/store.go",
		},
	}
	body := "package store\n\nfunc List() error { return nil }\n"
	err := ValidateImplementCrossBeadContent(rigDir, "myapp/internal/store/schema.go", body, v)
	if err == nil || !strings.Contains(err.Error(), "List") {
		t.Fatalf("got %v", err)
	}
}
