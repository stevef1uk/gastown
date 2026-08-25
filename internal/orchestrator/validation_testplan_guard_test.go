package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeTestPlanWithHeadings(t *testing.T, town, rig string, headings ...string) {
	t.Helper()
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := ""
	for _, h := range headings {
		body += "### " + h + "\nTest file: linkshelf/internal/store/store_test.go\n\n"
	}
	if err := os.WriteFile(filepath.Join(rigDir, "TEST_PLAN.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMismatchedTestPlanPhaseIDs(t *testing.T) {
	town := t.TempDir()
	v := WorkflowValidation{DeliveryPhases: []DeliveryPhase{
		{ID: "store-and-api"}, {ID: "cmd-server"},
	}}

	writeTestPlanWithHeadings(t, town, "fin", "store-and-api", "backend-core", "CMD-SERVER")
	bad := MismatchedTestPlanPhaseIDs(town, "fin", v)
	if len(bad) != 1 || bad[0] != "backend-core" {
		t.Fatalf("mismatches = %v, want [backend-core] (case-insensitive match must pass)", bad)
	}

	// No plan file at all → no mismatches reported (nothing to validate).
	town2 := t.TempDir()
	if bad := MismatchedTestPlanPhaseIDs(town2, "fin", v); bad != nil {
		t.Fatalf("missing TEST_PLAN.md should report nothing, got %v", bad)
	}
}

func TestWriteRigWorkflowProfile_unfreezesStaleTestPlanOnPhaseChange(t *testing.T) {
	town := t.TempDir()
	rig := "fin"
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestPlanWithHeadings(t, town, rig, "old-phase")

	oldV := WorkflowValidation{
		LayoutRoot:         "linkshelf",
		RequiredFiles:      []string{"linkshelf/go.mod"},
		DeliveryPhases:     []DeliveryPhase{{ID: "old-phase", RequiredFiles: []string{"linkshelf/go.mod"}}},
		ActivePhaseIDField: "old-phase",
	}
	if err := WriteRigWorkflowProfile(town, rig, oldV, "deterministic", "high"); err != nil {
		t.Fatal(err)
	}
	// Simulate the flags a completed run leaves behind.
	envPath := filepath.Join(rigDir, ".gastown", "workflow-profile.json")
	raw, _ := os.ReadFile(envPath)

	newV := WorkflowValidation{
		LayoutRoot:     "linkshelf",
		RequiredFiles:  []string{"linkshelf/go.mod"},
		DeliveryPhases: []DeliveryPhase{{ID: "new-phase-a", RequiredFiles: []string{"linkshelf/go.mod"}}},
	}
	// Mark the on-disk profile frozen/reviewed the way a real run does, then
	// regenerate with renamed phases.
	var env map[string]interface{}
	if json.Unmarshal(raw, &env) == nil {
		env["test_plan_frozen"] = true
		env["test_plan_reviewed"] = true
		if b, err := json.Marshal(env); err == nil {
			os.WriteFile(envPath, b, 0o644)
		}
	}
	if err := WriteRigWorkflowProfile(town, rig, newV, "llm", "high"); err != nil {
		t.Fatal(err)
	}

	fresh, ok, err := LoadRigWorkflowProfileFile(town, rig)
	if err != nil || !ok {
		t.Fatalf("reload failed: %v ok=%v", err, ok)
	}
	if fresh.ActivePhaseIDField != "new-phase-a" {
		t.Fatalf("active phase = %q", fresh.ActivePhaseIDField)
	}

	// The stale heading no longer matches — the saved envelope must have been
	// unfrozen so the Tester rewrites the plan against new-phase-a.
	raw2, _ := os.ReadFile(envPath)
	var env2 struct {
		TestPlanFrozen   bool `json:"test_plan_frozen"`
		TestPlanReviewed bool `json:"test_plan_reviewed"`
	}
	if json.Unmarshal(raw2, &env2) != nil {
		t.Fatal("decode")
	}
	if env2.TestPlanFrozen || env2.TestPlanReviewed {
		t.Fatalf("stale plan must be unfrozen on phase-id change: %+v", env2)
	}
}
