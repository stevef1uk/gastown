package orchestrator

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeTestPhasedProfile3 writes a three-phase delivery profile (backend, api, frontend) so the
// implementation rewind tests can exercise "earliest problematic phase" and "future phase skipped"
// scenarios that require more than the two-phase helper.
func writeTestPhasedProfile3(t *testing.T, townRoot, rig, activePhase string) {
	t.Helper()
	v := WorkflowValidation{
		LayoutRoot:          "linkshelf",
		BeadTitleContains:   "Implement ",
		MinPlanBytes:        100,
		MinArchitectureBytes: 200,
		QAVerifyCommand:     "cd linkshelf && go test ./...",
		DeliveryPhases: []DeliveryPhase{
			{
				ID:    "backend",
				Title: "Backend Store",
				RequiredFiles: []string{
					"linkshelf/go.mod",
					"linkshelf/internal/store/store.go",
					"linkshelf/internal/store/store_test.go",
				},
				QAVerifyCommand: "cd linkshelf && go test internal/store",
			},
			{
				ID:    "api",
				Title: "API Handlers",
				RequiredFiles: []string{
					"linkshelf/internal/api/handlers.go",
				},
				QAVerifyCommand: "cd linkshelf && go test internal/api",
			},
			{
				ID:    "frontend",
				Title: "Frontend",
				RequiredFiles: []string{
					"linkshelf/web/index.html",
				},
				QAVerifyCommand: "cd linkshelf && go test ./web",
			},
		},
		ActivePhaseIDField: activePhase,
	}
	v = FinalizeDeliveryPhases(v)
	if err := WriteRigWorkflowProfile(townRoot, rig, v, "test", "high"); err != nil {
		t.Fatal(err)
	}
}

// advancePhasesUpTo syncs, writes real content for, and closes each implement phase from the
// first phase through the phase immediately before targetID, marking each completed. It returns
// the reloaded profile so callers can assert on active phase after their setup. The target phase
// is left active with its beads open.
func advancePhasesUpTo(t *testing.T, townRoot, rig, rigDir, beadsDir, targetID string) WorkflowValidation {
	t.Helper()
	v, ok, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatal(err)
	}
	// Find index of targetID
	targetIdx := -1
	for i, p := range v.DeliveryPhases {
		if strings.TrimSpace(p.ID) == targetID {
			targetIdx = i
			break
		}
	}
	if targetIdx == -1 {
		t.Fatalf("target phase %q not found in delivery phases", targetID)
	}
	// Advance through phases 0 to targetIdx-1
	for i := 0; i < targetIdx; i++ {
		p := v.DeliveryPhases[i]
		phaseScope := v
		phaseScope.ActivePhaseIDField = strings.TrimSpace(p.ID)
		if _, err := SyncPlanningArtifacts(townRoot, rig, phaseScope.ForActivePhase(), true); err != nil {
			t.Fatal(err)
		}
		for _, rel := range phaseScope.ForActivePhase().RequiredFiles {
			switch {
			case strings.TrimSpace(p.ID) == "backend":
				writeBackendFileContent(t, rigDir, rel)
			case strings.HasSuffix(rel, ".html"):
				writeFrontendFileContent(t, rigDir, rel)
			default:
				writeBackendFileContent(t, rigDir, rel)
			}
		}
		closeAllOpenImplementBeads(t, townRoot, rig, beadsDir, rigDir, phaseScope.ForActivePhase())
		// Move active to next phase
		nextIdx := i + 1
		nextPhase := strings.TrimSpace(v.DeliveryPhases[nextIdx].ID)
		if err := SetRigActivePhase(townRoot, rig, nextPhase); err != nil {
			t.Fatal(err)
		}
		if err := AddRigCompletedPhase(townRoot, rig, strings.TrimSpace(p.ID)); err != nil {
			t.Fatal(err)
		}
		reloaded, ok, err := LoadRigWorkflowProfileFile(townRoot, rig)
		if err != nil || !ok {
			t.Fatal(err)
		}
		v = reloaded
	}
	out, ok, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatal(err)
	}
	return out
}

