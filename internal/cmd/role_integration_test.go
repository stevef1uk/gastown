package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/workspace"
)

// TestRoleCommandsRegistration verifies that the new role commands are registered.
func TestRoleCommandsRegistration(t *testing.T) {
	roles := []struct {
		name string
		cmd  *cobra.Command
	}{
		{"planner", plannerCmd},
		{"architect", architectCmd},
		{"qa", qaCmd},
	}

	for _, r := range roles {
		t.Run(r.name, func(t *testing.T) {
			if r.cmd == nil {
				t.Errorf("command for role %s is not initialized", r.name)
			}
			if r.cmd.Use != r.name {
				t.Errorf("expected command Use to be %s, got %s", r.name, r.cmd.Use)
			}
			
			// Verify subcommands exist
			subcommands := []string{"start", "stop", "attach"}
			for _, sub := range subcommands {
				found := false
				for _, sc := range r.cmd.Commands() {
					if sc.Name() == sub {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("role %s missing subcommand %s", r.name, sub)
				}
			}
		})
	}
}

// setupMockTown creates a temporary town structure for testing.
func setupMockTown(t *testing.T) string {
	townRoot := t.TempDir()
	
	// Create mayor/town.json
	mayorDir := filepath.Join(townRoot, "mayor")
	if err := os.MkdirAll(mayorDir, 0755); err != nil {
		t.Fatal(err)
	}
	
	townConfig := `{"name": "test-town"}`
	if err := os.WriteFile(filepath.Join(mayorDir, "town.json"), []byte(townConfig), 0644); err != nil {
		t.Fatal(err)
	}
	
	// Create rigs.json
	rigsConfig := `{"rigs": {"testrig": {"path": "testrig", "beads": {"prefix": "tr"}}}}`
	if err := os.WriteFile(filepath.Join(mayorDir, "rigs.json"), []byte(rigsConfig), 0644); err != nil {
		t.Fatal(err)
	}
	
	// Create rig directory
	rigDir := filepath.Join(townRoot, "testrig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	
	return townRoot
}

func TestPlannerStartCommand(t *testing.T) {
	townRoot := setupMockTown(t)
	origCwd, _ := os.Getwd()
	defer os.Chdir(origCwd)
	os.Chdir(townRoot)
	
	// We can't easily test the actual execution because it spawns tmux sessions,
	// but we can test that it correctly identifies the workspace.
	root, err := workspace.FindFromCwdOrError()
	if err != nil {
		t.Fatalf("failed to find workspace in mock town: %v", err)
	}
	if root != townRoot {
		t.Errorf("workspace root = %q, want %q", root, townRoot)
	}
}
