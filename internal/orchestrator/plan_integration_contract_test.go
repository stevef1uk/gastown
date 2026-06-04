package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderPlanIntegrationContract_includesAPIRoutes(t *testing.T) {
	dir := t.TempDir()
	spec := `# Spec
| GET | /api/links | 200 | — |
| POST | /api/links | 201 | — |
| DELETE | /api/links/{id} | 204 | — |
| GET | / | index | — |
`
	arch := `# Architecture
## Per-file ownership
| File | Owns |
| ` + "`linkshelf/internal/api/handlers.go`" + ` | handlers |
`
	for name, body := range map[string]string{"SPEC.md": spec, "architecture.md": arch} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	v := WorkflowValidation{
		LayoutRoot: "linkshelf",
		RequiredFiles: []string{
			"linkshelf/cmd/server/main.go",
			"linkshelf/internal/api/handlers.go",
		},
	}
	out := renderPlanIntegrationContract(dir, v)
	if out == "" {
		t.Fatal("expected contract block")
	}
	for _, path := range []string{"/api/links", "/"} {
		if !strings.Contains(out, path) {
			t.Fatalf("contract missing route %q:\n%s", path, out)
		}
	}
}

func TestEnsurePlanIntegrationContract_patchesMissingSection(t *testing.T) {
	dir := t.TempDir()
	spec := `| GET | /api/links | 200 | — |`
	if err := os.WriteFile(filepath.Join(dir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	plan := "# Implementation plan\n\n## Bead map\n\n### te-1: linkshelf/go.mod\n"
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte(plan), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		RequiredFiles: []string{"linkshelf/cmd/server/main.go"},
	}
	patched, err := ensurePlanIntegrationContract(dir, v)
	if err != nil {
		t.Fatal(err)
	}
	if !patched {
		t.Fatal("expected patch")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "plan.md"))
	if !strings.Contains(string(data), "## Integration contract") {
		t.Fatalf("plan.md missing section:\n%s", data)
	}
	if !strings.Contains(string(data), "/api/links") {
		t.Fatal("plan.md contract missing route")
	}
}
