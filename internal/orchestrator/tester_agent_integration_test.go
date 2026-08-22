package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIntegrationTesterAgentWorkflowStages validates the tester agent behavior
// at each workflow stage: test_plan creation, freezing, test_review phase-scoped
// validation, and phase progression.
func TestIntegrationTesterAgentWorkflowStages(t *testing.T) {
	// Create the correct directory structure: {townRoot}/{rig}/mayor/rig/
	townRoot := t.TempDir()
	rig := "testrig"
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	profileDir := filepath.Join(rigDir, ".gastown")
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create test files for different phases
	phases := map[string][]string{
		"go-module": {"pingapp/go.mod"},
		"core":      {"pingapp/cmd/server/main_test.go"},
		"web":       {"pingapp/e2e/ping.spec.ts"},
	}
	for phase, files := range phases {
		for _, f := range files {
			dir := filepath.Join(rigDir, filepath.Dir(f))
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			content := "package main\nimport \"testing\"\nfunc Test" + phase + "(t *testing.T) {}"
			if err := os.WriteFile(filepath.Join(rigDir, f), []byte(content), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}

	t.Run("test_plan creation", func(t *testing.T) {
		plan := `### req-1
Requirement: Go module initialization
Level: unit
Phase: go-module
Test file: pingapp/go.mod
Bead ID: pw-001

### req-2
Requirement: HTTP server
Level: unit
Phase: core
Test file: pingapp/cmd/server/main_test.go
Bead ID: pw-002

### req-3
Requirement: Web UI
Level: ui
Phase: web
Test file: pingapp/e2e/ping.spec.ts
Bead ID: pw-003
`
		blocks := ParseTestPlanBlocks(plan)
		if len(blocks) != 3 {
			t.Fatalf("expected 3 blocks, got %d", len(blocks))
		}

		// Verify all blocks have required fields
		for _, b := range blocks {
			if b.ReqID == "" || b.Level == "" || b.TestFile == "" {
				t.Errorf("block %s missing required fields: level=%q test_file=%q", b.ReqID, b.Level, b.TestFile)
			}
			if b.Phase == "" {
				t.Errorf("block %s missing phase field", b.ReqID)
			}
		}
	})

	t.Run("test_plan freezing", func(t *testing.T) {
		// Create a valid profile file with delivery phases
		profilePath := filepath.Join(profileDir, rigProfileFile)
		initialProfile := `{"version":1,"generated_at":"2024-01-01T00:00:00Z","source":"test","confidence":"1.0","validation":{"delivery_phases":[{"id":"go-module","title":"Go Module","required_files":["go.mod"]}]},"test_plan_reviewed":false,"test_plan_frozen":false}`
		if err := os.WriteFile(profilePath, []byte(initialProfile), 0644); err != nil {
			t.Fatal(err)
		}

		// Initially not frozen
		if IsTestPlanFrozen(townRoot, rig) {
			t.Error("plan should not be frozen initially")
		}

		// Set reviewed (should also freeze)
		if err := SetTestPlanReviewed(townRoot, rig, true); err != nil {
			t.Fatal(err)
		}

		if !IsTestPlanReviewed(townRoot, rig) {
			t.Error("plan should be reviewed")
		}
		if !IsTestPlanFrozen(townRoot, rig) {
			t.Error("plan should be frozen after review")
		}

		// Unfreeze by resetting
		if err := SetTestPlanFrozen(townRoot, rig, false); err != nil {
			t.Fatal(err)
		}
		if IsTestPlanFrozen(townRoot, rig) {
			t.Error("plan should not be frozen after unfreeze")
		}

		// Reset clears both flags (profile has delivery phases)
		if err := SetTestPlanReviewed(townRoot, rig, true); err != nil {
			t.Fatal(err)
		}
		if err := ResetRigPhaseForNewWorkflow(townRoot, rig); err != nil {
			t.Fatal(err)
		}
		if IsTestPlanReviewed(townRoot, rig) {
			t.Error("plan should not be reviewed after reset")
		}
		if IsTestPlanFrozen(townRoot, rig) {
			t.Error("plan should not be frozen after reset")
		}
	})

	t.Run("phase-scoped test validation", func(t *testing.T) {
		plan := `### req-1
Requirement: Go module initialization
Level: unit
Phase: go-module
Test file: pingapp/go.mod

### req-2
Requirement: HTTP server
Level: unit
Phase: core
Test file: pingapp/cmd/server/main_test.go

### req-3
Requirement: Web UI
Level: ui
Phase: web
Test file: pingapp/e2e/ping.spec.ts
`

		// go-module phase: only go.mod should be checked
		missing := MissingPlannedTestFilesForPhases(rigDir, "", plan, []string{"go-module"})
		if len(missing) != 0 {
			t.Errorf("go-module phase: expected 0 missing, got %d: %v", len(missing), missing)
		}

		// core phase: only main_test.go should be checked
		missing = MissingPlannedTestFilesForPhases(rigDir, "", plan, []string{"core"})
		if len(missing) != 0 {
			t.Errorf("core phase: expected 0 missing, got %d: %v", len(missing), missing)
		}

		// web phase: ping.spec.ts should be checked (exists)
		missing = MissingPlannedTestFilesForPhases(rigDir, "", plan, []string{"web"})
		if len(missing) != 0 {
			t.Errorf("web phase: expected 0 missing, got %d: %v", len(missing), missing)
		}

		// core + go-module (regression check): both should be checked
		missing = MissingPlannedTestFilesForPhases(rigDir, "", plan, []string{"core", "go-module"})
		if len(missing) != 0 {
			t.Errorf("core+go-module phases: expected 0 missing, got %d: %v", len(missing), missing)
		}
	})

	t.Run("phase-scoped validation skips future phases", func(t *testing.T) {
		plan := `### req-1
Requirement: Go module initialization
Level: unit
Phase: go-module
Test file: pingapp/go.mod

### req-2
Requirement: HTTP server
Level: unit
Phase: core
Test file: pingapp/cmd/server/main_test.go

### req-3
Requirement: Web UI
Level: ui
Phase: web
Test file: pingapp/e2e/ping.spec.ts
`
		// Only go-module phase complete: core and web should be skipped
		missing := MissingPlannedTestFilesForPhases(rigDir, "", plan, []string{"go-module"})
		if len(missing) != 0 {
			t.Errorf("only go-module: expected 0 missing (future phases skipped), got %d: %v", len(missing), missing)
		}

		// go-module + core complete: web should be skipped
		missing = MissingPlannedTestFilesForPhases(rigDir, "", plan, []string{"go-module", "core"})
		if len(missing) != 0 {
			t.Errorf("go-module+core: expected 0 missing (future phases skipped), got %d: %v", len(missing), missing)
		}
	})

	t.Run("backward compatible without Phase field", func(t *testing.T) {
		plan := `### req-1
Requirement: Go module initialization
Level: unit
Test file: pingapp/go.mod

### req-2
Requirement: HTTP server
Level: unit
Test file: pingapp/cmd/server/main_test.go
`
		// All blocks should be included (no Phase field)
		files := PlannedTestFilesForPhases(plan, []string{"go-module"})
		if len(files) != 2 {
			t.Errorf("expected 2 files (backward compatible), got %d: %v", len(files), files)
		}

		missing := MissingPlannedTestFilesForPhases(rigDir, "", plan, []string{"go-module"})
		if len(missing) != 0 {
			t.Errorf("backward compatible: expected 0 missing, got %d: %v", len(missing), missing)
		}
	})
}

// TestIntegrationTesterWriteGuardFrozenPlan validates that the tester cannot
// write TEST_PLAN.md when the plan is frozen.
func TestIntegrationTesterWriteGuardFrozenPlan(t *testing.T) {
	// Create the correct directory structure: {townRoot}/{rig}/mayor/rig/
	townRoot := t.TempDir()
	rig := "testrig"
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	profileDir := filepath.Join(rigDir, ".gastown")
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write a minimal profile
	profilePath := filepath.Join(profileDir, rigProfileFile)
	if err := os.WriteFile(profilePath, []byte(`{"version":1,"test_plan_reviewed":true,"test_plan_frozen":true}`), 0644); err != nil {
		t.Fatal(err)
	}

	// Verify plan is frozen
	if !IsTestPlanFrozen(townRoot, rig) {
		t.Fatal("plan should be frozen")
	}

	// Test that IsTesterWritingTestPlan detects writes to TEST_PLAN.md
	tests := []struct {
		cmd      string
		expected bool
	}{
		{"cat > TEST_PLAN.md <<'EOF' ... EOF", true},
		{"cat > test_plan.md <<'EOF' ... EOF", true},
		{"echo 'content' > test_plan.md", true},
		{"cat > other.md <<'EOF' ... EOF", false},
		{"cat TEST_PLAN.md", false},
		{"ls -la", false},
	}

	for _, tt := range tests {
		got := IsTesterWritingTestPlan(tt.cmd)
		if got != tt.expected {
			t.Errorf("IsTesterWritingTestPlan(%q) = %v, want %v", tt.cmd, got, tt.expected)
		}
	}
}
