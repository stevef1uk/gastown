package session

import (
	"strings"
	"testing"
)

func TestRigPipelineSessionIDs_includesRoles(t *testing.T) {
	ids := RigPipelineSessionIDs("testgt3")
	if len(ids) != 3 {
		t.Fatalf("got %v", ids)
	}
	for _, role := range []string{"architect", "qa", "polecat"} {
		found := false
		for _, id := range ids {
			if strings.Contains(id, "testgt3") && strings.Contains(id, role) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing %s in %v", role, ids)
		}
	}
}
