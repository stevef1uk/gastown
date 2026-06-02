package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestRigProfile(t *testing.T, townRoot, rig string, v WorkflowValidation) {
	t.Helper()
	profDir := filepath.Join(townRoot, rig, "mayor", "rig", rigProfileDir)
	if err := os.MkdirAll(profDir, 0755); err != nil {
		t.Fatal(err)
	}
	env := rigProfileEnvelope{Version: 1, Source: "test", Validation: v}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profDir, rigProfileFile), raw, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestCompleteTask_planReviewSuccessBlockedOnMisalignedPlan(t *testing.T) {
	dir := t.TempDir()
	townRoot := filepath.Join(dir, "gt")
	rig := "mockrig"
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	spec := `# Spec
| GET | /api/links | 200 | — |
module linkshelf
`
	arch := `# Architecture
| GET | /api/links | list |
Store: List, Create, Delete, InitSchema.
- linkshelf/cmd/server/main.go
- linkshelf/internal/store/schema.go
`
	plan := `# Plan
## Integration contract
GET /api/links only.

## Bead map
### te-1: linkshelf/main.go
- Scope: main
`
	for name, body := range map[string]string{"SPEC.md": spec, "architecture.md": arch, "plan.md": plan} {
		if err := os.WriteFile(filepath.Join(rigDir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	prof := WorkflowValidation{
		LayoutRoot:        "linkshelf",
		BeadTitleContains: "Implement linkshelf/",
		MinPlanBytes:      100,
		RequiredFiles: []string{
			"linkshelf/cmd/server/main.go",
			"linkshelf/internal/store/schema.go",
		},
	}
	writeTestRigProfile(t, townRoot, rig, prof)

	m := NewManager(townRoot)
	m.LoadTemplate(&WorkflowTemplate{
		ID:           "rig-flow",
		InitialState: "plan_review",
		States: map[string]State{
			"plan_review": {
				Role: "qa",
				Transitions: map[string]Transition{
					"success": {To: "project_setup"},
				},
			},
			"project_setup": {Role: "setup", Transitions: map[string]Transition{"success": {To: "implementation"}}},
			"implementation": {Role: "polecat"},
		},
	})
	id, err := m.StartWorkflow("rig-flow", map[string]string{"rig": rig})
	if err != nil {
		t.Fatal(err)
	}
	m.instances[id].CurrentState = "plan_review"

	next, err := m.CompleteTask(id, "success", "mockrig/qa", "looks good", "")
	if err == nil {
		t.Fatalf("expected gate error, got next=%q", next)
	}
	if !strings.Contains(err.Error(), "plan_review success blocked") {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.instances[id].CurrentState != "plan_review" {
		t.Fatalf("state should not advance, got %q", m.instances[id].CurrentState)
	}
}

func TestValidatePlanningPhaseGate_blocksFlattenedPlanPaths(t *testing.T) {
	dir := t.TempDir()
	townRoot := filepath.Join(dir, "gt")
	rig := "mockrig"
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	spec := `| GET | /api/links | 200 | — |
module linkshelf`
	arch := `GET /api/links. linkshelf/cmd/server/main.go`
	plan := `# Plan
## Integration contract
GET /api/links

## Bead map
### te-1: linkshelf/main.go
- Scope: main entrypoint for the server — must use linkshelf/cmd/server/main.go per required_files
- Acceptance: wires routes and listens on :8080
- Architecture: see architecture.md
`
	for name, body := range map[string]string{"SPEC.md": spec, "architecture.md": arch, "plan.md": plan} {
		if err := os.WriteFile(filepath.Join(rigDir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	v := WorkflowValidation{
		LayoutRoot:            "linkshelf",
		MinPlanBytes:          50,
		MinArchitectureBytes:  100,
		RequiredFiles:         []string{"linkshelf/cmd/server/main.go"},
	}
	err := ValidatePlanningPhaseGate(townRoot, rig, "plan_review", v)
	if err == nil || !strings.Contains(err.Error(), "linkshelf/main.go") {
		t.Fatalf("expected flattened path error, got %v", err)
	}
}
