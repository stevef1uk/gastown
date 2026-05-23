package orchestrator

import (
	"strings"
	"testing"
)

func TestRigFlowStaticURLConstants(t *testing.T) {
	t.Parallel()
	if !strings.Contains(RigFlowStaticURLContractGuidance, "architecture.md") {
		t.Fatal("guidance must reference architecture")
	}
	if strings.Contains(RigFlowStaticURLContractGuidance, "often `/app.js`") {
		t.Fatal("guidance must not contradict static prefix story")
	}
}