// TestMaybeRewindToProblemPhaseForImplementation_rewindsEarliestCompletedPhase reproduces the real
// broken-earlier-phase scenario: a completed backend-store phase's store.go is missing on disk
// while a later (active) api phase depends on the store package, so the api implementation cannot
// compile. The rewind must return active_phase_id to backend and reopen its implement bead.
func TestMaybeRewindToProblemPhaseForImplementation_rewindsEarliestCompletedPhase(t *testing.T) {
	townRoot, rig, rigDir, beadsDir := setupPhasedRigTown(t)
	writeTestPhasedProfile3(t, townRoot, rig, "frontend")
	v := advancePhasesUpTo(t, townRoot, rig, rigDir, beadsDir, "frontend")

	// Break an earlier completed phase (backend) file that the api phase depends on.
	if err := os.Remove(filepath.Join(rigDir, "linkshelf", "internal", "store", "store.go")); err != nil {
		t.Fatal(err)
	}

	logLine, rewindErr := MaybeRewindToProblemPhaseForImplementation(townRoot, rig, v)
	if rewindErr == nil {
		t.Fatal("expected rewind error for missing completed-phase file")
	}
	if !strings.Contains(logLine, "rewound active phase to backend") {
		t.Fatalf("expected rewind to backend, got: %q", logLine)
	}
	if !strings.Contains(logLine, "linkshelf/internal/store/store.go") {
		t.Fatalf("expected log to name store.go, got: %q", logLine)
	}
	assertActivePhase(t, townRoot, rig, "backend")

	reloaded, ok, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if got := reloaded.RewoundFromPhaseIDField; got != "frontend" {
		t.Fatalf("rewound_from_phase_id = %q, want frontend", got)
	}
	open, err := ListOpenImplementBeads(townRoot, rig, reloaded.ForActivePhase())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, b := range open {
		if strings.Contains(b.Title, "linkshelf/internal/store/store.go") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected reopened bead for store.go, open beads=%v", open)
	}
}

