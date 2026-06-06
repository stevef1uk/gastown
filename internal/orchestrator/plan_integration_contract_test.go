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
| GET | /static/{file} | 200 | — |
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
	for _, path := range []string{"/api/links", "/api/links/{id}", "/static/{file}", "/"} {
		if !strings.Contains(out, path) {
			t.Fatalf("contract missing route %q:\n%s", path, out)
		}
	}
	if strings.Contains(out, "| GET | `/static` |") || strings.Contains(out, "| DELETE | `/api/links` |") {
		t.Fatalf("contract must not strip path params:\n%s", out)
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

func TestIntegrationContractScopeNote_phasedWithoutServer(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		DeliveryPhases: []DeliveryPhase{
			{ID: "api-handlers", RequiredFiles: []string{"linkshelf/internal/api/handlers.go"}},
			{ID: "server-setup", RequiredFiles: []string{"linkshelf/cmd/server/main.go"}},
		},
		ActivePhaseIDField: "api-handlers",
	}
	note := v.IntegrationContractScopeNote()
	if !strings.Contains(note, "not required") {
		t.Fatalf("expected not required note, got %q", note)
	}
	if strings.Contains(note, "must have") {
		t.Fatalf("should not require contract in api-handlers phase: %q", note)
	}
}

func TestIntegrationContractScopeNote_serverPhase(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		DeliveryPhases: []DeliveryPhase{
			{ID: "server-setup", RequiredFiles: []string{"linkshelf/cmd/server/main.go"}},
		},
		ActivePhaseIDField: "server-setup",
	}
	note := v.IntegrationContractScopeNote()
	if !strings.Contains(note, "required") || !strings.Contains(note, "linkshelf/cmd/server/main.go") {
		t.Fatalf("expected required note with main path, got %q", note)
	}
}
