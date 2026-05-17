package orchestrator

import (
	"strings"
	"testing"
)

func TestValidateSummaryBeadIDs_wrongPrefix(t *testing.T) {
	known := map[string]bool{"de-5d0": true, "de-b3w": true}
	err := ValidateSummaryBeadIDs("reopen te-5d0 te-b3w for implementation", known, "de")
	if err == nil || !strings.Contains(err.Error(), "te-5d0") {
		t.Fatalf("expected wrong-prefix error, got %v", err)
	}
}

func TestValidateSummaryBeadIDs_knownIDs(t *testing.T) {
	known := map[string]bool{"de-5d0": true}
	if err := ValidateSummaryBeadIDs("reopen de-5d0; stub main.py", known, "de"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateSummaryBeadIDs_unknownID(t *testing.T) {
	known := map[string]bool{"de-5d0": true}
	err := ValidateSummaryBeadIDs("reopen de-zzzz not in list", known, "de")
	if err == nil {
		t.Fatal("expected unknown id error")
	}
}

func TestIsAgentIdentityBeadID(t *testing.T) {
	for _, id := range []string{"xx-mockrig-architect", "xx-mockrig-qa", "xx-mockrig-crew-steve"} {
		if !isAgentIdentityBeadID(id) {
			t.Fatalf("expected agent identity: %s", id)
		}
	}
	if isAgentIdentityBeadID("xx-abc123") {
		t.Fatal("task bead should not match")
	}
}

func TestValidateSummaryBeadIDs_ignoresAgentIdentity(t *testing.T) {
	known := map[string]bool{"xx-5d0": true}
	if err := ValidateSummaryBeadIDs("also xx-mockrig-architect", known, "xx"); err != nil {
		t.Fatalf("agent identity beads should be ignored: %v", err)
	}
}

func TestNormalizePytestCommand(t *testing.T) {
	in := "cd myapp/pkg && pytest -q"
	want := "cd myapp/pkg && python3 -m pytest -q"
	if got := NormalizePytestCommand(in); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestNormalizePipCommand(t *testing.T) {
	in := "pip install -r requirements.txt"
	want := "python3 -m pip install -r requirements.txt"
	if got := NormalizePipCommand(in); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
