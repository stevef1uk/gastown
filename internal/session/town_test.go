package session

import (
	"testing"
)

func TestTownSessions(t *testing.T) {
	sessions := TownSessions()

	if len(sessions) != 5 {
		t.Errorf("TownSessions() returned %d sessions, want 5", len(sessions))
	}

	// Verify order is correct (Mayor, Setup, Mechanic, Boot, Deacon)
	// Planner is now rig-level and no longer in town sessions
	expectedOrder := []string{"Mayor", "Setup", "Mechanic", "Boot", "Deacon"}
	for i, s := range sessions {
		if s.Name != expectedOrder[i] {
			t.Errorf("TownSessions()[%d].Name = %q, want %q", i, s.Name, expectedOrder[i])
		}
		if s.SessionID == "" {
			t.Errorf("TownSessions()[%d].SessionID should not be empty", i)
		}
	}
}

func TestTownSessions_SessionIDFormats(t *testing.T) {
	sessions := TownSessions()

	for _, s := range sessions {
		if s.SessionID == "" {
			t.Errorf("TownSession %q has empty SessionID", s.Name)
		}
		// Session IDs should follow a pattern
		if len(s.SessionID) < 4 {
			t.Errorf("TownSession %q SessionID %q is too short", s.Name, s.SessionID)
		}
	}
}

func TestTownSession_StructFields(t *testing.T) {
	ts := TownSession{
		Name:      "Test",
		SessionID: "test-session",
	}

	if ts.Name != "Test" {
		t.Errorf("TownSession.Name = %q, want %q", ts.Name, "Test")
	}
	if ts.SessionID != "test-session" {
		t.Errorf("TownSession.SessionID = %q, want %q", ts.SessionID, "test-session")
	}
}

func TestTownSession_CanBeCreated(t *testing.T) {
	// Test that TownSession can be created with any values
	tests := []struct {
		name      string
		sessionID string
	}{
		{"Mayor", "hq-mayor"},
		{"Boot", "hq-boot"},
		{"Custom", "custom-session"},
	}

	for _, tt := range tests {
		ts := TownSession{
			Name:      tt.name,
			SessionID: tt.sessionID,
		}
		if ts.Name != tt.name {
			t.Errorf("TownSession.Name = %q, want %q", ts.Name, tt.name)
		}
		if ts.SessionID != tt.sessionID {
			t.Errorf("TownSession.SessionID = %q, want %q", ts.SessionID, tt.sessionID)
		}
	}
}

func TestTownSession_ShutdownOrder(t *testing.T) {
	// Verify shutdown order. Boot (Deacon's watchdog) must stop before Deacon.
	expectedOrder := []string{"Mayor", "Setup", "Mechanic", "Boot", "Deacon"}
	sessions := TownSessions()

	if len(sessions) != len(expectedOrder) {
		t.Fatalf("TownSessions() returned %d sessions, want %d", len(sessions), len(expectedOrder))
	}
	for i, want := range expectedOrder {
		if sessions[i].Name != want {
			t.Errorf("TownSessions()[%d].Name = %q, want %q", i, sessions[i].Name, want)
		}
	}
}
