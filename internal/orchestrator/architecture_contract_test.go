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

func TestArchitectureContractSymbolNames_receiverMethodsAndInlineArch(t *testing.T) {
	rigDir := t.TempDir()
	spec := "## Store (`myapp/internal/store/store.go`)\n```go\nfunc (s *Store) List(ctx context.Context) ([]Link, error)\nfunc (s *Store) Create(ctx context.Context, title, url string) (Link, error)\nfunc (s *Store) Delete(ctx context.Context, id int64) error\n```\n"
	arch := "| `store.go` | `type Store struct { db *sql.DB }` `func NewStore(db *sql.DB) *Store` | notes |\n"
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{LayoutRoot: "myapp", QAVerifyCommand: "cd myapp && go test ./..."}
	got := ArchitectureContractSymbolNames(rigDir, "myapp/internal/store/store.go", v)
	want := map[string]bool{"Store": true, "NewStore": true, "List": true, "Create": true, "Delete": true}
	for name := range want {
		found := false
		for _, g := range got {
			if g == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing %q in %v", name, got)
		}
	}
}

func TestValidateImplementExportedSymbols_allowsStoreAndNewStoreFromReceiverSpec(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "rig", "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	spec := "## Store\n```go\nfunc (s *Store) List(ctx context.Context) ([]Link, error)\nfunc NewStore(db *sql.DB) *Store\n```\n"
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{LayoutRoot: "myapp", QAVerifyCommand: "cd myapp && go test ./..."}
	body := "package store\n\ntype Store struct{ db *sql.DB }\nfunc NewStore(db *sql.DB) *Store { return &Store{db: db} }\n"
	if err := ValidateImplementExportedSymbols(rigDir, "myapp/internal/store/store.go", body, v); err != nil {
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

func TestExtractContractSymbolsFromDocs_plainLayoutFencesIgnored(t *testing.T) {
	t.Parallel()
	spec := "# Goal\n\n```bash\ncd app && go test ./...\n```\n\n## Layout\n\n```\napp/store.go\n```\n\n## Store API\n\n```go\nfunc List(ctx context.Context) ([]Link, error)\nfunc Create(ctx context.Context, title, url string) (Link, error)\nfunc Delete(ctx context.Context, id int64) error\n```\n"
	got := extractContractSymbolsFromDocs(spec, "", "", "linkshelf/internal/store/store.go", "linkshelf")
	for _, sym := range []string{"List", "Create", "Delete"} {
		found := false
		for _, g := range got {
			if g == sym {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing %q in %v", sym, got)
		}
	}
}

func TestValidateImplementExportedSymbols_skipsWhenOnDiskCorrupt(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "rig", "mayor", "rig")
	rel := "linkshelf/internal/store/store.go"
	abs := filepath.Join(rigDir, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("`---END WRITE---`\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte("## Store\n```go\nfunc List() error\n```\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{LayoutRoot: "linkshelf", QAVerifyCommand: "cd linkshelf && go test ./..."}
	body := "package store\n\nfunc List() {}\nfunc Create() {}\nfunc Delete() {}\n"
	if err := validateImplementExportedSymbols(rigDir, rel, body, v, true); err != nil {
		t.Fatalf("corrupt on-disk file should allow recovery WRITE: %v", err)
	}
}
