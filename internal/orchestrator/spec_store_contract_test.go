package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractSpecMarkdownSection_store(t *testing.T) {
	doc := "# X\n\n## Data model\n\n```go\ntype Link struct {}\n```\n\n## Store (`internal/store/store.go`)\n\n```go\nfunc (s *Store) List(ctx context.Context) ([]Link, error)\n```\n\n## HTTP API\n\nfoo\n"
	got := ExtractSpecMarkdownSection(doc, "Store")
	if !strings.Contains(got, "List(ctx context.Context)") {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "HTTP API") {
		t.Fatal("section bled into next heading")
	}
}

func TestFormatSpecSchemaContractBlock_schemaBead(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	spec := "## Data model\n\n```sql\nCREATE TABLE IF NOT EXISTS links (id INTEGER PRIMARY KEY);\n```\n"
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	got := FormatSpecSchemaContractBlock(dir, rig, "linkshelf/internal/store/schema.go")
	for _, want := range []string{"InitSchema(db *sql.DB)", "DDL only", "links", "bookmarks"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "List(ctx context.Context)") {
		t.Fatal("schema bead must not get Store List/Create/Delete contract")
	}
}

func TestFormatSpecStoreContractBlock_storeBead(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	spec := "## Store\n\n```go\nfunc (s *Store) List(ctx context.Context) ([]Link, error)\n```\n"
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	got := FormatSpecStoreContractBlock(dir, rig, "linkshelf/internal/store/store.go")
	if !strings.Contains(got, "List(ctx context.Context)") {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(got, "AddBookmark") {
		t.Fatal("expected alignment warning against alternate APIs")
	}
}

func TestFormatStoreTestBeadChecklist(t *testing.T) {
	got := FormatStoreTestBeadChecklist("linkshelf/internal/store/store_test.go")
	if got == "" {
		t.Fatal("expected checklist")
	}
	for _, name := range []string{
		"TestStore_List_Empty",
		"TestStore_Delete_NonExistentID",
	} {
		if !strings.Contains(got, name) {
			t.Fatalf("missing %s in %q", name, got)
		}
	}
}

func TestFormatStoreTestBeadChecklist_nonTestPath(t *testing.T) {
	if got := FormatStoreTestBeadChecklist("linkshelf/internal/store/store.go"); got != "" {
		t.Fatalf("unexpected checklist: %q", got)
	}
}

func TestLoadSpecStoreContractFromRig_missingFile(t *testing.T) {
	if got := LoadSpecStoreContractFromRig(t.TempDir(), "rig"); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
}

func TestPreferIncrementalEdit_falseWhenGoSyntaxBroken(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	layout := filepath.Join(dir, rig, "mayor", "rig", "linkshelf", "internal", "store")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	path := "linkshelf/internal/store/store.go"
	broken := "package store\n\nfunc x() {}\n}n err\n"
	if err := os.WriteFile(filepath.Join(dir, rig, "mayor", "rig", path), []byte(broken), 0644); err != nil {
		t.Fatal(err)
	}
	v := DefaultWorkflowValidation()
	v.LayoutRoot = "linkshelf"
	if PreferIncrementalEdit(dir, rig, path, v) {
		t.Fatal("expected full rewrite when file does not parse")
	}
}
