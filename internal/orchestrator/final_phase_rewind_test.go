package orchestrator

import (
	"os"
	"path/filepath"
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
	writeTestPhasedProfile(t, townRoot, rig, "frontend")
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
	writeTestPhasedProfile(t, townRoot, rig, "frontend")
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

	writeTestPhasedProfile(t, townRoot, rig, "frontend")
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
