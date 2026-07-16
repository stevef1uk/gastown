package orchestrator

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanReviewFailureNeedsArchitect(t *testing.T) {
	t.Parallel()
	explicit := "architecture.md Store API signatures drift from SPEC: Create takes (db *sql.DB)"
	if !PlanReviewFailureNeedsArchitect(explicit) {
		t.Fatal("expected architect escalation for explicit drift")
	}
	boilerplate := "SPEC.md, architecture.md, and plan.md must agree on HTTP routes, store API names, module path, and integration contract; missing bead for handlers.go"
	if PlanReviewFailureNeedsArchitect(boilerplate) {
		t.Fatal("generic checklist + missing bead must stay with planner, not design")
	}
	if PlanReviewFailureNeedsArchitect("duplicate te-abc beads for main.go") {
		t.Fatal("bead-only failures should stay with planner")
	}
	if !PlanReviewFailureNeedsArchitect("revise architecture.md: POST route wrong in HTTP table") {
		t.Fatal("explicit revise architecture should escalate")
	}
}

func TestPlanReviewSpuriousFailureReason_integrationContract(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		DeliveryPhases: []DeliveryPhase{
			{ID: "api-handlers", RequiredFiles: []string{"linkshelf/internal/api/handlers.go"}},
		},
		ActivePhaseIDField: "api-handlers",
	}
	reason := PlanReviewSpuriousFailureReason("", "", "plan.md missing ## Integration contract", v)
	if reason == "" {
		t.Fatal("expected spurious integration contract reason in api-handlers phase")
	}
}

func TestPlanReviewSpuriousFailureReason_missingBead(t *testing.T) {
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd not in PATH")
	}
	townRoot := t.TempDir()
	rig := "mockrig"
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	beadsDir := filepath.Join(townRoot, rig, ".beads")
	for _, d := range []string{rigDir, beadsDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	init := exec.Command("bd", "init")
	init.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
	init.Dir = rigDir
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("bd init: %v\n%s", err, out)
	}
	v := WorkflowValidation{
		LayoutRoot:        "linkshelf",
		BeadTitleContains: "Implement linkshelf/",
		RequiredFiles:     []string{"linkshelf/internal/api/handlers.go"},
	}
	cmd := exec.Command("bd", "create", "--type", "task", "--title", "Implement linkshelf/internal/api/handlers.go per architecture", "--description=test")
	cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
	cmd.Dir = rigDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bd create: %v\n%s", err, out)
	}
	reason := PlanReviewSpuriousFailureReason(townRoot, rig, "missing bead for linkshelf/internal/api/handlers.go", v)
	if reason == "" {
		t.Fatal("expected spurious missing-bead reason when bead exists")
	}
}

func TestRejectSpuriousQAFailure_verifyPassesBeforeShellError(t *testing.T) {
	townRoot := t.TempDir()
	rig := "rigqa"

	profDir := filepath.Join(townRoot, rig, "mayor", "rig", rigProfileDir)
	if err := os.MkdirAll(profDir, 0755); err != nil {
		t.Fatal(err)
	}
	env := rigProfileEnvelope{
		Version: 1,
		Source:  "test",
		Validation: WorkflowValidation{
			// No Go/Python/smoke triggers — phaseVerifyPasses returns nil (passes).
		},
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profDir, rigProfileFile), raw, 0644); err != nil {
		t.Fatal(err)
	}

	t.Run("verify passes — shell error (command not found) rejected as spurious", func(t *testing.T) {
		reason := rejectSpuriousQAFailure(townRoot, rig, "command not found: pytest", "")
		if reason == "" {
			t.Fatal("expected spurious rejection when shell error + verify passes")
		}
		if !strings.Contains(reason, "phase verify passes") {
			t.Fatalf("want phase-verify reason, got: %s", reason)
		}
	})

	t.Run("verify passes — 'syntax error' (not a recognized shell error) NOT rejected", func(t *testing.T) {
		reason := rejectSpuriousQAFailure(townRoot, rig, "syntax error in command", "")
		if reason != "" {
			t.Fatalf("expected no rejection for vague 'syntax error' summary: %s", reason)
		}
	})

	t.Run("no profile — shell error not rejected (genuine failure)", func(t *testing.T) {
		other := t.TempDir()
		reason := rejectSpuriousQAFailure(other, rig, "command not found", "")
		if reason != "" {
			t.Fatalf("expected no rejection for shell error without profile: %s", reason)
		}
	})

	t.Run("no profile — syntax error not rejected (no code quality claim)", func(t *testing.T) {
		other := t.TempDir()
		reason := rejectSpuriousQAFailure(other, rig, "syntax error in command", "")
		if reason != "" {
			t.Fatalf("expected no rejection for syntax error: %s", reason)
		}
	})

	t.Run("verify passes — non-shell error is NOT rejected (legitimate QA finding)", func(t *testing.T) {
		reason := rejectSpuriousQAFailure(townRoot, rig, "Frontend/Backend API contract mismatch: index.html uses 'description' but spec defines 'title'", "")
		if reason != "" {
			t.Fatalf("expected no rejection for legitimate QA finding: %s", reason)
		}
	})
}
