package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/config"
)

func setupTestRigForRTM(t *testing.T) (string, string) {
	t.Helper()

	townRoot := t.TempDir()

	// Create mayor directory and town.json
	mayorDir := filepath.Join(townRoot, "mayor")
	if err := os.MkdirAll(mayorDir, 0755); err != nil {
		t.Fatalf("mkdir mayor: %v", err)
	}

	townConfig := &config.TownConfig{
		Type:      "town",
		Version:   config.CurrentTownVersion,
		Name:      "test-town",
		CreatedAt: time.Now().Truncate(time.Second),
	}
	townConfigPath := filepath.Join(mayorDir, "town.json")
	if err := config.SaveTownConfig(townConfigPath, townConfig); err != nil {
		t.Fatalf("save town.json: %v", err)
	}

	rigName := "testrig"
	rigDir := filepath.Join(townRoot, rigName, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatalf("mkdir rigDir: %v", err)
	}

	// Create TEST_PLAN.md
	testPlanContent := `# Test Plan
### test-req
Requirement: Verify test functionality
Level: unit
Test file: tests/test_sample.py
Bead ID: closed
Phase: test-phase
`
	if err := os.WriteFile(filepath.Join(rigDir, "TEST_PLAN.md"), []byte(testPlanContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Create test file
	testFile := filepath.Join(rigDir, "tests/test_sample.py")
	if err := os.MkdirAll(filepath.Dir(testFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(testFile, []byte("def test_ok():\n    assert True\n    assert 1 == 1\n"), 0644); err != nil {
		t.Fatal(err)
	}

	return townRoot, rigName
}

func TestRunRigRTM(t *testing.T) {
	townRoot, rigName := setupTestRigForRTM(t)

	// Save and restore flags
	oldTown := workflowTownRoot
	oldJSON := rigRTMJSON
	oldMD := rigRTMMarkdown
	oldOut := rigRTMOutput
	oldAudit := rigRTMAudit
	defer func() {
		workflowTownRoot = oldTown
		rigRTMJSON = oldJSON
		rigRTMMarkdown = oldMD
		rigRTMOutput = oldOut
		rigRTMAudit = oldAudit
	}()

	workflowTownRoot = townRoot
	rigRTMJSON = true
	rigRTMMarkdown = false
	rigRTMAudit = true

	if err := runRigRTM(rigRTMCmd, []string{rigName}); err != nil {
		t.Fatalf("runRigRTM failed: %v", err)
	}

	// Test markdown output file
	outPath := filepath.Join(t.TempDir(), "TRACEABILITY.md")
	rigRTMJSON = false
	rigRTMOutput = outPath

	if err := runRigRTM(rigRTMCmd, []string{rigName}); err != nil {
		t.Fatalf("runRigRTM with output failed: %v", err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty output file")
	}
}
