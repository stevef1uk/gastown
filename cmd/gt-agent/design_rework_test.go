package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestDesignReworkRequiresCurrentAmendment(t *testing.T) {
	for _, from := range []string{"design_review", "planning", "plan_review", "test_plan", "test_plan_review", "implementation", "test_review", "qa_review"} {
		t.Run(from, func(t *testing.T) {
			town := t.TempDir()
			rigDir := filepath.Join(town, "example", "mayor", "rig")
			if err := os.MkdirAll(rigDir, 0755); err != nil {
				t.Fatal(err)
			}
			writeMinimalDesignSPEC(t, rigDir)
			original := "# Architecture\n\nReported defect: old-contract\n\n" + strings.Repeat("Approved contract remains unchanged.\n", 200)
			archPath := filepath.Join(rigDir, "architecture.md")
			if err := os.WriteFile(archPath, []byte(original), 0644); err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-time.Hour)
			if err := os.Chtimes(archPath, old, old); err != nil {
				t.Fatal(err)
			}
			task := rigFlowTask(t, "design", orchestrator.DefaultWorkflowValidation())
			task.Variables = map[string]string{"designArchWritten": "true"}
			task.PendingRework = &orchestrator.WorkflowRework{FromState: from, Summary: "Fix old-contract only"}
			r := newStateRunner(task, town, "example")
			if r.track.designArchWritten {
				t.Fatal("prior design write satisfied a new rework request")
			}
			var output strings.Builder
			r.processOrchestratedTools("CMD: cat example/mayor/rig/architecture.md", "test", &output)
			if _, _, ok := r.tryAutoOutcome(); ok {
				t.Fatal("reading the existing document must not auto-complete rework")
			}
			output.Reset()
			r.processOrchestratedTools("CMD: sed -i.bak 's/old-contract/repaired-contract/' example/mayor/rig/architecture.md", "test", &output)
			if !r.track.designArchWritten {
				t.Fatalf("targeted edit did not count as amendment: %s", output.String())
			}
			if err := r.validateArtifacts("success"); err != nil {
				t.Fatalf("in-place amendment must pass without a heredoc: %v", err)
			}
			got, err := os.ReadFile(archPath)
			if err != nil || string(got) != strings.Replace(original, "old-contract", "repaired-contract", 1) {
				t.Fatalf("amendment changed unrelated design content: err=%v", err)
			}
		})
	}
}

func TestMissingBeadFilesStayInPlanningRecovery(t *testing.T) {
	town := t.TempDir()
	rig := "example"
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "plan.md"), []byte("# Plan\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := orchestrator.WorkflowValidation{
		LayoutRoot: "app", BeadTitleContains: "Implement ", RequiredFiles: []string{"app/store.py"},
	}
	var recoveryStatus string
	orchestrator.SetListImplementBeadsByStatusHook(func(_, _, status string) ([]orchestrator.PlanBead, error) {
		if status == "closed" {
			return []orchestrator.PlanBead{{ID: "ex-old", Title: "Implement app/store.py per architecture"}}, nil
		}
		if status == recoveryStatus {
			return []orchestrator.PlanBead{{ID: "ex-recovery", Title: "Implement app/store.py per architecture"}}, nil
		}
		return nil, nil
	})
	t.Cleanup(func() { orchestrator.SetListImplementBeadsByStatusHook(nil) })
	// No active bead: report the actual recovery command, never redesign.
	for _, validate := range []func(string, string, orchestrator.WorkflowValidation) error{
		validateClosedBeadFilesExist, validateRequiredFilesHaveBeads,
	} {
		err := validate(town, rig, v)
		if err == nil || !strings.Contains(err.Error(), "gt rig sync-planning example --force") || strings.Contains(err.Error(), "architecture_failure") {
			t.Fatalf("missing-file recovery must stay with planning sync: %v", err)
		}
	}
	// A historical closed duplicate must not block its open replacement while
	// the Polecat has not yet recreated the missing file.
	for _, status := range []string{"open", "in_progress"} {
		recoveryStatus = status
		if err := validateClosedBeadFilesExist(town, rig, v); err != nil {
			t.Fatalf("%s recovery bead should cover historical closed bead: %v", status, err)
		}
		if err := validateRequiredFilesHaveBeads(town, rig, v); err != nil {
			t.Fatalf("%s recovery bead should cover required path: %v", status, err)
		}
	}
}
