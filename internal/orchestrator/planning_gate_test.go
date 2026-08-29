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
	setListImplementBeadsByStatusHook(t, townRoot, rig, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "open" {
			return []PlanBead{
				{ID: "te-main", Title: "Implement linkshelf/cmd/server/main.go per architecture"},
				{ID: "te-schema", Title: "Implement linkshelf/internal/store/schema.go per architecture"},
			}, nil
		}
		return nil, nil
	})

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

	next, err := m.CompleteTask(id, "success", "mockrig/qa", "looks good", "", nil)
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

// TestValidatePlanningPhaseGate_projectSetupSkipsBeadCoverage proves project_setup
// can succeed even when required_files have no open beads (e.g. a phase that was
// already implemented and its beads were closed, then the workflow returned to
// project_setup for recovery).
func TestValidatePlanningPhaseGate_projectSetupSkipsBeadCoverage(t *testing.T) {
	dir := t.TempDir()
	townRoot := filepath.Join(dir, "gt")
	rig := "mockrig"
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	spec := `# Spec
| GET | /api/links | 200 | — |
`
	arch := `# Architecture
- frontend/app/page.tsx
- frontend/components/Widget.tsx
`
	plan := `# Plan

## Bead map

### x1: frontend/app/page.tsx
- Scope: implement the main page component for the frontend application per architecture.md.
- Acceptance: file exists, non-empty, and matches the planned layout.

### x2: frontend/components/Widget.tsx
- Scope: implement the reusable Widget component per architecture.md.
- Acceptance: file exists, non-empty, and matches the planned layout.

Additional context so the plan document exceeds the minimum size threshold required by the planning gate validator.
`
	for name, body := range map[string]string{"SPEC.md": spec, "architecture.md": arch, "plan.md": plan} {
		if err := os.WriteFile(filepath.Join(rigDir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	v := WorkflowValidation{
		LayoutRoot:        ".",
		BeadTitleContains: "Implement mockrig/",
		MinPlanBytes:      50,
		MinArchitectureBytes: 50,
		RequiredFiles: []string{
			"frontend/app/page.tsx",
			"frontend/components/Widget.tsx",
		},
		DeliveryPhases: []DeliveryPhase{{
			ID:            "frontend-ui",
			Title:         "Frontend",
			RequiredFiles: []string{"frontend/app/page.tsx", "frontend/components/Widget.tsx"},
		}},
		ActivePhaseIDField: "frontend-ui",
	}
	setListImplementBeadsByStatusHook(t, townRoot, rig, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		return nil, nil
	})
	// No open beads — project_setup should still pass because bead coverage is a
	// planning concern, not a setup concern.
	if err := ValidatePlanningPhaseGate(townRoot, rig, "project_setup", v); err != nil {
		t.Fatalf("expected project_setup gate to pass without open beads, got %v", err)
	}
	// planning state must still enforce bead coverage.
	if err := ValidatePlanningPhaseGate(townRoot, rig, "planning", v); err == nil || (!strings.Contains(err.Error(), "missing open bead") && !strings.Contains(err.Error(), "no open beads matching")) {
		t.Fatalf("expected planning gate to require open beads, got %v", err)
	}
}
