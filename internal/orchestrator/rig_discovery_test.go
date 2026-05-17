package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOrchestratorAgentID(t *testing.T) {
	if got := OrchestratorAgentID("polecat", "mockrig"); got != "mockrig/polecat" {
		t.Fatalf("OrchestratorAgentID(polecat, mockrig) = %q, want mockrig/polecat", got)
	}
	if got := OrchestratorAgentID("polecat", ""); got != "polecat" {
		t.Fatalf("OrchestratorAgentID(polecat, empty) = %q, want polecat", got)
	}
	if got := OrchestratorAgentID("planner", ""); got != "planner" {
		t.Fatalf("OrchestratorAgentID(planner, empty) = %q, want planner", got)
	}
}

func TestDiscoverTownPolecatRig_precedence(t *testing.T) {
	town := t.TempDir()
	if got := DiscoverTownPolecatRig(town, "from-env", "from-identity"); got != "from-env" {
		t.Fatalf("env precedence: got %q", got)
	}
	if got := DiscoverTownPolecatRig(town, "", "from-identity"); got != "from-identity" {
		t.Fatalf("identity precedence: got %q", got)
	}
}

func TestDiscoverTownPolecatRig_singleRigFromRigsJSON(t *testing.T) {
	town := t.TempDir()
	if err := os.MkdirAll(filepath.Join(town, "mayor"), 0755); err != nil {
		t.Fatal(err)
	}
	rigsJSON := `{"rigs":{"mockrig":{"name":"mockrig"}}}`
	if err := os.WriteFile(filepath.Join(town, "mayor", "rigs.json"), []byte(rigsJSON), 0644); err != nil {
		t.Fatal(err)
	}
	if got := DiscoverTownPolecatRig(town, "", ""); got != "mockrig" {
		t.Fatalf("single rig from rigs.json: got %q, want mockrig", got)
	}
}

func TestDiscoverTownPolecatRig_activeWorkflow(t *testing.T) {
	town := t.TempDir()
	orchDir := filepath.Join(town, "orchestrator")
	if err := os.MkdirAll(orchDir, 0755); err != nil {
		t.Fatal(err)
	}
	snap := instancesSnapshot{
		Instances: []*WorkflowInstance{
			{
				ID:         "wf-1",
				TemplateID: "rig-flow",
				Status:     "running",
				Variables:  map[string]string{"rig": "mockrig"},
			},
		},
	}
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orchDir, instancesFileName), data, 0644); err != nil {
		t.Fatal(err)
	}
	if got := DiscoverTownPolecatRig(town, "", ""); got != "mockrig" {
		t.Fatalf("active workflow rig: got %q, want mockrig", got)
	}
}
