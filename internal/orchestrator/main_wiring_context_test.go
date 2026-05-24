package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsCodeindexTestSymbol(t *testing.T) {
	t.Parallel()
	if !isCodeindexTestSymbol("TestFoo", "handlers_test.go") {
		t.Fatal("want test by file")
	}
	if !isCodeindexTestSymbol("TestInitSchema", "schema_test.go") {
		t.Fatal("want test by name")
	}
	if isCodeindexTestSymbol("List", "store.go") {
		t.Fatal("List is production")
	}
	if isCodeindexTestSymbol("Testing", "x.go") {
		t.Fatal("Testing is not TestX")
	}
}

func TestPartitionCodeindexSymbolLines(t *testing.T) {
	t.Parallel()
	prod, test := partitionCodeindexSymbolLines("List (function)\n[test only] TestFoo (function)")
	if !strings.Contains(prod, "List") || strings.Contains(prod, "TestFoo") {
		t.Fatalf("prod=%q", prod)
	}
	if !strings.Contains(test, "TestFoo") {
		t.Fatalf("test=%q", test)
	}
}

func TestFormatCodeindexSymbolsSection_splitsTestSymbols(t *testing.T) {
	t.Parallel()
	got := formatCodeindexSymbolsSection("dependency internal/api", "internal/api",
		"handleListLinks (function)\n[test only] TestHandleCreateLinkBadJSON (function)", 800)
	for _, want := range []string{
		"[test only]",
		"Test-only symbols",
		"Production symbols",
		"do not** call them from `main.go`",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestOrderedDependencyGoFilesForContext_mainPrioritizesHandlers(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot: "linkshelf",
		RequiredFiles: []string{
			"linkshelf/go.mod",
			"linkshelf/internal/store/schema.go",
			"linkshelf/internal/store/store.go",
			"linkshelf/internal/store/store_test.go",
			"linkshelf/internal/api/handlers.go",
			"linkshelf/internal/api/handlers_test.go",
			"linkshelf/cmd/server/main.go",
			"linkshelf/cmd/server/main_test.go",
		},
	}
	got := orderedDependencyGoFilesForContext("linkshelf/cmd/server/main.go", v)
	if len(got) < 3 {
		t.Fatalf("got %v", got)
	}
	if !strings.HasSuffix(got[0], "handlers.go") {
		t.Fatalf("want handlers first, got %v", got)
	}
	for _, p := range got {
		if strings.HasSuffix(p, "store_test.go") || strings.HasSuffix(p, "handlers_test.go") {
			t.Fatalf("should skip non-main test deps, got %v", got)
		}
	}
}

func TestFormatMainWiringContextForBead_registerHandlersMode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "rig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	handlers := filepath.Join(rigDir, "linkshelf", "internal", "api", "handlers.go")
	if err := os.MkdirAll(filepath.Dir(handlers), 0755); err != nil {
		t.Fatal(err)
	}
	body := "package api\n\nfunc registerHandlers(mux *http.ServeMux) {}\n"
	if err := os.WriteFile(handlers, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go test ./...",
		RequiredFiles: []string{
			"linkshelf/internal/api/handlers.go",
			"linkshelf/cmd/server/main.go",
			"linkshelf/cmd/server/main_test.go",
		},
	}
	got := FormatMainWiringContextForBead(dir, rig, "linkshelf/cmd/server/main.go", v)
	for _, want := range []string{
		"Main wiring",
		"registerHandlers",
		"Dependency exports",
		"do not invent symbols",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestFormatMainWiringContextForBead_factoryHandlerMode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "rig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	apiDir := filepath.Join(rigDir, "linkshelf", "internal", "api")
	storeDir := filepath.Join(rigDir, "linkshelf", "internal", "store")
	if err := os.MkdirAll(apiDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	handlers := `package api

import "net/http"

func List(s interface{}) http.HandlerFunc { return nil }
func Create(s interface{}) http.HandlerFunc { return nil }
func Delete(s interface{}) http.HandlerFunc { return nil }
`
	storeGo := `package store

import "database/sql"

type Store struct{ db *sql.DB }
func NewStore(db *sql.DB) *Store { return &Store{db: db} }
`
	if err := os.WriteFile(filepath.Join(apiDir, "handlers.go"), []byte(handlers), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "store.go"), []byte(storeGo), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go test ./...",
		RequiredFiles: []string{
			"linkshelf/internal/store/store.go",
			"linkshelf/internal/api/handlers.go",
			"linkshelf/cmd/server/main.go",
		},
	}
	got := FormatMainWiringContextForBead(dir, rig, "linkshelf/cmd/server/main.go", v)
	for _, want := range []string{
		"handler factory funcs",
		"exported instance type",
		"SPEC HTTP paths",
		"`List(",
		"linkshelf/internal/store/store.go",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}