// TestMaybeRewindToProblemPhaseForImplementation_earliestOfMultipleGaps verifies that when several
// completed phases are missing files, the rewind targets the earliest problematic phase.
func TestMaybeRewindToProblemPhaseForImplementation_earliestOfMultipleGaps(t *testing.T) {
	townRoot, rig, rigDir, beadsDir := setupPhasedRigTown(t)
	writeTestPhasedProfile3(t, townRoot, rig, "frontend")
	v := advancePhasesUpTo(t, townRoot, rig, rigDir, beadsDir, "frontend")

	// Break backend.store.go (phase 0) AND api.handlers.go (phase 1).
	if err := os.Remove(filepath.Join(rigDir, "linkshelf", "internal", "store", "store.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(rigDir, "linkshelf", "internal", "api", "handlers.go")); err != nil {
		t.Fatal(err)
	}

	logLine, rewindErr := MaybeRewindToProblemPhaseForImplementation(townRoot, rig, v)
	if rewindErr == nil {
		t.Fatal("expected rewind error")
	}
	// Earliest phase (backend, index 0) wins, not api.
	if !strings.Contains(logLine, "rewound active phase to backend") {
		t.Fatalf("expected rewind to backend (earliest), got: %q", logLine)
	}
	assertActivePhase(t, townRoot, rig, "backend")
}

// TestMaybeRewindToProblemPhaseForImplementation_skipsFuturePhase verifies that a missing file in a
// FUTURE phase (index > active) does not trigger a rewind — the completed+active scope from
// RequiredFilesForCompletedAndActive() intentionally excludes future-phase files.
func TestMaybeRewindToProblemPhaseForImplementation_skipsFuturePhase(t *testing.T) {
	townRoot, rig, rigDir, beadsDir := setupPhasedRigTown(t)
	writeTestPhasedProfile3(t, townRoot, rig, "api")
	v := advancePhasesUpTo(t, townRoot, rig, rigDir, beadsDir, "api")

	// The frontend (future) file was never created (phase not advanced). It is missing but must NOT
	// trigger a rewind because its phase index (2) >= activeIdx (1). The rewind only scans
	// completed phases (idx < activeIdx).
	logLine, rewindErr := MaybeRewindToProblemPhaseForImplementation(townRoot, rig, v)
	if rewindErr != nil {
		t.Fatalf("expected no rewind for future-phase gap, got err=%v", rewindErr)
	}
	if logLine != "" {
		t.Fatalf("expected empty rewind log, got: %q", logLine)
	}
	assertActivePhase(t, townRoot, rig, "api")
}

// TestMaybeRewindToProblemPhaseForImplementation_ignoresActivePhaseOwnedFiles verifies that a file
// owned by the active phase itself is NOT treated as a rewind trigger: the polecat's normal job is
// to write active-phase files, and a rewind would only mask that.
func TestMaybeRewindToProblemPhaseForImplementation_ignoresActivePhaseOwnedFiles(t *testing.T) {
	townRoot, rig, rigDir, beadsDir := setupPhasedRigTown(t)
	writeTestPhasedProfile3(t, townRoot, rig, "api")
	v := advancePhasesUpTo(t, townRoot, rig, rigDir, beadsDir, "api")

	// Create the active-phase (api) file first, then remove it. Must not rewind.
	handlersPath := filepath.Join(rigDir, "linkshelf", "internal", "api", "handlers.go")
	writeBackendFileContent(t, rigDir, "linkshelf/internal/api/handlers.go")
	if err := os.Remove(handlersPath); err != nil {
		t.Fatal(err)
	}

	logLine, rewindErr := MaybeRewindToProblemPhaseForImplementation(townRoot, rig, v)
	if rewindErr != nil {
		t.Fatalf("expected no rewind for active-phase gap, got err=%v", rewindErr)
	}
	if logLine != "" {
		t.Fatalf("expected empty rewind log, got: %q", logLine)
	}
	assertActivePhase(t, townRoot, rig, "api")
}

// TestMaybeRewindToProblemPhaseForImplementation_noIssueDoesNothing verifies the rewind is a no-op
// when every completed plus active phase file is present and non-stubbed.
func TestMaybeRewindToProblemPhaseForImplementation_noIssueDoesNothing(t *testing.T) {
	townRoot, rig, rigDir, beadsDir := setupPhasedRigTown(t)
	writeTestPhasedProfile3(t, townRoot, rig, "frontend")
	v := advancePhasesUpTo(t, townRoot, rig, rigDir, beadsDir, "frontend")

	logLine, rewindErr := MaybeRewindToProblemPhaseForImplementation(townRoot, rig, v)
	if rewindErr != nil {
		t.Fatalf("unexpected rewind error: %v", rewindErr)
	}
	if logLine != "" {
		t.Fatalf("expected no rewind log, got: %q", logLine)
	}
	assertActivePhase(t, townRoot, rig, "frontend")
}

// TestMaybeRewindToProblemPhaseForImplementation_noPhasedDelivery verifies a no-op when the rig has
// no phased delivery.
func TestMaybeRewindToProblemPhaseForImplementation_noPhasedDelivery(t *testing.T) {
	townRoot, rig, rigDir, _ := setupPhasedRigTown(t)
	writeTestPhasedProfile(t, townRoot, rig, "backend")
	// Strip phases so HasPhasedDelivery() is false.
	v, ok, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatal(err)
	}
	v.DeliveryPhases = nil
	v = FinalizeDeliveryPhases(v)
	if err := WriteRigWorkflowProfile(townRoot, rig, v, "test", "high"); err != nil {
		t.Fatal(err)
	}

	logLine, rewindErr := MaybeRewindToProblemPhaseForImplementation(townRoot, rig, v)
	if rewindErr != nil {
		t.Fatalf("unexpected rewind error for unphased rig: %v", rewindErr)
	}
	if logLine != "" {
		t.Fatalf("expected no rewind for unphased rig, got: %q", logLine)
	}
	_ = rigDir
	_ = v
}

