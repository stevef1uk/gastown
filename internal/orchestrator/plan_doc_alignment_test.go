package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidatePlanningDocAlignment_catchesPlanHTTPAndStoreDrift(t *testing.T) {
	dir := t.TempDir()
	spec := `# Linkshelf MVP
## HTTP
| GET | / | 200, serve web/index.html | — |
| GET | /api/links | 200, JSON array | — |
| POST | /api/links | 201 | — |

## Store
Package-level API (no Store struct):
` + "```go\nvar DB *sql.DB\nfunc InitSchema(db *sql.DB) error\nfunc List() ([]Link, error)\nfunc Create(title, url string) (Link, error)\nfunc Delete(id int64) error\n```" + `

module linkshelf
`
	arch := `# Architecture
| GET | /api/links | list |
| POST | /api/links | create |
Store: List, Create, Delete, InitSchema on package DB.
`
	plan := `# Plan
module github.com/example/linkshelf

Handlers register GET /links and POST /links.
Store uses ListLinks and CreateLink.
httptest is mandatory for every bead.
`
	for name, body := range map[string]string{"SPEC.md": spec, "architecture.md": arch, "plan.md": plan} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	v := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		RequiredFiles: []string{"linkshelf/cmd/server/main.go", "linkshelf/internal/store/store.go"},
	}
	err := ValidatePlanningDocAlignment(dir, v)
	if err == nil {
		t.Fatal("expected alignment error")
	}
	msg := err.Error()
	for _, want := range []string{"/links", "ListLinks", "github.com/example", "integration contract", "httptest"} {
		if !strings.Contains(strings.ToLower(msg), strings.ToLower(want)) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
}

func TestValidatePlanningDocAlignment_allowsQualifiedStoreRefs(t *testing.T) {
	dir := t.TempDir()
	spec := `# Spec
## Store
` + "```go\nfunc InitSchema(db *sql.DB) error\nfunc List() ([]Link, error)\n```" + `
| GET | /api/links | JSON array | — |
`
	arch := `# Architecture
Call schema.InitSchema from main, then store.List and store.Create.
`
	plan := `# Plan
## Integration contract
main calls InitSchema; handlers use store.List.
## Bead map
### te-1: linkshelf/cmd/server/main.go
- Acceptance: wire store.List
`
	for name, body := range map[string]string{"SPEC.md": spec, "architecture.md": arch, "plan.md": plan} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	v := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		RequiredFiles: []string{"linkshelf/cmd/server/main.go"},
	}
	if err := ValidatePlanningDocAlignment(dir, v); err != nil {
		t.Fatal(err)
	}
}

func TestValidatePlanningDocAlignment_passesAlignedDocs(t *testing.T) {
	dir := t.TempDir()
	spec := `# Spec
| GET | /api/links | 200, JSON array | — |
| POST | /api/links | 201 | — |
## Store
` + "```go\nfunc List() ([]Link, error)\nfunc Create(title, url string) (Link, error)\n```" + `
module linkshelf
`
	arch := `Routes: GET/POST /api/links. Store: List, Create.`
	plan := `# Plan
## Integration contract
Entrypoint imports store; registers GET/POST /api/links; exports List, Create.

## Bead map
### fi-1: linkshelf/cmd/server/main.go
- Acceptance: wire List, Create from store package
`
	for name, body := range map[string]string{"SPEC.md": spec, "architecture.md": arch, "plan.md": plan} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	v := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		RequiredFiles: []string{"linkshelf/cmd/server/main.go"},
	}
	if err := ValidatePlanningDocAlignment(dir, v); err != nil {
		t.Fatal(err)
	}
}
