package orchestrator

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeFrontendFileContent(t *testing.T, rigDir, rel string) {
	t.Helper()
	full := filepath.Join(rigDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	var content string
	switch {
	case strings.HasSuffix(rel, ".html"):
		content = `<!DOCTYPE html>
<html>
<head><title>Link Shelf</title></head>
<body><h1>Links</h1></body>
</html>
`
	case strings.HasSuffix(rel, ".go"):
		content = `package main

import (
	"fmt"
	"log"
	"net/http"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "Link Shelf server")
	})
	log.Println("starting server on :8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatal(err)
	}
}
`
	default:
		content = "placeholder\n"
	}
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func writeBackendFileContent(t *testing.T, rigDir, rel string) {
	t.Helper()
	full := filepath.Join(rigDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	var content string
	switch {
	case strings.HasSuffix(rel, "_test.go"):
		content = `package store

import "testing"

func TestStore(t *testing.T) {
	if Store() == "" {
		t.Fatal("store returned empty")
	}
}
`
	case strings.HasSuffix(rel, "schema.go"):
		content = `package store

// Schema returns the SQLite DDL for the linkshelf tables.
func Schema() string {
	return "CREATE TABLE IF NOT EXISTS items (id INTEGER PRIMARY KEY, name TEXT);"
}
`
	case strings.HasSuffix(rel, "go.mod"):
		content = "module linkshelf\n\ngo 1.21\n"
	default:
		content = `package store

// Store returns a placeholder value for the store package.
func Store() string {
	return "stored"
}
`
	}
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// TestMaybeRewindToProblemPhaseForFinalPhase_rewindsEarliestPhase verifies that when the active
// phase is the final one and an earlier phase's required file is missing, the active phase is
// rewound to that earlier phase and the corresponding implement bead is reopened.
func TestMaybeRewindToProblemPhaseForFinalPhase_rewindsEarliestPhase(t *testing.T) {
	townRoot, rig, rigDir, beadsDir := setupPhasedRigTown(t)
	writeTestPhasedProfile(t, townRoot, rig, "backend")

	v, ok, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatal(err)
	}

	// Create and close backend beads with real content.
	if _, err := SyncPlanningArtifacts(townRoot, rig, v.ForActivePhase(), true); err != nil {
		t.Fatal(err)
	}
	for _, rel := range v.ForActivePhase().RequiredFiles {
		writeBackendFileContent(t, rigDir, rel)
	}
	closeAllOpenImplementBeads(t, townRoot, rig, beadsDir, rigDir, v.ForActivePhase())

	// Simulate advancement to the final phase.
	if err := SetRigActivePhase(townRoot, rig, "frontend"); err != nil {
		t.Fatal(err)
	}
	v, ok, err = LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if !v.IsFinalDeliveryPhase() {
		t.Fatal("expected frontend to be the final phase")
	}

	// Remove a backend file to create a final-phase validation failure.
	missing := filepath.Join(rigDir, "linkshelf", "internal", "store", "store.go")
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}

	logLine, rewindErr := MaybeRewindToProblemPhaseForFinalPhase(townRoot, rig, v)
	if rewindErr == nil {
		t.Fatal("expected rewind error for missing backend file")
	}
	if !strings.Contains(logLine, "rewound active phase to backend") {
		t.Fatalf("expected rewind log to mention backend, got: %q", logLine)
	}
	if !strings.Contains(logLine, "linkshelf/internal/store/store.go") {
		t.Fatalf("expected rewind log to mention missing file, got: %q", logLine)
	}

	assertActivePhase(t, townRoot, rig, "backend")

	// The closed backend bead should now be open again.
	open, err := ListOpenImplementBeads(townRoot, rig, v.ForActivePhase())
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
		t.Fatalf("expected open bead for linkshelf/internal/store/store.go, open beads=%v", open)
	}
}

// TestRewindToPhaseForClosedFile_rewindsOwningPhase verifies that when a write targets a closed
// implement file owned by an earlier phase, the rig is returned to that phase and the closed bead
// is reopened so the file can be repaired — instead of dead-ending on the closed-bead guard.
func TestRewindToPhaseForClosedFile_rewindsOwningPhase(t *testing.T) {
	townRoot, rig, rigDir, _ := setupPhasedRigTown(t)
	writeTestPhasedProfile(t, townRoot, rig, "backend")

	v, ok, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if _, err := SyncPlanningArtifacts(townRoot, rig, v.ForActivePhase(), true); err != nil {
		t.Fatal(err)
	}
	for _, rel := range v.ForActivePhase().RequiredFiles {
		writeBackendFileContent(t, rigDir, rel)
	}
	closeAllOpenImplementBeads(t, townRoot, rig, filepath.Join(townRoot, rig, ".beads"), rigDir, v.ForActivePhase())

	// Advance to the frontend phase, then break the earlier-phase store.go so its
	// closed bead must be reopened for repair.
	if err := SetRigActivePhase(townRoot, rig, "frontend"); err != nil {
		t.Fatal(err)
	}
	v, ok, err = LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if !v.IsFinalDeliveryPhase() {
		t.Fatal("expected frontend to be the final phase")
	}
	if err := os.Remove(filepath.Join(rigDir, "linkshelf", "internal", "store", "store.go")); err != nil {
		t.Fatal(err)
	}

	logLine, rewindErr := RewindToPhaseForClosedFile(townRoot, rig, "linkshelf/internal/store/store.go", v)
	if rewindErr != nil {
		t.Fatalf("unexpected rewind error: %v", rewindErr)
	}
	if !strings.Contains(logLine, "rewound active phase frontend → backend") {
		t.Fatalf("expected log to mention rewind frontend → backend, got: %q", logLine)
	}
	if !strings.Contains(logLine, "linkshelf/internal/store/store.go") {
		t.Fatalf("expected log to mention store.go, got: %q", logLine)
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
		t.Fatalf("expected reopened bead for linkshelf/internal/store/store.go, open beads=%v", open)
	}
}

// TestRewindToPhaseForClosedFile_currentOrFuturePhaseNoOp verifies that files owned by the current
// phase (or no phase at all) do not trigger a rewind.
func TestRewindToPhaseForClosedFile_currentOrFuturePhaseNoOp(t *testing.T) {
	townRoot, rig, rigDir, _ := setupPhasedRigTown(t)
	writeTestPhasedProfile(t, townRoot, rig, "backend")

	v, ok, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if _, err := SyncPlanningArtifacts(townRoot, rig, v.ForActivePhase(), true); err != nil {
		t.Fatal(err)
	}
	for _, rel := range v.ForActivePhase().RequiredFiles {
		writeBackendFileContent(t, rigDir, rel)
	}
	closeAllOpenImplementBeads(t, townRoot, rig, filepath.Join(townRoot, rig, ".beads"), rigDir, v.ForActivePhase())

	// Current-phase file: rewind must be a no-op even though its bead is closed.
	logLine, err := RewindToPhaseForClosedFile(townRoot, rig, "linkshelf/internal/store/store.go", v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if logLine != "" {
		t.Fatalf("expected no rewind for current-phase file, got: %q", logLine)
	}
	assertActivePhase(t, townRoot, rig, "backend")

	// File not owned by any phase: no-op.
	logLine, err = RewindToPhaseForClosedFile(townRoot, rig, "linkshelf/unowned/thing.txt", v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if logLine != "" {
		t.Fatalf("expected no rewind for unowned file, got: %q", logLine)
	}
	assertActivePhase(t, townRoot, rig, "backend")
}

// TestMaybeRewindToProblemPhaseForFinalPhase_noIssueDoesNothing verifies that when all required
// files are present and non-stubbed, the final-phase rewind is a no-op.
func TestMaybeRewindToProblemPhaseForFinalPhase_noIssueDoesNothing(t *testing.T) {
	townRoot, rig, rigDir, beadsDir := setupPhasedRigTown(t)
	writeTestPhasedProfile(t, townRoot, rig, "backend")

	v, ok, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatal(err)
	}

	if _, err := SyncPlanningArtifacts(townRoot, rig, v.ForActivePhase(), true); err != nil {
		t.Fatal(err)
	}
	for _, rel := range v.ForActivePhase().RequiredFiles {
		writeBackendFileContent(t, rigDir, rel)
	}
	closeAllOpenImplementBeads(t, townRoot, rig, beadsDir, rigDir, v.ForActivePhase())

	if err := SetRigActivePhase(townRoot, rig, "frontend"); err != nil {
		t.Fatal(err)
	}
	v, ok, err = LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatal(err)
	}

	// Create frontend files so final-phase validation passes.
	for _, rel := range v.ForActivePhase().RequiredFiles {
		writeFrontendFileContent(t, rigDir, rel)
	}

	logLine, rewindErr := MaybeRewindToProblemPhaseForFinalPhase(townRoot, rig, v)
	if rewindErr != nil {
		t.Fatalf("unexpected rewind error: %v", rewindErr)
	}
	if logLine != "" {
		t.Fatalf("expected no rewind log, got: %q", logLine)
	}
	assertActivePhase(t, townRoot, rig, "frontend")
}

// TestMaybeRewindToProblemPhaseForQA_rewindsClosedCompletedPhase verifies that when a completed
// (earlier) phase's required file goes missing on disk, QA's rewind returns the active phase to
// that earlier phase and reopens its implement bead so the polecat can repair the file — the
// rewind QA performs on the Planner's behalf, routing to the polecat.
func TestMaybeRewindToProblemPhaseForQA_rewindsClosedCompletedPhase(t *testing.T) {
	townRoot, rig, rigDir, beadsDir := setupPhasedRigTown(t)
	writeTestPhasedProfile(t, townRoot, rig, "backend")

	v, ok, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatal(err)
	}
	// Create and close the backend (completed) phase beads with real content.
	if _, err := SyncPlanningArtifacts(townRoot, rig, v.ForActivePhase(), true); err != nil {
		t.Fatal(err)
	}
	for _, rel := range v.ForActivePhase().RequiredFiles {
		writeBackendFileContent(t, rigDir, rel)
	}
	closeAllOpenImplementBeads(t, townRoot, rig, beadsDir, rigDir, v.ForActivePhase())

	// Advance to the frontend phase and mark backend completed.
	if err := SetRigActivePhase(townRoot, rig, "frontend"); err != nil {
		t.Fatal(err)
	}
	if err := AddRigCompletedPhase(townRoot, rig, "backend"); err != nil {
		t.Fatal(err)
	}
	v, ok, err = LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatal(err)
	}

	// Break a completed-phase file.
	if err := os.Remove(filepath.Join(rigDir, "linkshelf", "internal", "store", "store.go")); err != nil {
		t.Fatal(err)
	}

	logLine, rewindErr := MaybeRewindToProblemPhaseForQA(townRoot, rig, v)
	if rewindErr == nil {
		t.Fatal("expected rewind error for missing completed-phase file")
	}
	if !strings.Contains(logLine, "rewound active phase to backend") {
		t.Fatalf("expected rewind log to mention backend, got: %q", logLine)
	}
	if !strings.Contains(logLine, "linkshelf/internal/store/store.go") {
		t.Fatalf("expected rewind log to mention store.go, got: %q", logLine)
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
		t.Fatalf("expected reopened bead for linkshelf/internal/store/store.go, open beads=%v", open)
	}
}

