package doctor

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Regression: Fix must not add status:docked to new rig identity beads (gt doctor --fix after
// reset-town-and-rig.sh was killing te-*-polecat via isRigOperational).
func TestRigConfigSyncCheck_FixSourceDoesNotDockNewRigs(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	srcBytes, err := os.ReadFile(filepath.Join(filepath.Dir(file), "rig_config_sync_check.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(srcBytes)
	if strings.Contains(src, "status:docked") {
		t.Fatal("rig_config_sync_check Fix must not add status:docked to new rig beads")
	}
	if !strings.Contains(src, "EnsureRigBead") {
		t.Fatal("rig_config_sync_check Fix should use EnsureRigBead for identity beads")
	}
}
