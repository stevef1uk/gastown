package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnrichWorkflowValidationFromArchitecture_preservesNestedProfileWhenSpecFlat(t *testing.T) {
	dir := t.TempDir()
	spec := `# Link Shelf

## Layout (implement beads only)

` + "```" + `
linkshelf/
├── go.mod
├── cmd/server/main.go
├── internal/store/schema.go
├── internal/store/store.go
├── internal/api/handlers.go
└── web/
    ├── index.html
    ├── app.js
    └── style.css
` + "```" + `

## Module
` + "```" + `
module linkshelf
` + "```" + `
`
	// Architecture with optional test files that must not flatten required_files.
	arch := `# Architecture
| ` + "`linkshelf/internal/store`" + ` | ` + "`store_test.go`" + ` (optional) |
| ` + "`linkshelf/internal/api`" + ` | ` + "`handlers_test.go`" + ` (optional) |
`
	if err := os.WriteFile(filepath.Join(dir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	// Profile already has canonical nested paths (from spec-index LLM).
	v := WorkflowValidation{
		LayoutRoot:        "linkshelf",
		BeadTitleContains: "Implement linkshelf/",
		RequiredFiles: []string{
			"linkshelf/go.mod",
			"linkshelf/cmd/server/main.go",
			"linkshelf/internal/store/schema.go",
			"linkshelf/internal/store/store.go",
			"linkshelf/internal/api/handlers.go",
			"linkshelf/web/index.html",
			"linkshelf/web/app.js",
			"linkshelf/web/style.css",
		},
	}
	got := EnrichWorkflowValidationFromArchitecture(v, dir)
	for _, flat := range []string{
		"linkshelf/handlers.go",
		"linkshelf/main.go",
		"linkshelf/store.go",
		"linkshelf/schema.go",
		"linkshelf/store_test.go",
	} {
		for _, rf := range got.RequiredFiles {
			if rf == flat {
				t.Fatalf("enrich must not replace nested profile with flat path %q; got %v", flat, got.RequiredFiles)
			}
		}
	}
	if !RequiresExactImplementPaths(got) {
		t.Fatalf("expected exact paths after enrich, got %v", got.RequiredFiles)
	}
	if got.RequiredFiles[0] != "linkshelf/go.mod" {
		t.Fatalf("expected profile nested paths preserved, got %v", got.RequiredFiles)
	}
}

func TestExtractSpecLayoutPaths_linkshelfNestedStillFlattens(t *testing.T) {
	dir := "/Users/stevef/gt/testgt3/mayor/rig"
	if _, err := os.Stat(dir); err != nil {
		t.Skip("testgt3 not available")
	}
	paths, ok, _ := extractSpecLayoutPaths(dir)
	if !ok {
		t.Fatal("expected spec paths")
	}
	// Known limitation: tree parser captures leaf filenames only; enrich must not apply these over profile.
	for _, flat := range []string{"linkshelf/handlers.go", "linkshelf/main.go"} {
		found := false
		for _, p := range paths {
			if p == flat {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected flat extract path %q in %v", flat, paths)
		}
	}
}

func TestEnrichWorkflowValidationFromArchitecture_testgt3Profile(t *testing.T) {
	dir := "/Users/stevef/gt/testgt3/mayor/rig"
	profPath := filepath.Join(dir, ".gastown/workflow-profile.json")
	if _, err := os.Stat(profPath); err != nil {
		t.Skip("testgt3 not available")
	}
	data, err := os.ReadFile(profPath)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Validation WorkflowValidation `json:"validation"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	before := append([]string(nil), env.Validation.RequiredFiles...)
	got := EnrichWorkflowValidationFromArchitecture(env.Validation, dir)
	t.Logf("before: %v", before)
	t.Logf("after:  %v", got.RequiredFiles)
	t.Logf("exact:  %v", RequiresExactImplementPaths(got))
	for _, flat := range []string{"linkshelf/handlers.go", "linkshelf/main.go", "linkshelf/store_test.go"} {
		for _, rf := range got.RequiredFiles {
			if rf == flat {
				t.Fatalf("enrich produced flat path %q", flat)
			}
		}
	}
	if strings.Join(before, ",") != strings.Join(got.RequiredFiles, ",") {
		t.Fatalf("profile required_files changed:\n  before: %v\n  after:  %v", before, got.RequiredFiles)
	}
}