// TestMaybeRewindToProblemPhaseForQA_noIssueDoesNothing verifies that QA's rewind is a no-op when
// all completed-phase files are present and non-stubbed.
func TestMaybeRewindToProblemPhaseForQA_noIssueDoesNothing(t *testing.T) {
	townRoot, rig, rigDir, beadsDir := setupPhasedRigTown(t)
	writeTestPhasedProfile(t, townRoot, rig, "backend")

	v, ok, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if _, err := SyncPlanningArtifacts(townRoot, rig, v.ForActivePhase(), true); err != nil {
		t.Fatal(err)
	}
	for _, rel := range v.ForActivePhase().RequiredFiles {
		writeBackendFileContent(t, rigDir, rel)
	}
	closeAllOpenImplementBeads(t, townRoot, rig, beadsDir, rigDir, v.ForActivePhase())

	if err := SetRigActivePhase(townRoot, rig, "frontend"); err != nil {
		t.Fatal(err)
	}
	if err := AddRigCompletedPhase(townRoot, rig, "backend"); err != nil {
		t.Fatal(err)
	}
	v, ok, err = LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatal(err)
	}

	logLine, rewindErr := MaybeRewindToProblemPhaseForQA(townRoot, rig, v)
	if rewindErr != nil {
		t.Fatalf("unexpected rewind error: %v", rewindErr)
	}
	if logLine != "" {
		t.Fatalf("expected no rewind log, got: %q", logLine)
	}
	assertActivePhase(t, townRoot, rig, "frontend")
}

