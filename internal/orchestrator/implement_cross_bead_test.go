package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func linkshelfStoreValidation() WorkflowValidation {
	return WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go test ./...",
		RequiredFiles: []string{
			"linkshelf/internal/store/schema.go",
			"linkshelf/internal/store/store.go",
		},
	}
}

func writeStorePackageFixture(t *testing.T, rigDir string, schemaBody, storeBody string) {
	t.Helper()
	schemaPath := filepath.Join(rigDir, "linkshelf", "internal", "store", "schema.go")
	storePath := filepath.Join(rigDir, "linkshelf", "internal", "store", "store.go")
	if err := os.MkdirAll(filepath.Dir(schemaPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(schemaPath, []byte(schemaBody), 0644); err != nil {
		t.Fatal(err)
	}
	if storeBody != "" {
		if err := os.WriteFile(storePath, []byte(storeBody), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestValidateImplementCrossBeadContent_rejectsInitSchemaInStore(t *testing.T) {
	t.Parallel()
	v := linkshelfStoreValidation()
	rigDir := t.TempDir()
	writeStorePackageFixture(t, rigDir,
		"package store\n\ntype Link struct{}\n\nfunc InitSchema(db *sql.DB) error { return nil }\n",
		"",
	)
	body := "package store\n\nfunc InitSchema(db *sql.DB) error { return nil }\n"
	err := ValidateImplementCrossBeadContent(rigDir, "linkshelf/internal/store/store.go", body, v)
	if err == nil || !strings.Contains(err.Error(), "InitSchema") {
		t.Fatalf("got %v", err)
	}
}

func TestValidateImplementCrossBeadContent_allowsInitSchemaOnSchemaBead(t *testing.T) {
	t.Parallel()
	v := linkshelfStoreValidation()
	rigDir := t.TempDir()
	writeStorePackageFixture(t, rigDir, "", "")
	body := "package store\n\nfunc InitSchema(db *sql.DB) error { return nil }\n"
	if err := ValidateImplementCrossBeadContent(rigDir, "linkshelf/internal/store/schema.go", body, v); err != nil {
		t.Fatalf("got %v", err)
	}
}

// Regression: store.go Depends column lists InitSchema; schema bead must still own InitSchema (not blocked as "later" API).
func TestValidateImplementCrossBeadContent_allowsInitSchemaWhenStoreDependsColumnMentionsIt(t *testing.T) {
	t.Parallel()
	rigDir := t.TempDir()
	arch := `| Package | File | Owns (exported) | Depends on |
| --- | --- | --- | --- |
| datastore | app/internal/store/schema.go | ` + "`func InitSchema(db *sql.DB) error` `type Record struct{}`" + ` | |
| datastore | app/internal/store/store.go | ` + "`func List(context.Context) ([]Record, error)`" + ` | ` + "`type Record` `func InitSchema`" + ` |
`
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:      "app",
		QAVerifyCommand: "cd app && go test ./...",
		RequiredFiles: []string{
			"app/internal/store/schema.go",
			"app/internal/store/store.go",
		},
	}
	body := "package store\n\nimport \"database/sql\"\n\nfunc InitSchema(db *sql.DB) error { return nil }\n"
	if err := ValidateImplementCrossBeadContent(rigDir, "app/internal/store/schema.go", body, v); err != nil {
		t.Fatalf("got %v", err)
	}
}

func TestValidateImplementCrossBeadContent_rejectsStoreMethodsOnSchema(t *testing.T) {
	t.Parallel()
	v := linkshelfStoreValidation()
	rigDir := t.TempDir()
	writeStorePackageFixture(t, rigDir, "package store\n", "package store\n\nfunc NewStore(db *sql.DB) *Store { return nil }\n\ntype Store struct{}\n")
	body := "package store\n\nfunc NewStore(path string) (*Store, error) { return nil, nil }\n"
	err := ValidateImplementCrossBeadContent(rigDir, "linkshelf/internal/store/schema.go", body, v)
	if err == nil || !strings.Contains(err.Error(), "NewStore") {
		t.Fatalf("got %v", err)
	}
}

func TestPrepareImplementPackageWrite_stripsRedeclaredType(t *testing.T) {
	t.Parallel()
	v := linkshelfStoreValidation()
	rigDir := t.TempDir()
	writeStorePackageFixture(t, rigDir,
		"package store\n\ntype Link struct { ID int64 }\n\nfunc InitSchema(db *sql.DB) error { return nil }\n",
		"",
	)
	body := `package store

type Link struct {
	ID int64
}

type Store struct { db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }
`
	out, ok := PrepareImplementPackageWrite(rigDir, "linkshelf/internal/store/store.go", body, v)
	if !ok {
		t.Fatal("expected strip")
	}
	if strings.Contains(out, "type Link struct") {
		t.Fatalf("Link type should be stripped:\n%s", out)
	}
	if !strings.Contains(out, "type Store struct") {
		t.Fatalf("Store should remain:\n%s", out)
	}
	err := ValidateImplementCrossBeadContent(rigDir, "linkshelf/internal/store/store.go", out, v)
	if err != nil {
		t.Fatalf("after strip: %v", err)
	}
}

func TestValidateImplementWrittenContent_crossBead(t *testing.T) {
	t.Parallel()
	v := linkshelfStoreValidation()
	rigDir := t.TempDir()
	writeStorePackageFixture(t, rigDir,
		"package store\n\nfunc InitSchema() error { return nil }\n",
		"",
	)
	err := ValidateImplementWrittenContent(rigDir, "linkshelf/internal/store/store.go", "func InitSchema() {}\n", v)
	if err == nil {
		t.Fatal("expected error")
	}
}
