package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestPhaseAdvanceProfileErrorDoesNotCompleteWorkflow(t *testing.T) {
	for _, target := range []string{"advance_phase", "completed"} {
		t.Run(target, func(t *testing.T) {
			townRoot, rig := t.TempDir(), "phaserig"
			profileDir := filepath.Join(townRoot, rig, "mayor", "rig", ".gastown")
			if err := os.MkdirAll(profileDir, 0755); err != nil {
				t.Fatal(err)
			}
			writeTestPhasedProfile(t, townRoot, rig, "backend")
			m, wfID := loadPhasedManager(t, townRoot, rig)
			tpl := m.templates["rig-flow"]
			state := tpl.States["qa_review"]
			state.Transitions["all_passed"] = Transition{To: target}
			tpl.States["qa_review"] = state
			// Simulate a damaged/unreadable profile after QA fetched its task.
			if err := os.WriteFile(filepath.Join(profileDir, rigProfileFile), []byte("{"), 0644); err != nil {
				t.Fatal(err)
			}
			if next, err := m.CompleteTask(wfID, "all_passed", rig+"/qa", "phase green", "", nil); err == nil || next == "completed" {
				t.Fatalf("profile error must reject completion: next=%q err=%v", next, err)
			}
			inst := m.instances[wfID]
			if inst.Status != "running" || inst.CurrentState != "qa_review" {
				t.Fatalf("QA must remain retryable, got status=%q state=%q", inst.Status, inst.CurrentState)
			}
		})
	}
}

func TestConcurrentProfileWritesDoNotShareTemporaryFile(t *testing.T) {
	townRoot, rig := t.TempDir(), "phaserig"
	profileDir := filepath.Join(townRoot, rig, "mayor", "rig", ".gastown")
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		t.Fatal(err)
	}
	profile := rigProfileEnvelope{Version: 1, Validation: WorkflowValidation{LayoutRoot: "app"}}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if err := SaveRigWorkflowProfileEnvelope(townRoot, rig, profile); err != nil {
					t.Errorf("concurrent profile write: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	data, err := os.ReadFile(filepath.Join(profileDir, rigProfileFile))
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) {
		t.Fatalf("profile corrupted by concurrent writes: %s", data)
	}
}
