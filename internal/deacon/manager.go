package deacon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/tmux"
)

// Common errors
var (
	ErrNotRunning     = errors.New("deacon not running")
	ErrAlreadyRunning = errors.New("deacon already running")
)

// Manager handles deacon lifecycle operations.
type Manager struct {
	townRoot string
	sp       session.Provider
}

// NewManager creates a new deacon manager for a town.
func NewManager(townRoot string) *Manager {
	return NewManagerWithProvider(townRoot, session.GetDefaultProvider(townRoot))
}

// NewManagerWithProvider creates a new deacon manager with a specific provider.
func NewManagerWithProvider(townRoot string, sp session.Provider) *Manager {
	return &Manager{
		townRoot: townRoot,
		sp:       sp,
	}
}

// SessionName returns the tmux session name for the deacon.
// This is a package-level function for convenience.
func SessionName() string {
	return session.DeaconSessionName()
}

// SessionName returns the tmux session name for the deacon.
func (m *Manager) SessionName() string {
	return SessionName()
}

// deaconDir returns the working directory for the deacon.
func (m *Manager) deaconDir() string {
	return filepath.Join(m.townRoot, "deacon")
}

// Start starts the deacon session.
// agentOverride allows specifying an alternate agent alias (e.g., for testing).
// Restarts are handled by daemon via ensureDeaconRunning on each heartbeat.
func (m *Manager) Start(agentOverride string, orchestrated bool) error {
	sp := m.sp
	sessionID := m.SessionName()
	ctx := context.Background()

	// Check for a live agent (not just the nats-wrapper PID).
	if alive, _ := sp.IsAgentRunning(ctx, sessionID); alive {
		return ErrAlreadyRunning
	}
	if exists, _ := sp.Exists(ctx, sessionID); exists {
		_ = sp.Stop(ctx, sessionID, false) // zombie wrapper without gt-agent
	}

	// Ensure deacon directory exists
	deaconDir := m.deaconDir()
	if err := os.MkdirAll(deaconDir, 0755); err != nil {
		return fmt.Errorf("creating deacon directory: %w", err)
	}

	// Use unified session lifecycle for config -> settings -> command -> create -> env -> theme -> wait.
	var theme *tmux.Theme
	if _, isTmux := sp.(*session.TmuxProvider); isTmux {
		theme = tmux.ResolveSessionTheme(m.townRoot, "", "deacon", "")
	}

	_, err := session.StartSession(ctx, sp, &session.SessionConfig{
		SessionID:    sessionID,
		WorkDir:      deaconDir,
		Role:         "deacon",
		TownRoot:     m.townRoot,
		Orchestrated: orchestrated,
		Beacon: session.BeaconConfig{
			Recipient: "deacon",
			Sender:    "daemon",
			Topic:     "patrol",
		},
		AgentOverride: agentOverride,
		Theme:         theme,
		WaitForAgent:  true,
		WaitFatal:     true,
		AutoRespawn:   true,
		AcceptBypass:  true,
	})
	if err != nil {
		return err
	}

	// Seed a fresh heartbeat so the daemon does not treat a newly started Deacon as
	// stuck based on a pre-restart heartbeat.json (stuck-agent-dog false positive).
	_ = WriteHeartbeat(m.townRoot, &Heartbeat{
		Timestamp:  time.Now().UTC(),
		Cycle:      0,
		LastAction: "starting",
	})

	time.Sleep(constants.ShutdownNotifyDelay)

	return nil
}

// Stop stops the deacon session.
func (m *Manager) Stop() error {
	sp := m.sp
	sessionID := m.SessionName()
	ctx := context.Background()

	// Check if session exists
	running, err := sp.Exists(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("checking session: %w", err)
	}
	if !running {
		return ErrNotRunning
	}

	// Stop the session via provider
	if err := sp.Stop(ctx, sessionID, false); err != nil {
		return fmt.Errorf("stopping session: %w", err)
	}

	return nil
}

// IsRunning checks if the deacon agent process is active (not just the wrapper).
func (m *Manager) IsRunning() (bool, error) {
	return m.sp.IsAgentRunning(context.Background(), m.SessionName())
}

// Status returns information about the deacon session.
func (m *Manager) Status() (*tmux.SessionInfo, error) {
	sp := m.sp
	sessionID := m.SessionName()
	ctx := context.Background()

	running, err := sp.IsAgentRunning(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("checking session: %w", err)
	}
	if !running {
		return nil, ErrNotRunning
	}

	// For tmux, get detailed session info. For NATS, return minimal info.
	if tp, ok := sp.(*session.TmuxProvider); ok {
		return tp.Tmux().GetSessionInfo(sessionID)
	}

	return &tmux.SessionInfo{Name: sessionID}, nil
}
