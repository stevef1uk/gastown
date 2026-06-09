package daemon

import (
	"github.com/steveyegge/gastown/internal/orchestrator"
	"github.com/steveyegge/gastown/internal/session"
)

func init() {
	session.NatsAutoRespawnAllowed = orchestrator.SessionShouldAutoRespawn
}
