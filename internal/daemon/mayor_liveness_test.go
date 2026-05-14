package daemon

import (
	"testing"

	"github.com/steveyegge/gastown/internal/session"
)

func TestMayorTownLogStaleGuardOnlyTmux(t *testing.T) {
	tmuxP := &session.TmuxProvider{}
	if !mayorTownLogStaleGuardApplies(tmuxP) {
		t.Fatal("expected town.log staleness guard for tmux mayor transport")
	}
	var natP *session.NatsProvider
	if mayorTownLogStaleGuardApplies(natP) {
		t.Fatal("NATS mayor must not use town.log staleness — false zombie kills")
	}
}
