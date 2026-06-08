package orchestrator

import (
	"os"
	"os/exec"
	"sync"
	"testing"
)

// bdIntegrationMu serializes tests that spawn real bd/embedded-dolt against temp BEADS_DIR trees.
// Parallel bd init/list calls flake with "context canceled" under full package load.
var bdIntegrationMu sync.Mutex

func withBdIntegration(t *testing.T) {
	t.Helper()
	bdIntegrationMu.Lock()
	t.Cleanup(func() { bdIntegrationMu.Unlock() })
}

func initBeadsDBForTest(t *testing.T, rigDir, beadsDir string) {
	t.Helper()
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd not in PATH")
	}
	withBdIntegration(t)
	if err := os.MkdirAll(beadsDir, 0700); err != nil {
		t.Fatal(err)
	}
	init := exec.Command("bd", "init")
	init.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
	init.Dir = rigDir
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("bd init: %v\n%s", err, out)
	}
}