// newImplementationRewindManager builds a manager whose rig-flow template routes
// implementation failure -> planning (matching the real rig-flow), so the implementation-rewind
// hook can redirect it back to implementation when a completed-phase gap is detected.
func newImplementationRewindManager(t *testing.T, townRoot, rig string) (*Manager, string) {
	t.Helper()
	m := NewManager(townRoot)
	m.LoadTemplate(&WorkflowTemplate{
		ID:           "rig-flow",
		InitialState: "implementation",
		States: map[string]State{
			"implementation": {
				Role: "polecat",
				Transitions: map[string]Transition{
					"success": {To: "qa_review"},
					"failure": {To: "planning"},
				},
			},
			"planning": {
				Role: "planner",
				Transitions: map[string]Transition{
					"success": {To: "qa_review"},
				},
			},
			"qa_review": {Role: "qa", Transitions: map[string]Transition{
				"all_passed": {To: "completed"},
			}},
			"completed": {Role: "mayor", Instructions: "done"},
		},
	})
	id, err := m.StartWorkflow("rig-flow", map[string]string{"rig": rig})
	if err != nil {
		t.Fatal(err)
	}
	// StartWorkflow calls ResetRigPhaseForNewWorkflow which clears completed_phase_ids
	// and may change active_phase_id. Re-establish the test scenario:
	// completed phases = [backend, api], active phase = frontend.
	if err := AddRigCompletedPhase(townRoot, rig, "backend"); err != nil {
		t.Fatal(err)
	}
	if err := AddRigCompletedPhase(townRoot, rig, "api"); err != nil {
		t.Fatal(err)
	}
	if err := SetRigActivePhase(townRoot, rig, "frontend"); err != nil {
		t.Fatal(err)
	}
	m.instances[id].CurrentState = "implementation"
	m.instances[id].Status = "running"
	return m, id
}

// TestCompleteTask_implementationFailureRewindsAndRoutesToPolecat verifies the complete hook: when
// the polecat reports implementation failure and an earlier completed phase is missing a file the
// active phase depends on, CompleteTask must rewind active_phase_id to that earlier phase and route
// next to implementation (polecat) instead of the FSM default of planning.
func TestCompleteTask_implementationFailureRewindsAndRoutesToPolecat(t *testing.T) {
	townRoot, rig, rigDir, beadsDir := setupPhasedRigTown(t)
	writeTestPhasedProfile3(t, townRoot, rig, "frontend")
	advancePhasesUpTo(t, townRoot, rig, rigDir, beadsDir, "frontend")

	// Active phase frontend; completed backend (with the store package) is missing store.go —
	// the exact scenario where api/frontend can't build.
	if err := os.Remove(filepath.Join(rigDir, "linkshelf", "internal", "store", "store.go")); err != nil {
		t.Fatal(err)
	}

	m, id := newImplementationRewindManager(t, townRoot, rig)
	next, err := m.CompleteTask(id, "failure", rig+"/polecat",
		"implementation failed: store package missing (store.go)", "cannot build handlers without store", nil)
	if err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}
	// Must route to implementation (polecat), NOT the FSM planning default.
	if next != "implementation" {
		t.Fatalf("next = %q, want implementation (polecat) after rewind", next)
	}
	assertActivePhase(t, townRoot, rig, "backend")

	reloaded, ok, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if got := reloaded.RewoundFromPhaseIDField; got != "frontend" {
		t.Fatalf("rewound_from_phase_id = %q, want frontend", got)
	}
	if !slices.Equal(reloaded.CompletedPhaseIDs(), []string{"backend", "api"}) {
		t.Fatalf("completed_phase_ids = %v, want [backend api]", reloaded.CompletedPhaseIDs())
	}
}

// TestCompleteTask_implementationFailureNoGapRoutesToPlanning verifies the hook is inert when no
// completed-phase gap exists: implementation failure keeps the FSM default (planning).
func TestCompleteTask_implementationFailureNoGapRoutesToPlanning(t *testing.T) {
	townRoot, rig, rigDir, beadsDir := setupPhasedRigTown(t)
	writeTestPhasedProfile3(t, townRoot, rig, "frontend")
	advancePhasesUpTo(t, townRoot, rig, rigDir, beadsDir, "frontend")

	m, id := newImplementationRewindManager(t, townRoot, rig)
	next, err := m.CompleteTask(id, "failure", rig+"/polecat",
		"implementation failed: tests red", "unit tests failing", nil)
	if err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}
	if next != "planning" {
		t.Fatalf("next = %q, want planning (no rewind)", next)
	}
	assertActivePhase(t, townRoot, rig, "frontend")
}
