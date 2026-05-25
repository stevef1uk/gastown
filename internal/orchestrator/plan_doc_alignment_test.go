package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateArchitectureDocAlignment_catchesArchDrift(t *testing.T) {
	dir := t.TempDir()
	spec := `# Linkshelf MVP
| GET | / | 200, serve web/index.html | — |
| GET | /static/{file} | 200, file under web/ | 404 |
| GET | /api/links | 200, JSON array | — |

## Store
` + "```go\nfunc InitSchema(db *sql.DB) error\nfunc List(ctx context.Context) ([]Link, error)\nfunc Create(ctx context.Context, title, url string) (Link, error)\nfunc Delete(ctx context.Context, id int64) error\n```" + `

module linkshelf
`
	arch := `# Architecture
| GET | /web/* | static |
Handlers: GetLinks, DeleteLink. type Store struct. InitDB(db).
`
	if err := os.WriteFile(filepath.Join(dir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{LayoutRoot: "linkshelf"}
	err := ValidateArchitectureDocAlignment(dir, v)
	if err == nil {
		t.Fatal("expected architecture alignment error")
	}
	msg := err.Error()
	for _, want := range []string{"/web", "GetLinks", "Store struct", "InitDB"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
}

func TestValidateArchitectureDocAlignment_passesAlignedArch(t *testing.T) {
	dir := t.TempDir()
	spec := `# Spec
| GET | /api/links | 200, JSON array | — |
## Store
` + "```go\nfunc List() ([]Link, error)\nfunc Create(title, url string) (Link, error)\n```" + `
module linkshelf
`
	arch := `Routes GET/POST /api/links. Package store: List, Create, InitSchema.`
	for name, body := range map[string]string{"SPEC.md": spec, "architecture.md": arch} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := ValidateArchitectureDocAlignment(dir, WorkflowValidation{LayoutRoot: "linkshelf"}); err != nil {
		t.Fatal(err)
	}
}

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
