package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/gastown/internal/session"
)

func TestRigPipelineRoles_includesArchitectQAPolecat(t *testing.T) {
	roles := rigPipelineRoles("testgt3")
	if len(roles) != 3 {
		t.Fatalf("got %d roles", len(roles))
	}
	seen := map[string]bool{}
	for _, r := range roles {
		seen[r.label] = true
		if r.sessionID == "" {
			t.Fatalf("empty session for %s", r.label)
		}
	}
	for _, want := range []string{"Architect", "QA", "Polecat"} {
		if !seen[want] {
			t.Fatalf("missing label %s", want)
		}
	}
	wantArch := session.ArchitectSessionName(session.PrefixFor("testgt3"), "testgt3")
	if roles[0].sessionID != wantArch {
		t.Fatalf("architect session = %q want %q", roles[0].sessionID, wantArch)
	}
}

func TestPathWithinDirectory(t *testing.T) {
	tests := []struct {
		directory string
		path      string
		want      bool
	}{
		{"/town", "/town", true},
		{"/town", "/town/rig/.beads/dolt", true},
		{"/town/.dolt-data", "/town/.dolt-data/testgt4", true},
		{"/town/.dolt-data", "/town/.dolt-data-old/testgt4", false},
		{"/town", "/town-old/rig", false},
	}
	for _, tt := range tests {
		got := pathWithinDirectory(filepath.Clean(tt.directory), filepath.Clean(tt.path))
		if got != tt.want {
			t.Errorf("pathWithinDirectory(%q, %q) = %t, want %t", tt.directory, tt.path, got, tt.want)
		}
	}
}

func TestIsProcessRunning_CurrentProcess(t *testing.T) {
	if !isProcessRunning(os.Getpid()) {
		t.Error("current process should be detected as running")
	}
}

func TestIsProcessRunning_InvalidPID(t *testing.T) {
	if isProcessRunning(99999999) {
		t.Error("invalid PID should not be detected as running")
	}
}

func TestIsProcessRunning_MaxPID(t *testing.T) {
	if isProcessRunning(2147483647) {
		t.Error("max PID should not be running")
	}
}
