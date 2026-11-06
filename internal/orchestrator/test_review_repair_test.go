package orchestrator

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func testReviewRepairFixture(t *testing.T, source, test string) (string, string, WorkflowValidation, string, []PlanBead) {
	t.Helper()
	town, rig := t.TempDir(), "sample"
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	v := WorkflowValidation{
		LayoutRoot: "demo", BeadTitleContains: "Implement ",
		ActivePhaseIDField: "api", RewoundFromPhaseIDField: "browser",
		CompletedPhaseIDsField: []string{"storage", "api"},
		DeliveryPhases: []DeliveryPhase{
			{ID: "storage", RequiredFiles: []string{source}},
			{ID: "api", RequiredFiles: []string{"demo/server/main.go"}},
			{ID: "browser", RequiredFiles: []string{"demo/tests/e2e.spec.js"}},
		},
	}
	v.RequiredFiles = v.UnionRequiredFiles()
	plan := "# Test plan\n\n### storage\nPhase: storage\nLevel: unit\nTest file: " + test + "\nBead ID: \n" +
		"\n### api\nPhase: api\nLevel: unit\nTest file: demo/server/main_test.go\nBead ID: sample-api\n" +
		"\n### browser\nPhase: browser\nLevel: integration\nTest file: demo/tests/e2e.spec.js\nBead ID: \n"
	for file, data := range map[string]string{
		test:                       "package store\nimport \"testing\"\nfunc TestRoundTrip(t *testing.T) { if got := 1; got != 1 { t.Fatalf(\"got %d\", got) } }\n",
		"demo/server/main_test.go": "package main\nimport \"testing\"\nfunc TestStartup(t *testing.T) { if got := 1; got != 1 { t.Fatalf(\"got %d\", got) } }\n",
		"TEST_PLAN.md":             plan,
	} {
		path := filepath.Join(rigDir, file)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if strings.HasSuffix(test, ".py") {
		if err := os.WriteFile(filepath.Join(rigDir, test), []byte("import unittest\nclass StoreTest(unittest.TestCase):\n    def test_round_trip(self):\n        self.assertEqual(dict(title='example')['title'], 'example')\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	profileDir := filepath.Join(rigDir, ".gastown")
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(rigProfileEnvelope{Version: 1, Validation: v, TestPlanFrozen: true, TestPlanReviewed: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, "workflow-profile.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	beads := []PlanBead{
		{ID: "sample-store", Title: "Implement " + source + " per architecture"},
		{ID: "sample-api", Title: "Implement demo/server/main.go per architecture"},
		{ID: "sample-browser", Title: "Implement demo/tests/e2e.spec.js per architecture"},
	}
	return town, rig, v, plan, beads
}

func TestTesterRepairResolvesGoAndPythonCompanions(t *testing.T) {
	for _, tc := range []struct{ name, source, test string }{
		{"go", "demo/store/store.go", "demo/store/store_test.go"},
		{"python-prefix", "demo/store/store.py", "demo/store/test_store.py"},
		{"python-suffix", "demo/store/store.py", "demo/store/store_test.py"},
		{"python-tests-dir", "demo/store/store.py", "demo/tests/test_store.py"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			town, rig, v, plan, beads := testReviewRepairFixture(t, tc.source, tc.test)
			rigDir := filepath.Join(town, rig, "mayor", "rig")
			for _, summary := range []string{tc.test + " lacks mutation-safety assertions", "Reopen sample-store to strengthen coverage"} {
				repair, err := selectTestReviewRepair(rigDir, v, plan, summary, beads)
				if err != nil || repair.phase != "storage" || len(repair.beads) != 1 || repair.beads[0].ID != "sample-store" {
					t.Fatalf("repair=%+v err=%v", repair, err)
				}
			}
			statuses := map[string]string{"sample-store": "closed", "sample-api": "in_progress", "sample-browser": "closed"}
			setListImplementBeadsByStatusHook(t, town, rig, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
				var result []PlanBead
				for _, bead := range beads {
					if statuses[bead.ID] == status {
						result = append(result, bead)
					}
				}
				return result, nil
			})
			prev := bdUpdateImplementBeadStatusHook
			t.Cleanup(func() { bdUpdateImplementBeadStatusHook = prev })
			bdUpdateImplementBeadStatusHook = func(_, _, id, status string) error { statuses[id] = status; return nil }
			ids, err := reopenTestReviewRepair(town, rig, v, plan, tc.test+" lacks assertions")
			if err != nil || !slices.Equal(ids, []string{"sample-store"}) || statuses["sample-store"] != "in_progress" || statuses["sample-api"] != "open" || statuses["sample-browser"] != "closed" {
				t.Fatalf("reopened=%v statuses=%v err=%v", ids, statuses, err)
			}
			profile, _, err := LoadRigWorkflowProfileFile(town, rig)
			if err != nil || profile.ActivePhaseID() != "storage" || profile.RewoundFromPhaseIDField != "browser" || !slices.Equal(profile.CompletedPhaseIDs(), v.CompletedPhaseIDs()) {
				t.Fatalf("repair lost delivery progress: %+v err=%v", profile, err)
			}
			if err := ValidateImplementWritePath(town, rig, "sample-store", tc.test, profile, false, tc.test+" lacks assertions", nil); err != nil {
				t.Fatalf("repair owner cannot edit its Go/Python test companion: %v", err)
			}
			ids, err = reopenTestReviewRepair(town, rig, profile, plan, tc.test+" lacks assertions")
			if err != nil || !slices.Equal(ids, []string{"sample-store"}) {
				t.Fatalf("existing open repair must stay actionable: ids=%v err=%v", ids, err)
			}
		})
	}
}

func TestTesterRepairScopesAndOwnershipGaps(t *testing.T) {
	town, rig, v, plan, beads := testReviewRepairFixture(t, "demo/store/store.go", "demo/store/store_test.go")
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	repair, err := selectTestReviewRepair(rigDir, v, plan, "verification failed", beads)
	if err != nil || repair.phase != "api" || len(repair.beads) != 1 || repair.beads[0].ID != "sample-api" {
		t.Fatalf("fallback must select active source, not future missing browser tests: %+v %v", repair, err)
	}
	for _, tc := range []struct {
		name, plan string
		beads      []PlanBead
	}{
		{"unowned", strings.ReplaceAll(plan, "demo/store/store_test.go", "demo/unrelated/test_other.py"), beads},
		{"missing-owner", plan, beads[1:]},
		{"ambiguous", plan, append(append([]PlanBead(nil), beads...), PlanBead{ID: "sample-duplicate", Title: beads[0].Title})},
		{"empty", "# Empty plan", beads},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := selectTestReviewRepair(rigDir, v, tc.plan, "coverage inadequate", tc.beads)
			var gap *testReviewPlanGap
			if !errors.As(err, &gap) {
				t.Fatalf("expected actionable plan gap, got %v", err)
			}
		})
	}
	beads = append(beads, PlanBead{ID: "sample-store-tests", Title: "Implement demo/store/store_test.go per TEST_PLAN"})
	repair, err = selectTestReviewRepair(rigDir, v, plan, "demo/store/store_test.go needs coverage", beads)
	if err != nil || len(repair.beads) != 1 || repair.beads[0].ID != "sample-store-tests" {
		t.Fatalf("explicit test bead should own its repair: %+v %v", repair, err)
	}
}

func TestTesterRepairManagerRoutesOwnedWorkAndPlanGaps(t *testing.T) {
	requireBdIntegration(t)
	for _, tc := range []struct {
		name, source, test string
		gap                bool
	}{
		{"go", "demo/store/store.go", "demo/store/store_test.go", false},
		{"python", "demo/store/store.py", "demo/store/test_store.py", false},
		{"ownership-gap", "demo/store/store.py", "demo/store/test_store.py", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			town, rig, v, _, beads := testReviewRepairFixture(t, tc.source, tc.test)
			beadsDir := filepath.Join(town, rig, ".beads")
			if err := os.MkdirAll(beadsDir, 0700); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bd", "init")
			cmd.Dir = filepath.Join(town, rig, "mayor", "rig")
			cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("bd init: %v\n%s", err, out)
			}
			if tc.gap {
				beads = beads[1:]
			}
			status := map[string]string{}
			for _, bead := range beads {
				status[bead.ID] = "closed"
			}
			setListImplementBeadsByStatusHook(t, town, rig, func(_, _ string, _ WorkflowValidation, want string) ([]PlanBead, error) {
				var result []PlanBead
				for _, bead := range beads {
					if status[bead.ID] == want {
						result = append(result, bead)
					}
				}
				return result, nil
			})
			prev := bdUpdateImplementBeadStatusHook
			t.Cleanup(func() { bdUpdateImplementBeadStatusHook = prev })
			bdUpdateImplementBeadStatusHook = func(_, _, id, state string) error { status[id] = state; return nil }
			m := newTestReviewLoopManager(t, town)
			m.instances["wf-repair"] = &WorkflowInstance{ID: "wf-repair", TemplateID: "rig-flow", CurrentState: "test_review", Status: "running", Variables: map[string]string{"rig": rig}}
			next, err := m.CompleteTask("wf-repair", "failure", rig+"/tester", tc.test+" lacks meaningful persistence assertions", "Go/Python verification passes but coverage is inadequate", nil)
			if err != nil {
				t.Fatal(err)
			}
			pending := m.instances["wf-repair"].PendingRework
			if pending == nil || !strings.Contains(pending.Summary, tc.test) {
				t.Fatalf("repair lost Tester findings: %+v", pending)
			}
			if tc.gap {
				if next != "test_plan_rework" || pending.Outcome != "plan_gap" || IsTestPlanFrozen(town, rig) || IsTestPlanReviewed(town, rig) {
					t.Fatalf("ownership gap must unfreeze plan and return to Tester: next=%s pending=%+v", next, pending)
				}
				return
			}
			if next != "implementation" || status["sample-store"] != "in_progress" || !strings.Contains(pending.Summary, "sample-store") || !strings.Contains(pending.Feedback, "sample-store") {
				t.Fatalf("repair handed back an empty queue: next=%s status=%v pending=%+v", next, status, pending)
			}
			profile, _, err := LoadRigWorkflowProfileFile(town, rig)
			if err != nil || profile.ActivePhaseID() != "storage" || profile.RewoundFromPhaseIDField != v.RewoundFromPhaseIDField {
				t.Fatalf("repair lost resume phase: %+v %v", profile, err)
			}
		})
	}
}
