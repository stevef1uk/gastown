package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsurePhaseBoundaryIsolation_stashesFutureFilesAndRestoresWhenActive(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "testrig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")

	// Phase 1 (core): app/core.go, app/core_test.go
	// Phase 2 (api): app/api.go, app/api_test.go
	// Phase 3 (kanban): app/kanban_test.go
	v := WorkflowValidation{
		LayoutRoot: "app",
		DeliveryPhases: []DeliveryPhase{
			{ID: "phase-1", RequiredFiles: []string{"app/core.go", "app/core_test.go"}},
			{ID: "phase-2", RequiredFiles: []string{"app/api.go", "app/api_test.go"}},
			{ID: "phase-3", RequiredFiles: []string{"app/kanban_test.go"}},
		},
		ActivePhaseIDField: "phase-2",
	}

	appDir := filepath.Join(rigDir, "app")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create files on disk
	for _, f := range []string{"app/core.go", "app/core_test.go", "app/api.go", "app/api_test.go", "app/kanban_test.go"} {
		p := filepath.Join(rigDir, filepath.FromSlash(f))
		if err := os.WriteFile(p, []byte("// "+f), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// Active phase is phase-2. phase-3's kanban_test.go should be isolated/stashed.
	isolated, restored, err := EnsurePhaseBoundaryIsolation(dir, rig, v)
	if err != nil {
		t.Fatalf("EnsurePhaseBoundaryIsolation: %v", err)
	}
	if len(isolated) != 1 || isolated[0] != "app/kanban_test.go" {
		t.Fatalf("expected app/kanban_test.go isolated, got %v", isolated)
	}
	if len(restored) != 0 {
		t.Fatalf("expected 0 restored, got %v", restored)
	}

	// Verify kanban_test.go no longer exists in appDir
	if _, err := os.Stat(filepath.Join(rigDir, "app/kanban_test.go")); !os.IsNotExist(err) {
		t.Fatal("expected app/kanban_test.go to be moved to stash")
	}

	// Advance phase to phase-3
	v.ActivePhaseIDField = "phase-3"
	isolated2, restored2, err2 := EnsurePhaseBoundaryIsolation(dir, rig, v)
	if err2 != nil {
		t.Fatalf("EnsurePhaseBoundaryIsolation phase 3: %v", err2)
	}
	if len(isolated2) != 0 {
		t.Fatalf("expected 0 isolated in phase 3, got %v", isolated2)
	}
	if len(restored2) != 1 || restored2[0] != "app/kanban_test.go" {
		t.Fatalf("expected app/kanban_test.go restored, got %v", restored2)
	}

	// Verify kanban_test.go is restored
	if _, err := os.Stat(filepath.Join(rigDir, "app/kanban_test.go")); err != nil {
		t.Fatalf("expected app/kanban_test.go restored on disk: %v", err)
	}
}

func TestEnsurePhaseBoundaryIsolation_stashesCompanionTestOfFuturePhase(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "testrig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")

	// Phase 1 (core): app/core.go
	// Phase 2 (api): app/api.go
	// On disk: app/api_test.go is created, but NOT listed in RequiredFiles
	v := WorkflowValidation{
		LayoutRoot: "app",
		DeliveryPhases: []DeliveryPhase{
			{ID: "phase-1", RequiredFiles: []string{"app/core.go"}},
			{ID: "phase-2", RequiredFiles: []string{"app/api.go"}},
		},
		ActivePhaseIDField: "phase-1",
	}

	appDir := filepath.Join(rigDir, "app")
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatal(err)
	}

	for _, f := range []string{"app/core.go", "app/api.go", "app/api_test.go"} {
		p := filepath.Join(rigDir, filepath.FromSlash(f))
		if err := os.WriteFile(p, []byte("// "+f), 0644); err != nil {
			t.Fatal(err)
		}
	}

	// In phase-1, both app/api.go (required by future phase) and app/api_test.go (correlated test of future phase) must be stashed
	isolated, _, err := EnsurePhaseBoundaryIsolation(dir, rig, v)
	if err != nil {
		t.Fatalf("EnsurePhaseBoundaryIsolation: %v", err)
	}
	if len(isolated) != 2 {
		t.Fatalf("expected 2 files isolated (app/api.go and app/api_test.go), got %v", isolated)
	}

	// Verify app/api_test.go is moved to stash
	if _, err := os.Stat(filepath.Join(rigDir, "app/api_test.go")); !os.IsNotExist(err) {
		t.Fatal("expected app/api_test.go to be moved to stash")
	}

	// Advance to phase-2: both should be restored
	v.ActivePhaseIDField = "phase-2"
	_, restored, err := EnsurePhaseBoundaryIsolation(dir, rig, v)
	if err != nil {
		t.Fatalf("EnsurePhaseBoundaryIsolation: %v", err)
	}
	if len(restored) != 2 {
		t.Fatalf("expected 2 files restored, got %v", restored)
	}
	if _, err := os.Stat(filepath.Join(rigDir, "app/api_test.go")); err != nil {
		t.Fatalf("expected app/api_test.go restored on disk: %v", err)
	}
}

func TestEnsurePhaseBoundaryIsolation_rescuesPastFilesTrappedInFutureStash(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "testrig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")

	// Phase 1 (foundation): scripts/stop.sh
	// Phase 2 (polish): app/page.tsx
	// Phase 3 (release): test/e2e.spec.ts (accidentally had scripts/stop.sh in its stash folder)
	v := WorkflowValidation{
		LayoutRoot: "app",
		DeliveryPhases: []DeliveryPhase{
			{ID: "foundation", RequiredFiles: []string{"scripts/stop.sh"}},
			{ID: "polish", RequiredFiles: []string{"app/page.tsx"}},
			{ID: "release", RequiredFiles: []string{"test/e2e.spec.ts"}},
		},
		ActivePhaseIDField: "polish",
	}

	// Suppose scripts/stop.sh was previously moved into release's stash folder
	releaseStashDir := filepath.Join(rigDir, ".gastown", "phase-stash", "release", "scripts")
	if err := os.MkdirAll(releaseStashDir, 0755); err != nil {
		t.Fatal(err)
	}
	stashedStopScript := filepath.Join(releaseStashDir, "stop.sh")
	if err := os.WriteFile(stashedStopScript, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}

	// Active phase is polish (Phase 2). scripts/stop.sh is needed by foundation (Phase 1 <= Active).
	_, restored, err := EnsurePhaseBoundaryIsolation(dir, rig, v)
	if err != nil {
		t.Fatalf("EnsurePhaseBoundaryIsolation: %v", err)
	}

	// scripts/stop.sh should have been rescued from release stash and restored to rigDir/scripts/stop.sh
	restoredPath := filepath.Join(rigDir, "scripts", "stop.sh")
	if _, err := os.Stat(restoredPath); err != nil {
		t.Fatalf("expected scripts/stop.sh to be restored on disk: %v (restored list: %v)", err, restored)
	}
}
