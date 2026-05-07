// Package session provides polecat session lifecycle management.
package session

import (
	"context"
	"fmt"
	"time"

	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/events"
)

// TownSession represents a town-level tmux session.
type TownSession struct {
	Name      string // Display name (e.g., "Mayor")
	SessionID string // Tmux session ID (e.g., "hq-mayor")
}

// TownSessions returns the list of town-level sessions in shutdown order.
// Order matters: Boot (Deacon's watchdog) must be stopped before Deacon,
// otherwise Boot will try to restart Deacon.
func TownSessions() []TownSession {
	return []TownSession{
		{"Mayor", MayorSessionName()},
		{"Planner", PlannerSessionName()},
		{"Mechanic", MechanicSessionName()},
		{"Boot", BootSessionName()},
		{"Deacon", DeaconSessionName()},
	}
}

// StopTownSession stops a single town-level session.
// If force is true, skips graceful shutdown (Ctrl-C) and kills immediately.
// Returns true if the session was running and stopped, false if not running.
func StopTownSession(p Provider, ts TownSession, force bool) (bool, error) {
	ctx := context.Background()
	running, err := p.Exists(ctx, ts.SessionID)
	if err != nil {
		return false, err
	}
	if !running {
		return false, nil
	}

	return stopTownSessionInternal(p, ts, force)
}

// stopTownSessionInternal performs the actual session stop.
func stopTownSessionInternal(p Provider, ts TownSession, force bool) (bool, error) {
	ctx := context.Background()
	// Try graceful shutdown first (unless forced)
	if !force {
		_ = p.Inject(ctx, ts.SessionID, "C-c")
		WaitForSessionExit(p, ts.SessionID, constants.GracefulShutdownTimeout)
	}

	// Log pre-death event for crash investigation (before killing)
	reason := "user shutdown"
	if force {
		reason = "forced shutdown"
	}
	_ = events.LogFeed(events.TypeSessionDeath, ts.SessionID,
		events.SessionDeathPayload(ts.SessionID, ts.Name, reason, "gt down"))

	// Kill the session via provider.
	if err := p.Stop(ctx, ts.SessionID, true); err != nil {
		return false, fmt.Errorf("killing %s session: %w", ts.Name, err)
	}

	return true, nil
}

// WaitForSessionExit polls for a session's process to exit within the given timeout.
// Returns true if the process exited on its own, false if the timeout was reached.
// This allows graceful shutdown (e.g., after Ctrl-C) to actually complete before
// falling through to forceful termination.
func WaitForSessionExit(p Provider, sessionID string, timeout time.Duration) bool {
	ctx := context.Background()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		running, err := p.Exists(ctx, sessionID)
		if err != nil || !running {
			return true
		}
		time.Sleep(constants.PollInterval)
	}
	return false
}
