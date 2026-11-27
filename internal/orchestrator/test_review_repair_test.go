package orchestrator

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func capturePhaseRewindLogs(t *testing.T) func() []string {
	t.Helper()
	var mu sync.Mutex
	var lines []string
	phaseRewindLogMu.Lock()
	old := phaseRewindLogf
	phaseRewindLogf = func(format string, args ...any) {
		mu.Lock()
		lines = append(lines, fmt.Sprintf(format, args...))
		mu.Unlock()
	}
	phaseRewindLogMu.Unlock()
	t.Cleanup(func() {
		phaseRewindLogMu.Lock()
		phaseRewindLogf = old
		phaseRewindLogMu.Unlock()
	})
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), lines...)
	}
}

func phaseRewindLogsForTown(lines []string, town string) []string {
	var result []string
	for _, line := range lines {
		if strings.Contains(line, "town="+town) {
			result = append(result, line)
		}
	}
	return result
}

func assertPhaseRewindLogged(t *testing.T, lines []string, town, from, to, reason string) {
	t.Helper()
	for _, line := range phaseRewindLogsForTown(lines, town) {
		if strings.Contains(line, "from="+strconv.Quote(from)) &&
			strings.Contains(line, "to="+strconv.Quote(to)) &&
			strings.Contains(line, "reason="+strconv.Quote(reason)) {
			return
		}
	}
	t.Fatalf("missing phase rewind log from %s to %s (%s): %v", from, to, reason, phaseRewindLogsForTown(lines, town))
}

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
			for _, summary := range []string{"Test repair: " + tc.test + " lacks mutation-safety assertions", "Reopen sample-store to strengthen coverage"} {
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
			getRewindLogs := capturePhaseRewindLogs(t)
			ids, err := reopenTestReviewRepair(town, rig, v, plan, "Test repair: "+tc.test+" lacks assertions")
			if err != nil || !slices.Equal(ids, []string{"sample-store"}) || statuses["sample-store"] != "in_progress" || statuses["sample-api"] != "open" || statuses["sample-browser"] != "closed" {
				t.Fatalf("reopened=%v statuses=%v err=%v", ids, statuses, err)
			}
			assertPhaseRewindLogged(t, getRewindLogs(), town, "api", "storage", "test review repair")
			profile, _, err := LoadRigWorkflowProfileFile(town, rig)
			if err != nil || profile.ActivePhaseID() != "storage" || profile.RewoundFromPhaseIDField != "browser" || !slices.Equal(profile.CompletedPhaseIDs(), v.CompletedPhaseIDs()) {
				t.Fatalf("repair lost delivery progress: %+v err=%v", profile, err)
			}
			if err := ValidateImplementWritePath(town, rig, "sample-store", tc.test, profile, false, tc.test+" lacks assertions", nil); err != nil {
				t.Fatalf("repair owner cannot edit its Go/Python test companion: %v", err)
			}
			ids, err = reopenTestReviewRepair(town, rig, profile, plan, "Test repair: "+tc.test+" lacks assertions")
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
	repair, err = selectTestReviewRepair(rigDir, v, plan, "Test repair: demo/store/store_test.go needs coverage", beads)
	if err != nil || len(repair.beads) != 1 || repair.beads[0].ID != "sample-store-tests" {
		t.Fatalf("explicit test bead should own its repair: %+v %v", repair, err)
	}
}

func TestTesterRepairDifferentTestsSharingOwnerStillRewinds(t *testing.T) {
	town, rig, v, _, beads := testReviewRepairFixture(t, "demo/store/store.py", "demo/store/test_store.py")
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	v.ActivePhaseIDField = "browser"
	v.DeliveryPhases[2].RequiredFiles = append(v.DeliveryPhases[2].RequiredFiles, "demo/store/store.py")
	v.RequiredFiles = v.UnionRequiredFiles()
	plan := "# Test plan\n\n### storage\nPhase: storage\nLevel: unit\nTest file: demo/store/test_store.py\nBead ID: sample-store\n" +
		"\n### browser\nPhase: browser\nLevel: unit\nTest file: demo/store/store_test.py\nBead ID: sample-store\n"
	recoveryFile(t, rigDir, "demo/store/store_test.py", "def test_store():\n    assert True\n")
	repair, err := selectTestReviewRepair(rigDir, v, plan, "Reopen sample-store to fix both tests", beads)
	if err != nil || repair.phase != "storage" || len(repair.beads) != 1 || repair.beads[0].ID != "sample-store" {
		t.Fatalf("distinct earlier-phase test lost: %+v err=%v", repair, err)
	}
}

