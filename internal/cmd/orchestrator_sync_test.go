package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOrchestratorSyncExplicitTownRootOutsideWorkspace(t *testing.T) {
	t.Chdir(t.TempDir())
	townRoot := t.TempDir()
	oldRoot, oldUpdate := orchestratorSyncTownRoot, orchestratorSyncUpdate
	t.Cleanup(func() {
		orchestratorSyncTownRoot, orchestratorSyncUpdate = oldRoot, oldUpdate
	})
	orchestratorSyncTownRoot, orchestratorSyncUpdate = townRoot, true

	if err := runOrchestratorSync(orchestratorSyncCmd, nil); err != nil {
		t.Fatalf("sync with explicit town root outside workspace: %v", err)
	}
	for _, path := range []string{
		"orchestrator/templates/rig-flow.yaml",
		"orchestrator/prompts/rig-flow/test_review.md",
	} {
		if _, err := os.Stat(filepath.Join(townRoot, filepath.FromSlash(path))); err != nil {
			t.Errorf("synced asset %s: %v", path, err)
		}
	}
}