// TestCompleteRewindAdvanceCycle verifies the complete workflow-profile.json
// state through a full rewind/advance cycle:
// 1. Backend phase: advance to frontend (verify completed_phase_ids, rewound_from, active)
// 2. Delete backend file -> triggers rewind (verify active=backend, rewound_from=frontend, completed preserved)
// 3. Fix file, advance again (verify completed_phase_ids preserved, rewound_from cleared, active=frontend)
func TestCompleteRewindAdvanceCycle(t *testing.T) {
	t.Parallel()
	townRoot, rig, rigDir, beadsDir := setupPhasedRigTown(t)
	writeTestPhasedProfile(t, townRoot, rig, "backend")

	// Step 1: Setup backend phase - create and close beads
	v, ok, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if _, err := SyncPlanningArtifacts(townRoot, rig, v.ForActivePhase(), true); err != nil {
		t.Fatal(err)
	}
	for _, rel := range v.ForActivePhase().RequiredFiles {
		writeBackendFileContent(t, rigDir, rel)
	}
	closeAllOpenImplementBeads(t, townRoot, rig, beadsDir, rigDir, v.ForActivePhase())

	// Step 2: Advance to frontend phase (simulates QA all_passed)
	// In real workflow: TryAdvanceDeliveryPhaseAfterQA calls SetRigActivePhase + AddRigCompletedPhase
	if err := SetRigActivePhase(townRoot, rig, "frontend"); err != nil {
		t.Fatal(err)
	}
	if err := AddRigCompletedPhase(townRoot, rig, "backend"); err != nil {
		t.Fatal(err)
	}
	if err := ClearRigRewoundFromPhase(townRoot, rig); err != nil {
		t.Fatal(err)
	}

	// Verify profile after advance: completed_phase_ids = ["backend"], active=frontend, rewound_from=""
	v, ok, err = LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if !slices.Equal(v.CompletedPhaseIDs(), []string{"backend"}) {
		t.Fatalf("after advance: completed_phase_ids = %v want [backend]", v.CompletedPhaseIDs())
	}
	if v.RewoundFromPhaseIDField != "" {
		t.Fatalf("after advance: rewound_from_phase_id = %q want empty", v.RewoundFromPhaseIDField)
	}
	if v.ActivePhaseID() != "frontend" {
		t.Fatalf("after advance: active_phase_id = %q want frontend", v.ActivePhaseID())
	}

	// Create frontend files
	for _, rel := range v.ForActivePhase().RequiredFiles {
		writeFrontendFileContent(t, rigDir, rel)
	}

	// Step 3: Delete a backend file -> triggers rewind
	missing := filepath.Join(rigDir, "linkshelf", "internal", "store", "store.go")
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}

	logLine, rewindErr := RewindToPhaseForClosedFile(townRoot, rig, "linkshelf/internal/store/store.go", v)
	if rewindErr != nil {
		t.Fatalf("unexpected rewind error: %v", rewindErr)
	}
	if !strings.Contains(logLine, "rewound active phase frontend → backend") {
		t.Fatalf("expected rewind log frontend → backend, got: %q", logLine)
	}

	// Verify profile after rewind: active=backend, rewound_from=frontend, completed_phase_ids preserved
	v, ok, err = LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if v.ActivePhaseID() != "backend" {
		t.Fatalf("after rewind: active_phase_id = %q want backend", v.ActivePhaseID())
	}
	if v.RewoundFromPhaseIDField != "frontend" {
		t.Fatalf("after rewind: rewound_from_phase_id = %q want frontend", v.RewoundFromPhaseIDField)
	}
	if !slices.Equal(v.CompletedPhaseIDs(), []string{"backend"}) {
		t.Fatalf("after rewind: completed_phase_ids = %v want [backend]", v.CompletedPhaseIDs())
	}

	// Step 4: Fix the file (recreate it)
	writeBackendFileContent(t, rigDir, "linkshelf/internal/store/store.go")

	// Step 5: Advance again (simulate QA all_passed after fix)
	// In real workflow: TryAdvanceDeliveryPhaseAfterQA calls both SetRigActivePhase and AddRigCompletedPhase
	if err := SetRigActivePhase(townRoot, rig, "frontend"); err != nil {
		t.Fatal(err)
	}
	if err := AddRigCompletedPhase(townRoot, rig, "backend"); err != nil {
		t.Fatal(err)
	}
	if err := ClearRigRewoundFromPhase(townRoot, rig); err != nil {
		t.Fatal(err)
	}

	// Verify profile after re-advance: completed_phase_ids preserved, rewound_from cleared, active=frontend
	v, ok, err = LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if !slices.Equal(v.CompletedPhaseIDs(), []string{"backend"}) {
		t.Fatalf("after re-advance: completed_phase_ids = %v want [backend]", v.CompletedPhaseIDs())
	}
	if v.RewoundFromPhaseIDField != "" {
		t.Fatalf("after re-advance: rewound_from_phase_id = %q want empty", v.RewoundFromPhaseIDField)
	}
	if v.ActivePhaseID() != "frontend" {
		t.Fatalf("after re-advance: active_phase_id = %q want frontend", v.ActivePhaseID())
	}
}
