
package session

import (
	"testing"
	"fmt"
)

func TestParseArchitect(t *testing.T) {
	DefaultRegistry().Register("te", "testgt2")
	id, err := ParseSessionName("te-testgt2-architect")
	if err != nil {
		t.Fatalf("Error: %v", err)
	}
	fmt.Printf("Role: %s, Rig: %s, Name: %s, Prefix: %s\n", id.Role, id.Rig, id.Name, id.Prefix)
}
