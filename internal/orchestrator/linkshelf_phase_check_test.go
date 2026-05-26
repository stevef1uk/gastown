//go:build integration

package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLinkshelfPhaseVerifyNow(t *testing.T) {
	townRoot := "/home/stevef/gt"
	rig := "testgt3"
	if _, err := os.Stat(filepath.Join(townRoot, rig, "mayor", "rig", "linkshelf")); err != nil {
		t.Skip("testgt3 not present")
	}
	v, ok, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatal(err, ok)
	}
	open, _ := ListImplementBeadsOpenOrInProgress(townRoot, rig, v)
	t.Logf("open/in_progress implement beads: %d", len(open))
	if err := ImplementationPhaseVerifyOK(townRoot, rig, v); err != nil {
		t.Logf("ImplementationPhaseVerifyOK: %v", err)
	} else {
		t.Log("ImplementationPhaseVerifyOK: OK")
	}
	t.Logf("ImplementationQueueGreen=%v", ImplementationQueueGreen(townRoot, rig, v))
}
