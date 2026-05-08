package session

import (
	"context"
	"time"
	"fmt"

	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/tmux"
)

// TmuxProvider implements the Provider interface using tmux.
type TmuxProvider struct {
	t        *tmux.Tmux
	townRoot string
}

// NewTmuxProvider creates a new TmuxProvider.
func NewTmuxProvider(t *tmux.Tmux, townRoot string) *TmuxProvider {
	return &TmuxProvider{t: t, townRoot: townRoot}
}

// Tmux returns the underlying *tmux.Tmux. Used for tmux-specific operations
// (theming, hooks, prompt detection) that have no NATS equivalent.
func (p *TmuxProvider) Tmux() *tmux.Tmux {
	return p.t
}

func (p *TmuxProvider) IsAvailable() bool {
	return p.t.IsAvailable()
}

func (p *TmuxProvider) Start(ctx context.Context, sessionID, workDir, command string, env map[string]string) error {
	return p.t.NewSessionWithCommandAndEnv(sessionID, workDir, command, env)
}

func (p *TmuxProvider) Stop(ctx context.Context, sessionID string, graceful bool) error {
	// Note: graceful shutdown (Ctrl-C + wait) is handled by StopSession in
	// lifecycle.go BEFORE calling this method. This method should only do
	// the final session kill to avoid infinite recursion.
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

func (p *TmuxProvider) NudgeSession(ctx context.Context, sessionID, message, sender string) error {
	prefixed := fmt.Sprintf("[from %s] %s", sender, message)
	opts := tmux.NudgeOpts{TownRoot: p.townRoot}

	// Auto-detect SkipEscape based on agent preset
	if agentName, err := p.t.GetEnvironment(sessionID, "GT_AGENT"); err == nil && agentName != "" {
		if preset := config.GetAgentPresetByName(agentName); preset != nil && preset.EscapeCancelsRequest {
			opts.SkipEscape = true
		}
	}

	return p.t.NudgeSessionWithOpts(sessionID, prefixed, opts)
}

func (p *TmuxProvider) GetEnvironment(ctx context.Context, sessionID string) (map[string]string, error) {
	// Tmux doesn't expose full env, return empty map for now
	return make(map[string]string), nil
}

func (p *TmuxProvider) SetEnvironment(ctx context.Context, sessionID, key, value string) error {
	return p.t.SetEnvironment(sessionID, key, value)
}

func (p *TmuxProvider) SetGlobalEnvironment(key, value string) error {
	return p.t.SetGlobalEnvironment(key, value)
}

func (p *TmuxProvider) UnsetGlobalEnvironment(key string) error {
	return p.t.UnsetGlobalEnvironment(key)
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

func (p *TmuxProvider) IsAgentRunning(ctx context.Context, id string) (bool, error) {
	return p.t.IsAgentAlive(id), nil
}

func (p *TmuxProvider) CleanupOrphanedSessions(isGTSession func(string) bool) (int, error) {
	return p.t.CleanupOrphanedSessions(isGTSession)
}

func (p *TmuxProvider) EnsureSessionFresh(ctx context.Context, sessionID, workDir, command string, env map[string]string) error {
	return p.t.EnsureSessionFreshWithCommand(sessionID, workDir, command)
}

func (p *TmuxProvider) StopAllSessions(ctx context.Context) error {
	return p.t.KillServer()
}

func (p *TmuxProvider) GetSessionInfo(ctx context.Context, sessionID string) (*SessionInfo, error) {
	ti, err := p.t.GetSessionInfo(sessionID)
	if err != nil {
		return nil, err
	}
	// Convert tmux-specific SessionInfo to provider-agnostic SessionInfo
	return &SessionInfo{
		Name:         ti.Name,
		Windows:      ti.Windows,
		Created:      ti.Created,
		Attached:     ti.Attached,
		Activity:     ti.Activity,
		LastAttached: ti.LastAttached,
	}, nil
}

func (p *TmuxProvider) GetMainPID(ctx context.Context, sessionID string) (string, error) {
	return p.t.GetPanePID(sessionID)
}

func (p *TmuxProvider) GetServerPID(ctx context.Context) (int, error) {
	return p.t.ServerPID(), nil
}

func (p *TmuxProvider) GetWorkDir(ctx context.Context, sessionID string) (string, error) {
	return p.t.GetPaneWorkDir(sessionID)
}

// IsIdle returns true if the tmux session is idle.
func (p *TmuxProvider) IsIdle(ctx context.Context, sessionID string) (bool, error) {
	return p.t.IsIdle(sessionID), nil
}

// CapturePane returns the last N lines from the tmux pane.
func (p *TmuxProvider) CapturePane(ctx context.Context, sessionID string, lines int) (string, error) {
	return p.t.CapturePane(sessionID, lines)
}

// AttachSession attaches to the tmux session.
func (p *TmuxProvider) AttachSession(ctx context.Context, sessionID string) error {
	return p.t.AttachSession(sessionID)
}

// SendKeysDebounced sends keys to the tmux session with debouncing.
func (p *TmuxProvider) SendKeysDebounced(ctx context.Context, sessionID string, keys string, debounceMs int) error {
	return p.t.SendKeysDebounced(sessionID, keys, debounceMs)
}

// WaitForCommand waits for the session's main process to start.
func (p *TmuxProvider) WaitForCommand(ctx context.Context, sessionID string, excludeCommands []string, timeout time.Duration) error {
	return p.t.WaitForCommand(sessionID, excludeCommands, timeout)
}

// AcceptStartupDialogs dismisses startup dialogs in the tmux session.
func (p *TmuxProvider) AcceptStartupDialogs(ctx context.Context, sessionID string) error {
	return p.t.AcceptStartupDialogs(sessionID)
}

// WaitForIdle waits for the session to become idle.
func (p *TmuxProvider) WaitForIdle(sessionID string, timeout time.Duration) error {
	return p.t.WaitForIdle(sessionID, timeout)
}

// WaitForRuntimeReady waits for the agent to be ready for input.
func (p *TmuxProvider) WaitForRuntimeReady(ctx context.Context, sessionID string, rc *config.RuntimeConfig, timeout time.Duration) error {
	return p.t.WaitForRuntimeReady(sessionID, rc, timeout)
}

func (p *TmuxProvider) CheckSessionHealth(ctx context.Context, sessionID string, maxInactivity time.Duration) tmux.ZombieStatus {
	return p.t.CheckSessionHealth(sessionID, maxInactivity)
}

func (p *TmuxProvider) GetLastActivity(ctx context.Context, sessionID string) (time.Time, error) {
	return p.t.GetSessionActivity(sessionID)
}
