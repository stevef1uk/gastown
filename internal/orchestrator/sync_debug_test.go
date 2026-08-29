package orchestrator

import (
	"testing"
)

func TestManualSync(t *testing.T) {
	synced, err := SyncRigWorkflowProfileFromArchitecture("/home/stevef/gt", "testgt3")
	t.Logf("synced=%v err=%v", synced, err)
}
