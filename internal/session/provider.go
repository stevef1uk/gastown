package session

import (
	"context"
)

// Provider abstracts session management operations.
// This allows Gas Town to support multiple transports (Tmux, NATS, etc.)
// using a consistent interface.
type Provider interface {
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

	// SetRemainOnExit sets the remain-on-exit behavior for the session.
	SetRemainOnExit(ctx context.Context, sessionID string, enabled bool) error

	// Configure applying provider-specific configuration (like theming for tmux).
	Configure(ctx context.Context, sessionID string, cfg any) error
}
