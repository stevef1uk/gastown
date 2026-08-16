package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

// TestValidateImplementationBeadFileWrite_rewindsToOwningPhase verifies that writing to a closed
// implement file owned by an earlier delivery phase returns the rig to that phase and reopens the
// bead (beads can only be opened in the current phase), instead of dead-ending the agent.
func TestValidateImplementationBeadFileWrite_rewindsToOwningPhase(t *testing.T) {
	townRoot, rig, rigDir, beadsDir := setupPhasedRigForGuardTest(t)

	writeGuardTestPhasedProfile(t, townRoot, rig, "backend")
	v, ok, err := orchestrator.LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if _, err := orchestrator.SyncPlanningArtifacts(townRoot, rig, v.ForActivePhase(), true); err != nil {
		t.Fatal(err)
	}
	for _, rel := range v.ForActivePhase().RequiredFiles {
		writeGuardTestBackendFile(t, rigDir, rel)
	}
	closeAllOpenBeadsForGuardTest(t, townRoot, rig, beadsDir, rigDir, v.ForActivePhase())

	// Advance to the frontend phase and break the earlier-phase store.go.
	if err := orchestrator.SetRigActivePhase(townRoot, rig, "frontend"); err != nil {
		t.Fatal(err)
	}
	v, ok, err = orchestrator.LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(rigDir, "linkshelf", "internal", "store", "store.go")); err != nil {
		t.Fatal(err)
	}

	cmd := `cd phaserig/mayor/rig && cat > linkshelf/internal/store/store.go <<'EOF'
package store

// Store returns the stored value.
func Store() string {
	return "stored"
}
EOF`
	err = validateImplementationBeadFileWrite(cmd, townRoot, rig, "te-front", v, nil, "")
	if err == nil {
		t.Fatal("expected rewind guidance error for write to earlier-phase closed file")
	}
	if !strings.Contains(err.Error(), "rewound active phase frontend → backend") {
		t.Fatalf("expected rewind guidance in error, got: %v", err)
	}

	reloaded, ok, err := orchestrator.LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if got := reloaded.ActivePhaseID(); got != "backend" {
		t.Fatalf("active phase = %q, want backend", got)
	}
	if got := reloaded.RewoundFromPhaseIDField; got != "frontend" {
		t.Fatalf("rewound_from_phase_id = %q, want frontend", got)
	}

	open, err := orchestrator.ListOpenImplementBeads(townRoot, rig, reloaded.ForActivePhase())
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

func setupPhasedRigForGuardTest(t *testing.T) (townRoot, rig, rigDir, beadsDir string) {
	t.Helper()
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd not in PATH")
	}
	townRoot = t.TempDir()
	rig = "phaserig"
	rigDir = filepath.Join(townRoot, rig, "mayor", "rig")
	beadsDir = filepath.Join(townRoot, rig, ".beads")
	for _, d := range []string{rigDir, beadsDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	init := exec.Command("bd", "init")
	init.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
	init.Dir = rigDir
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("bd init: %v\n%s", err, out)
	}
	return townRoot, rig, rigDir, beadsDir
}

func writeGuardTestPhasedProfile(t *testing.T, townRoot, rig, activePhase string) {
	t.Helper()
	v := orchestrator.WorkflowValidation{
		LayoutRoot:          "linkshelf",
		BeadTitleContains:   "Implement ",
		MinPlanBytes:        100,
		MinArchitectureBytes: 200,
		QAVerifyCommand:      "cd linkshelf && go test ./...",
		DeliveryPhases: []orchestrator.DeliveryPhase{
			{
				ID:    "backend",
				Title: "Backend Implementation",
				RequiredFiles: []string{
					"linkshelf/go.mod",
					"linkshelf/internal/store/store.go",
					"linkshelf/internal/store/store_test.go",
				},
				QAVerifyCommand: "cd linkshelf && go test internal/store",
			},
			{
				ID:    "frontend",
				Title: "Frontend Implementation",
				RequiredFiles: []string{
					"linkshelf/web/index.html",
					"linkshelf/cmd/server/main.go",
				},
				QAVerifyCommand: "cd linkshelf && go test ./cmd/server",
			},
		},
		ActivePhaseIDField: activePhase,
	}
	v = orchestrator.FinalizeDeliveryPhases(v)
	if err := orchestrator.WriteRigWorkflowProfile(townRoot, rig, v, "test", "high"); err != nil {
		t.Fatal(err)
	}
}

func writeGuardTestBackendFile(t *testing.T, rigDir, rel string) {
	t.Helper()
	full := filepath.Join(rigDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatal(err)
	}
	var content string
	switch {
	case strings.HasSuffix(rel, "store_test.go"):
		content = `package store

import "testing"

func TestStore(t *testing.T) {
	if Store() == "" {
		t.Fatal("store returned empty")
	}
}
`
	case strings.HasSuffix(rel, "go.mod"):
		content = "module linkshelf\n\ngo 1.21\n"
	default:
		content = `package store

// Store returns the stored value.
func Store() string {
	return "stored"
}
`
	}
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func closeAllOpenBeadsForGuardTest(t *testing.T, townRoot, rig, beadsDir, workDir string, v orchestrator.WorkflowValidation) {
	t.Helper()
	open, err := orchestrator.ListOpenImplementBeads(townRoot, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range open {
		cmd := exec.Command("bd", "close", b.ID, "--reason=test")
		cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
		cmd.Dir = workDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("bd close %s: %v\n%s", b.ID, err, out)
		}
	}
}