func TestTesterRepairManagerIgnoresInventoryAndKeepsSharedTestsInActivePhase(t *testing.T) {
	town, rig, v, plan, beads := testReviewRepairFixture(t, "demo/store/store.go", "demo/store/store_test.go")
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	v.ActivePhaseIDField = "browser"
	v.RewoundFromPhaseIDField = ""
	v.DeliveryPhases[1].RequiredFiles = []string{"demo/server/main_test.go"}
	v.DeliveryPhases[2].RequiredFiles = append(v.DeliveryPhases[2].RequiredFiles, "demo/server/main_test.go")
	v.RequiredFiles = v.UnionRequiredFiles()
	beads[1].Title = "Implement demo/server/main_test.go per architecture"
	plan += "\n### browser-api\nPhase: browser\nLevel: unit\nTest file: demo/server/main_test.go\nBead ID: sample-api\n"
	recoveryFile(t, rigDir, "TEST_PLAN.md", plan)
	recoveryFile(t, rigDir, "demo/tests/e2e.spec.js", "test('browser', () => { expect(1).toBe(1); });\n")
	earlier, err := selectTestReviewRepair(rigDir, v, plan, "Test repair: demo/store/store_test.go lacks assertions", beads)
	if err != nil || earlier.phase != "storage" || len(earlier.beads) != 1 || earlier.beads[0].ID != "sample-store" {
		t.Fatalf("genuine earlier-phase repair lost: %+v err=%v", earlier, err)
	}
	data, err := json.Marshal(rigProfileEnvelope{Version: 1, Validation: v, TestPlanFrozen: true, TestPlanReviewed: true})
	if err != nil {
		t.Fatal(err)
	}
	recoveryFile(t, rigDir, ".gastown/workflow-profile.json", string(data))
	if err := os.MkdirAll(filepath.Join(town, rig, ".beads"), 0700); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	recoveryFile(t, bin, "bd", "#!/bin/sh\nprintf '[]\\n'\n")
	if err := os.Chmod(filepath.Join(bin, "bd"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	status := map[string]string{"sample-store": "closed", "sample-api": "closed", "sample-browser": "closed"}
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
	bdUpdateImplementBeadStatusHook = func(_, _, id, value string) error { status[id] = value; return nil }
	t.Cleanup(func() { bdUpdateImplementBeadStatusHook = prev })
	m := newTestReviewLoopManager(t, town)
	m.instances["wf-repair"] = &WorkflowInstance{ID: "wf-repair", TemplateID: "rig-flow", CurrentState: "test_review", Status: "running", Variables: map[string]string{"rig": rig}}
	summary := "Reopen sample-api: Test repair: demo/server/main_test.go lacks browser API assertions; reopen sample-old: Test repair: demo/tests/e2e.spec.js lacks UI assertions."
	feedback := "Closed bead inventory: sample-store demo/store/store_test.go; sample-api; sample-browser"
	next, err := m.CompleteTask("wf-repair", "failure", rig+"/tester", summary, feedback, nil)
	if err != nil || next != "implementation" {
		t.Fatalf("next=%q err=%v", next, err)
	}
	profile, _, err := LoadRigWorkflowProfileFile(town, rig)
	if err != nil || profile.ActivePhaseID() != "browser" || profile.RewoundFromPhaseIDField != "" {
		t.Fatalf("unexpected phase rewind: %+v err=%v", profile, err)
	}
	if status["sample-store"] != "closed" ||
		!(status["sample-api"] == "in_progress" && status["sample-browser"] == "open" ||
			status["sample-api"] == "open" && status["sample-browser"] == "in_progress") {
		t.Fatalf("wrong repair targets: %v", status)
	}
	pending := m.instances["wf-repair"].PendingRework
	if pending == nil || !strings.Contains(pending.Feedback, "demo/server/main_test.go") ||
		strings.Contains(pending.Feedback, "demo/store/store_test.go") {
		t.Fatalf("repair feedback includes unrelated inventory: %+v", pending)
	}
}

func TestTesterRepairManagerDefersCompletedPhaseTestFinding(t *testing.T) {
	town, rig, v, plan, beads := testReviewRepairFixture(t, "demo/store/store.go", "demo/store/store_test.go")
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	v.RewoundFromPhaseIDField = ""
	data, err := json.Marshal(rigProfileEnvelope{Version: 1, Validation: v, TestPlanFrozen: true, TestPlanReviewed: true})
	if err != nil {
		t.Fatal(err)
	}
	recoveryFile(t, rigDir, ".gastown/workflow-profile.json", string(data))
	recoveryFile(t, rigDir, "TEST_PLAN.md", plan)
	if err := os.MkdirAll(filepath.Join(town, rig, ".beads"), 0700); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	recoveryFile(t, bin, "bd", "#!/bin/sh\nprintf '[]\\n'\n")
	if err := os.Chmod(filepath.Join(bin, "bd"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	status := map[string]string{"sample-store": "closed", "sample-api": "closed", "sample-browser": "closed"}
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
	bdUpdateImplementBeadStatusHook = func(_, _, id, value string) error { status[id] = value; return nil }
	t.Cleanup(func() { bdUpdateImplementBeadStatusHook = prev })
	getRewindLogs := capturePhaseRewindLogs(t)
	m := newTestReviewLoopManager(t, town)
	m.instances["wf-repair"] = &WorkflowInstance{ID: "wf-repair", TemplateID: "rig-flow", CurrentState: "test_review", Status: "running", Variables: map[string]string{"rig": rig}}
	summary := "Test repair: demo/store/store_test.go lacks persistence assertions; Implementation repair: demo/server/main_test.go fails"
	next, err := m.CompleteTask("wf-repair", "failure", rig+"/tester", summary, "", nil)
	if err != nil || next != "implementation" {
		t.Fatalf("next=%q err=%v", next, err)
	}
	profile, _, err := LoadRigWorkflowProfileFile(town, rig)
	if err != nil || profile.ActivePhaseID() != "api" || profile.RewoundFromPhaseIDField != "" {
		t.Fatalf("test-only completed-phase finding rewound delivery: %+v err=%v", profile, err)
	}
	if status["sample-store"] != "closed" || (status["sample-api"] != "open" && status["sample-api"] != "in_progress") {
		t.Fatalf("wrong active repair target: %v", status)
	}
	inst := m.instances["wf-repair"]
	if _, ok := inst.DeferredTestReviews["api"].TestRepairFiles["sample-store"]["demo/store/store_test.go"]; !ok {
		t.Fatalf("completed-phase test finding was not deferred: %+v", inst.DeferredTestReviews)
	}
	if logs := phaseRewindLogsForTown(getRewindLogs(), town); len(logs) != 0 {
		t.Fatalf("no rewind should have been logged: %v", logs)
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
			next, err := m.CompleteTask("wf-repair", "failure", rig+"/tester", "Test repair: "+tc.test+" lacks meaningful persistence assertions", "Go/Python verification passes but coverage is inadequate", nil)
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

func TestTesterRepairDefersCompletedPhaseTestFindings(t *testing.T) {
	town, rig, v, plan, beads := testReviewRepairFixture(t, "demo/store/store.go", "demo/store/store_test.go")
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	summary := "Test repair: demo/store/store_test.go lacks persistence assertions; Test repair: demo/server/main_test.go lacks isolation assertions"
	repair, err := selectTestReviewRepair(rigDir, v, plan, summary, beads)
	if err != nil || repair.phase != "api" || !slices.Equal(repair.tests["sample-api"], []string{"demo/server/main_test.go"}) ||
		!slices.Equal(repair.deferred["api"]["sample-store"], []string{"demo/store/store_test.go"}) {
		t.Fatalf("completed-phase test finding should be deferred: %+v, %v", repair, err)
	}
	summary = "Test repair: demo/store/store_test.go lacks assertions; Implementation repair: demo/server/main_test.go fails"
	repair, err = selectTestReviewRepair(rigDir, v, plan, summary, beads)
	if err != nil || repair.phase != "api" || !slices.Equal(repair.deferred["api"]["sample-store"], []string{"demo/store/store_test.go"}) {
		t.Fatalf("completed-phase test finding should be deferred behind active implementation repair: %+v, %v", repair, err)
	}
	repair, err = selectTestReviewRepair(rigDir, v, plan, "Implementation repair: demo/store/store_test.go fails due to store behavior", beads)
	if err != nil || len(repair.tests) != 0 {
		t.Fatalf("implementation failure must not require changing its correct test: %+v, %v", repair, err)
	}
	if _, err := selectTestReviewRepair(rigDir, v, plan, "demo/store/store_test.go fails", beads); err == nil || !strings.Contains(err.Error(), "classify") {
		t.Fatalf("ambiguous test finding should be rejected: %v", err)
	}
}

func TestValidateTestReviewRepairSummary_futurePhase(t *testing.T) {
	town, rig, v, _, _ := testReviewRepairFixture(t, "demo/store/store.go", "demo/store/store_test.go")
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	// Active phase is "api", completed is "storage", future is "browser".
	// Reject repair demand on future-phase test.
	futureRepairSummary := "Test repair: demo/tests/e2e.spec.js lacks assertions"
	if err := ValidateTestReviewRepairSummary(rigDir, v, futureRepairSummary); err == nil || !strings.Contains(err.Error(), "future phase") {
		t.Fatalf("expected error rejecting future-phase test repair, got %v", err)
	}

	// Allow summary that notes future-phase test is deferred or out of scope.
	deferredSummary := "Test repair: demo/server/main_test.go lacks assertions. Future phase browser test demo/tests/e2e.spec.js is deferred and out of scope."
	if err := ValidateTestReviewRepairSummary(rigDir, v, deferredSummary); err != nil {
		t.Fatalf("expected nil error when future test is deferred, got %v", err)
	}
}

func TestTesterRepairDoesNotReopenPassingInventoryBead(t *testing.T) {
	town, rig, v, plan, beads := testReviewRepairFixture(t, "demo/store/store.go", "demo/store/store_test.go")
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	// The summary names store_test.go for repair, and mentions sample-api only as passing inventory.
	// sample-api must NOT be selected for repair.
	summary := "Test repair: demo/store/store_test.go lacks assertions. phase-api (sample-api) test files exist and phase verify is green."
	repair, err := selectTestReviewRepair(rigDir, v, plan, summary, beads)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := repair.tests["sample-api"]; ok {
		t.Fatalf("expected sample-api not selected for repair when mentioned only as passing inventory, got tests: %+v", repair.tests)
	}
	for _, b := range repair.beads {
		if b.ID == "sample-api" {
			t.Fatalf("expected sample-api not in repair beads, got %+v", repair.beads)
		}
	}
}

func TestTesterRepairBaselineSurvivesRestartAndTimeout(t *testing.T) {
	town := t.TempDir()
	m := newTestReviewLoopManager(t, town)
	prepareMockTestReview(t, town, m)
	tpl := m.templates["rig-flow"]
	implementation := tpl.States["implementation"]
	implementation.Transitions["timeout"] = Transition{To: "implementation"}
	implementation.Transitions["failure"] = Transition{To: "planning"}
	tpl.States["implementation"] = implementation
	tpl.States["planning"] = State{Role: "planner", Instructions: "Repair plan", Transitions: map[string]Transition{"success": {To: "implementation"}}}
	review := tpl.States["test_review"]
	review.Transitions["success"] = Transition{To: "qa_review"}
	tpl.States["test_review"] = review
	m.instances["wf-repair"] = &WorkflowInstance{ID: "wf-repair", TemplateID: "rig-flow", CurrentState: "test_review", Status: "running", Variables: map[string]string{"rig": "mockrig"}}
	summary := "Test repair: demo/handlers_test.go lacks assertion for the requested behavior (te-7o7)"
	if _, err := m.CompleteTask("wf-repair", "failure", "mockrig/tester", summary, "", nil); err != nil {
		t.Fatal(err)
	}
	m = NewManager(town)
	m.LoadTemplate(tpl)
	inst := m.instances["wf-repair"]
	if inst == nil || inst.PendingRework == nil || len(inst.PendingRework.TestRepairFiles["te-7o7"]) != 1 {
		t.Fatalf("repair baseline was not persisted: %+v", inst)
	}
	if _, err := m.applyStateTimeout("wf-repair"); err != nil {
		t.Fatal(err)
	}
	m = NewManager(town)
	m.LoadTemplate(tpl)
	inst = m.instances["wf-repair"]
	if inst.PendingRework == nil || inst.PendingRework.Summary == "" || len(inst.PendingRework.TestRepairFiles["te-7o7"]) != 1 {
		t.Fatalf("timeout lost original finding: %+v", inst.PendingRework)
	}
	if next, err := m.CompleteTask("wf-repair", "failure", "mockrig/polecat", "need planning help", "", nil); err != nil || next != "planning" {
		t.Fatalf("implementation failure did not reach planning: next=%s err=%v", next, err)
	}
	m = NewManager(town)
	m.LoadTemplate(tpl)
	inst = m.instances["wf-repair"]
	if inst.ActiveTestRepair == nil || len(inst.ActiveTestRepair.TestRepairFiles["te-7o7"]) != 1 {
		t.Fatalf("intervening planning lost repair baseline: %+v", inst.ActiveTestRepair)
	}
	if _, err := m.CompleteTask("wf-repair", "success", "mockrig/planner", "plan repaired", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CompleteTask("wf-repair", "success", "mockrig/polecat", "tests pass", "", nil); err == nil || !strings.Contains(err.Error(), "unchanged") {
		t.Fatalf("unchanged repair accepted: %v", err)
	}
	rigDir := filepath.Join(town, "mockrig", "mayor", "rig")
	recoveryFile(t, rigDir, "demo/handlers_test.go", "package demo\nfunc test() { assertBehavior() }\n")
	if _, err := m.CompleteTask("wf-repair", "success", "mockrig/polecat", "repair made", "", nil); err != nil {
		t.Fatal(err)
	}
	if inst.ActiveTestRepair == nil || inst.ActiveTestRepair.Summary == "" {
		t.Fatal("original finding was not carried to the follow-up review")
	}
	if _, err := m.CompleteTask("wf-repair", "success", "mockrig/tester", "covered", "", nil); err == nil {
		t.Fatal("review without evidence accepted")
	}
	recoveryFile(t, rigDir, "test-report.md", "- Evidence: {\"file\":\"demo/handlers_test.go\",\"line\":2,\"excerpt\":\"invented assertion\"}\n")
	if _, err := m.CompleteTask("wf-repair", "success", "mockrig/tester", "covered", "", nil); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("invented evidence accepted: %v", err)
	}
	recoveryFile(t, rigDir, "test-report.md", "- Evidence: {\"file\":\"demo/handlers_test.go\",\"line\":2,\"excerpt\":\"func test() { assertBehavior() }\"}\n")
	if _, err := m.CompleteTask("wf-repair", "success", "mockrig/tester", "covered", "", nil); err != nil {
		t.Fatal(err)
	}
}

func TestTesterRepairMissingFilesAndUnsafePaths(t *testing.T) {
	rigDir := t.TempDir()
	files, err := snapshotTestRepairFiles(rigDir, map[string][]string{"bead": {"tests/missing.py"}})
	if err != nil {
		t.Fatal(err)
	}

	if err := ValidateTestRepairChanges(rigDir, files, "bead"); err == nil {
		t.Fatal("missing test was accepted")
	}
	recoveryFile(t, rigDir, "tests/missing.py", "assert True\n")
	if err := ValidateTestRepairChanges(rigDir, files, "bead"); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshotTestRepairFiles(rigDir, map[string][]string{"bead": {"../outside"}}); err == nil {
		t.Fatal("escaping path accepted")
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(rigDir, "outside")); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshotTestRepairFiles(rigDir, map[string][]string{"bead": {"outside/file"}}); err == nil {
		t.Fatal("escaping symlink accepted")
	}
}

func TestTesterRepairDeferredPhaseSurvivesRestart(t *testing.T) {
	town, rig, v, _, beads := testReviewRepairFixture(t, "demo/store/store.go", "demo/store/store_test.go")
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	if err := os.MkdirAll(filepath.Join(town, rig, ".beads"), 0700); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	recoveryFile(t, bin, "bd", "#!/bin/sh\nprintf '[]\\n'\n")
	if err := os.Chmod(filepath.Join(bin, "bd"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	setListImplementBeadsByStatusHook(t, town, rig, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "closed" {
			return beads, nil
		}
		return nil, nil
	})
	prev := bdUpdateImplementBeadStatusHook
	bdUpdateImplementBeadStatusHook = func(_, _, _, _ string) error { return nil }
	t.Cleanup(func() { bdUpdateImplementBeadStatusHook = prev })
	m := newTestReviewLoopManager(t, town)
	tpl := m.templates["rig-flow"]
	review := tpl.States["test_review"]
	review.Instructions = "Review tests"
	review.Transitions["success"] = Transition{To: "qa_review"}
	tpl.States["test_review"] = review
	m.instances["wf-repair"] = &WorkflowInstance{ID: "wf-repair", TemplateID: "rig-flow", CurrentState: "test_review", Status: "running", Variables: map[string]string{"rig": rig}}
	summary := "Test repair: demo/store/store_test.go lacks persistence assertions; Test repair: demo/server/main_test.go lacks isolation assertions"
	if _, err := m.CompleteTask("wf-repair", "failure", rig+"/tester", summary, "", nil); err != nil {
		t.Fatal(err)
	}
	m = NewManager(town)
	m.LoadTemplate(tpl)
	inst := m.instances["wf-repair"]
	if _, ok := inst.DeferredTestReviews["api"].TestRepairFiles["sample-store"]["demo/store/store_test.go"]; !ok {
		t.Fatalf("completed-phase finding was lost: %+v", inst.DeferredTestReviews)
	}
	if err := SetRigActivePhase(town, rig, v.ActivePhaseID()); err != nil {
		t.Fatal(err)
	}
	inst.CurrentState = "test_review"
	inst.PendingRework = nil
	inst.ActiveTestRepair = nil
	payload, err := m.BuildTaskPayload(inst, tpl, review)
	if err != nil || payload["deferred_test_review"] == nil {
		t.Fatalf("deferred review missing completed-phase finding: %+v, %v", payload, err)
	}
	if _, err := m.CompleteTask("wf-repair", "success", rig+"/tester", "covered", "", nil); err == nil {
		t.Fatal("deferred finding was silently approved without evidence")
	}
	recoveryFile(t, rigDir, "test-report.md", "- Evidence: {\"file\":\"demo/store/store_test.go\",\"line\":3,\"excerpt\":\"func TestRoundTrip(t *testing.T) { if got := 1; got != 1 { t.Fatalf(\\\"got %d\\\", got) } }\"}\n")
	if _, err := m.CompleteTask("wf-repair", "success", rig+"/tester", "covered", "", nil); err != nil {
		t.Fatal(err)
	}
	if len(inst.DeferredTestReviews) != 0 {
		t.Fatalf("reviewed finding was not cleared: %+v", inst.DeferredTestReviews)
	}
}

func TestTesterRepairIncorrectFindingCanUnfreezePlan(t *testing.T) {
	town, rig, _, _, _ := testReviewRepairFixture(t, "demo/store/store.go", "demo/store/store_test.go")
	m := newTestReviewLoopManager(t, town)
	tpl := m.templates["rig-flow"]
	implementation := tpl.States["implementation"]
	implementation.Transitions["plan_gap"] = Transition{To: "test_plan_rework"}
	tpl.States["implementation"] = implementation
	m.instances["wf-repair"] = &WorkflowInstance{
		ID: "wf-repair", TemplateID: "rig-flow", CurrentState: "implementation", Status: "running",
		Variables: map[string]string{"rig": rig},
		PendingRework: &WorkflowRework{
			FromState: "test_review", Summary: "Original finding: test lacks persistence assertion",
			TestRepairFiles: map[string]map[string]string{"sample-store": {"demo/store/store_test.go": "baseline"}},
		},
	}

	if !IsTestPlanFrozen(town, rig) || !IsTestPlanReviewed(town, rig) {
		t.Fatal("fixture should start with a frozen reviewed plan")
	}
	next, err := m.CompleteTask("wf-repair", "plan_gap", rig+"/polecat", "finding is incorrect", "existing test asserts persistence", nil)
	if err != nil || next != "test_plan_rework" || IsTestPlanFrozen(town, rig) || IsTestPlanReviewed(town, rig) {
		t.Fatalf("incorrect finding could not reach plan correction: next=%s err=%v", next, err)
	}
	if pending := m.instances["wf-repair"].PendingRework; pending == nil || !strings.Contains(pending.Feedback, "Original finding") {
		t.Fatalf("plan correction lost original finding: %+v", pending)
	}
}

func TestValidateContractMismatchSummary_requiresTrace(t *testing.T) {
	good := `Implementation repair: demo/server/main_test.py — SPEC: SPEC.md §API-3 '400 {"error":...}'; Assertion: demo/server/main_test.py:115 'assert detail'; Production fix: demo/server/main.py:179 return error envelope; reopen sample-api`
	if err := ValidateContractMismatchSummary(good); err != nil {
		t.Fatalf("valid trace rejected: %v", err)
	}
	for _, marker := range []string{"Implementation repair:", "SPEC:", "Assertion:", "Production fix:"} {
		bad := strings.Replace(good, marker, "", 1)
		if err := ValidateContractMismatchSummary(bad); err == nil {
			t.Fatalf("missing %q was accepted", marker)
		}
	}
}

func TestSelectTestReviewRepair_contractMismatchDoesNotFlagTestFiles(t *testing.T) {
	town, rig, v, plan, beads := testReviewRepairFixture(t, "demo/store/store.py", "demo/store/test_store.py")
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	source, test := "demo/server/main.go", "demo/server/main_test.go"
	summary := `Implementation repair: ` + test + ` — SPEC: SPEC.md §API-3 '400 {"error":...}'; Assertion: ` + test + `:115 'assert detail'; Production fix: ` + source + `:179 return error envelope; reopen sample-api`
	repair, err := selectTestReviewRepair(rigDir, v, plan, summary, beads)
	if err != nil {
		t.Fatal(err)
	}
	if len(repair.beads) != 1 || repair.beads[0].ID != "sample-api" {
		t.Fatalf("wrong repair owner: %+v", repair.beads)
	}
	if len(repair.tests) != 0 {
		t.Fatalf("contract mismatch must not flag test files for rewriting: %+v", repair.tests)
	}
}

func TestTestReviewTimeoutRetriesAndEscalates(t *testing.T) {
	for _, load := range []func() (*WorkflowTemplate, error){LoadRigFlowTemplate, LoadReqFlowTemplate} {
		tpl, err := load()
		if err != nil {
			t.Fatal(err)
		}
		t.Run(tpl.ID, func(t *testing.T) {
			m := NewManager(t.TempDir())
			m.LoadTemplate(tpl)
			m.instances["wf-timeout"] = &WorkflowInstance{ID: "wf-timeout", TemplateID: tpl.ID, CurrentState: "test_review", Status: "running"}
			for _, want := range []string{"test_review", "test_review", "review_recovery"} {
				next, err := m.applyStateTimeout("wf-timeout")
				if err != nil || next != want {
					t.Fatalf("next=%q want=%q err=%v", next, want, err)
				}
			}
			m.instances["wf-timeout"].CurrentState = "test_review"
			for range 3 {
				if _, err := m.applyStateTimeout("wf-timeout"); err != nil {
					t.Fatal(err)
				}
			}
			if inst := m.instances["wf-timeout"]; inst.Status != "paused" || !strings.Contains(inst.PauseReason, "timed out") {
				t.Fatalf("repeated review timeouts did not pause: %+v", inst)
			}
		})
	}
}

func TestSelectTestReviewRepair_unreportedBrokenCompletedPhaseTestDoesNotRewind(t *testing.T) {
	town, rig, v, _, beads := testReviewRepairFixture(t, "demo/store/store.py", "demo/store/test_store.py")
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	v.ActivePhaseIDField = "browser"
	v.DeliveryPhases[2].RequiredFiles = append(v.DeliveryPhases[2].RequiredFiles, "demo/store/store.py")
	v.RequiredFiles = v.UnionRequiredFiles()
	// Earlier phase "storage" test file is intentionally NOT created on disk (broken/missing),
	// but reviewer explicitly reports a failure in "browser" only.
	plan := "# Test plan\n\n### storage\nPhase: storage\nLevel: unit\nTest file: demo/store/test_store.py\nBead ID: sample-store\n" +
		"\n### browser\nPhase: browser\nLevel: unit\nTest file: demo/store/store_test.py\nBead ID: sample-browser\n"
	beads = append(beads, PlanBead{ID: "sample-browser", Title: "Implement demo/store/store_test.py per architecture"})
	recoveryFile(t, rigDir, "demo/store/store_test.py", "def test_store():\n    assert True\n")
	summary := "Test repair: demo/store/store_test.py — lacks assertions; reopen bead sample-browser. Future phases deferred."
	repair, err := selectTestReviewRepair(rigDir, v, plan, summary, beads)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repair.phase != "browser" {
		t.Fatalf("expected active phase browser to be repaired, but got rewound phase %q: %+v", repair.phase, repair)
	}
	if len(repair.beads) != 1 || repair.beads[0].ID != "sample-browser" {
		t.Fatalf("expected bead sample-browser, got %+v", repair.beads)
	}
}

