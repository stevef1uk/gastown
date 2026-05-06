package cmd

import (
	"testing"

	"github.com/steveyegge/gastown/internal/session"
)

func setupDndTestRegistry(t *testing.T) {
	t.Helper()
	reg := session.NewPrefixRegistry()
	reg.Register("gt", "gastown")
	reg.Register("bd", "beads")
	old := session.DefaultRegistry()
	session.SetDefaultRegistry(reg)
	t.Cleanup(func() { session.SetDefaultRegistry(old) })
}

func TestAddressToAgentBeadID(t *testing.T) {
	setupDndTestRegistry(t)
	tests := []struct {
		address  string
		expected string
	}{
		// Mayor and deacon use hq- prefix (town-level)
		{"mayor", "hq-mayor"},
		{"deacon", "hq-deacon"},
		{"gastown/witness", "hq-gt-gastown-witness"},
		{"gastown/refinery", "hq-gt-gastown-refinery"},
		{"gastown/alpha", "gt-alpha"},
		{"gastown/crew/max", "gt-crew-max"},
		{"gastown/polecats/alpha", "gt-alpha"},
		{"beads/polecats/beta", "bd-beta"},
		{"beads/witness", "hq-bd-beads-witness"},
		{"beads/beta", "bd-beta"},
		// Invalid addresses should return empty string
		{"invalid", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.address, func(t *testing.T) {
			got := addressToAgentBeadID(tt.address)
			if got != tt.expected {
				t.Errorf("addressToAgentBeadID(%q) = %q, want %q", tt.address, got, tt.expected)
			}
		})
	}
}
