package session

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/steveyegge/gastown/internal/tmux"
)

// SessionInfo holds provider-agnostic information about a session.
type SessionInfo struct {
	Name         string
	Windows      int
	Created      string
	Attached     bool
	Activity     string // Last activity time
	LastAttached string // Last time the session was attached
}

// WaitForIdle waits for a session to become idle using provider-specific detection.
// For tmux this uses prompt-based detection; for NATS it returns an error.
func WaitForIdle(p Provider, sessionID string, timeout time.Duration) error {
	if tp, ok := p.(*TmuxProvider); ok {
		return tp.WaitForIdle(sessionID, timeout)
	}
	return fmt.Errorf("idle detection not supported for this provider")
}

// GetDefaultProvider returns the default session provider based on environment.
func GetDefaultProvider(townRoot string) Provider {
	// If GT_SESSION_TRANSPORT is set to "nats", use NatsProvider
	if os.Getenv("GT_SESSION_TRANSPORT") == "nats" {
		p, err := NewNatsProvider(townRoot, "")
		if err == nil {
			return p
		}
	}

	// Default to TmuxProvider for now
	return NewTmuxProvider(tmux.NewTmux())
}

// Provider abstracts session management operations.
// This allows Gas Town to support multiple transports (Tmux, NATS, etc.)
// using a consistent interface.
type Provider interface {
	// IsAvailable returns true if the session provider is ready for use.
	IsAvailable() bool

	// Start starts a new session with the given command and environment.
	Start(ctx context.Context, sessionID, workDir, command string, env map[string]string) error

	// Stop stops an existing session with optional graceful shutdown.
	Stop(ctx context.Context, sessionID string, graceful bool) error

	// Exists returns true if the session is currently running.
	Exists(ctx context.Context, sessionID string) (bool, error)

	// List returns a list of all active session IDs managed by this provider.
	List(ctx context.Context) ([]string, error)

	// Inject sends input (e.g. keystrokes or commands) to an active session.
	Inject(ctx context.Context, sessionID string, data string) error

	// GetEnvironment returns the environment variables for a running session.
	GetEnvironment(ctx context.Context, sessionID string) (map[string]string, error)

	// SetEnvironment sets an environment variable in a running session.
	SetEnvironment(ctx context.Context, sessionID, key, value string) error

	// SetGlobalEnvironment sets an environment variable for all future sessions.
	SetGlobalEnvironment(key, value string) error

	// UnsetGlobalEnvironment removes an environment variable for all future sessions.
	UnsetGlobalEnvironment(key string) error

	// SetRemainOnExit sets the remain-on-exit behavior for the session.
	SetRemainOnExit(ctx context.Context, sessionID string, enabled bool) error

	// Configure applying provider-specific configuration (like theming for tmux).
	Configure(ctx context.Context, sessionID string, cfg any) error

	// EnsureSessionFresh ensures that a session is fresh (kills existing if needed).
	EnsureSessionFresh(ctx context.Context, sessionID, workDir, command string, env map[string]string) error

	// IsAgentRunning checks if the agent process is actively running in the session.
	IsAgentRunning(ctx context.Context, id string) (bool, error)

	// CleanupOrphanedSessions scans for zombie sessions and kills them.
	CleanupOrphanedSessions(isGTSession func(string) bool) (int, error)

	// StopAllSessions terminates all sessions managed by this provider.
	StopAllSessions(ctx context.Context) error

	// GetMainPID returns the PID of the main process in the session.
	GetMainPID(ctx context.Context, sessionID string) (string, error)

	// --- Interactive Methods (for monitoring and control) ---

	// IsIdle returns true if the session is not actively processing input.
	IsIdle(ctx context.Context, sessionID string) (bool, error)

	// CapturePane returns the last N lines of output from the session.
	CapturePane(ctx context.Context, sessionID string, lines int) (string, error)

	// AttachSession attaches the current terminal to the session (blocking).
	// For tmux this is tmux attach; for NATS this tails the session log.
	AttachSession(ctx context.Context, sessionID string) error

	// SendKeysDebounced sends input to the session with debouncing.
	SendKeysDebounced(ctx context.Context, sessionID string, keys string, debounceMs int) error

	// GetSessionInfo returns provider-agnostic session metadata.
	GetSessionInfo(ctx context.Context, sessionID string) (*SessionInfo, error)
}
