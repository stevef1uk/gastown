package session

import (
	"context"

	"github.com/steveyegge/gastown/internal/tmux"
)

// TmuxProvider implements the Provider interface using tmux.
type TmuxProvider struct {
	t *tmux.Tmux
}

// NewTmuxProvider creates a new TmuxProvider.
func NewTmuxProvider(t *tmux.Tmux) *TmuxProvider {
	return &TmuxProvider{t: t}
}

func (p *TmuxProvider) Start(ctx context.Context, sessionID, workDir, command string, env map[string]string) error {
	return p.t.NewSessionWithCommandAndEnv(sessionID, workDir, command, env)
}

func (p *TmuxProvider) Stop(ctx context.Context, sessionID string, graceful bool) error {
	if graceful {
		// StopSession handles graceful Ctrl-C
		return StopSession(p.t, sessionID, true)
	}
	return p.t.KillSessionWithProcesses(sessionID)
}

func (p *TmuxProvider) Exists(ctx context.Context, sessionID string) (bool, error) {
	return p.t.HasSession(sessionID)
}

func (p *TmuxProvider) List(ctx context.Context) ([]string, error) {
	return p.t.ListSessions()
}

func (p *TmuxProvider) Inject(ctx context.Context, sessionID string, data string) error {
	return p.t.SendKeysRaw(sessionID, data)
}

func (p *TmuxProvider) GetEnvironment(ctx context.Context, sessionID string) (map[string]string, error) {
	// Tmux doesn't expose full env, return empty map for now
	return make(map[string]string), nil
}

func (p *TmuxProvider) SetEnvironment(ctx context.Context, sessionID, key, value string) error {
	return p.t.SetEnvironment(sessionID, key, value)
}

func (p *TmuxProvider) SetRemainOnExit(ctx context.Context, sessionID string, enabled bool) error {
	return p.t.SetRemainOnExit(sessionID, enabled)
}

func (p *TmuxProvider) Configure(ctx context.Context, sessionID string, cfg any) error {
	// If cfg is a tmux.Theme, apply it.
	if theme, ok := cfg.(*tmux.Theme); ok {
		// Note: ConfigureGasTownSession requires rig/agent/role names which are not in the Provider interface yet.
		// For now we just use empty strings or skip if not available.
		return p.t.ConfigureGasTownSession(sessionID, theme, "", "", "")
	}
	return nil
}
