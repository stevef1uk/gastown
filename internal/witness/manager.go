package witness

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/nudge"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/runtime"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/tmux"
	"github.com/steveyegge/gastown/internal/workspace"
)

// Common errors
var (
	ErrNotRunning     = errors.New("witness not running")
	ErrAlreadyRunning = errors.New("witness already running")
)

// Manager handles witness lifecycle and monitoring operations.
// ZFC-compliant: tmux session is the source of truth for running state.
type Manager struct {
	rig *rig.Rig
}

// NewManager creates a new witness manager for a rig.
func NewManager(r *rig.Rig) *Manager {
	return &Manager{
		rig: r,
	}
}

// IsRunning checks if the witness session is active and healthy.
// Checks both tmux session existence AND agent process liveness to avoid
// reporting zombie sessions (tmux alive but Claude dead) as "running".
// ZFC: tmux session existence is the source of truth for session state,
// but agent liveness determines if the session is actually functional.
func (m *Manager) IsRunning() (bool, error) {
	sp := session.GetDefaultProvider(m.townRoot())
	return sp.Exists(context.Background(), m.SessionName())
}

// IsHealthy checks if the witness is running and has been active recently.
// For tmux, checks session health. For NATS, just checks process existence.
func (m *Manager) IsHealthy(maxInactivity time.Duration) tmux.ZombieStatus {
	sp := session.GetDefaultProvider(m.townRoot())
	if tp, ok := sp.(*session.TmuxProvider); ok {
		return tp.Tmux().CheckSessionHealth(m.SessionName(), maxInactivity)
	}
	// NATS: basic process existence check
	if running, _ := sp.Exists(context.Background(), m.SessionName()); running {
		return tmux.SessionHealthy
	}
	return tmux.SessionDead
}

// SessionName returns the session name for this witness.
func (m *Manager) SessionName() string {
	return session.WitnessSessionName(session.PrefixFor(m.rig.Name))
}

// Status returns information about the witness session.
func (m *Manager) Status() (*tmux.SessionInfo, error) {
	sp := session.GetDefaultProvider(m.townRoot())
	sessionID := m.SessionName()
	ctx := context.Background()

	running, err := sp.Exists(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("checking session: %w", err)
	}
	if !running {
		return nil, ErrNotRunning
	}

	if tp, ok := sp.(*session.TmuxProvider); ok {
		return tp.Tmux().GetSessionInfo(sessionID)
	}
	return &tmux.SessionInfo{Name: sessionID}, nil
}

// witnessDir returns the working directory for the witness.
// Prefers witness/rig/, falls back to witness/, then rig root.
func (m *Manager) witnessDir() string {
	witnessRigDir := filepath.Join(m.rig.Path, "witness", "rig")
	if _, err := os.Stat(witnessRigDir); err == nil {
		return witnessRigDir
	}

	witnessDir := filepath.Join(m.rig.Path, "witness")
	if _, err := os.Stat(witnessDir); err == nil {
		return witnessDir
	}

	return m.rig.Path
}

// Start starts the witness.
// If foreground is true, returns an error (foreground mode deprecated).
// Otherwise, spawns a Claude agent in a tmux session.
// agentOverride optionally specifies a different agent alias to use.
// envOverrides are KEY=VALUE pairs that override all other env var sources.
// ZFC-compliant: no state file, tmux session is source of truth.
func (m *Manager) Start(foreground bool, agentOverride string, envOverrides []string) error {
	if foreground {
		// Foreground mode is deprecated - patrol logic moved to mol-witness-patrol
		return fmt.Errorf("foreground mode is deprecated; use background mode (remove --foreground flag)")
	}

	townRoot := m.townRoot()
	sp := session.GetDefaultProvider(townRoot)
	sessionID := m.SessionName()
	ctx := context.Background()

	// Check if session already exists using the provider
	if running, _ := sp.Exists(ctx, sessionID); running {
		return ErrAlreadyRunning
	}

	// Ensure runtime settings exist in the shared witness parent directory.
	witnessDir := m.witnessDir()
	if err := beads.SetupRedirect(townRoot, witnessDir); err != nil {
		return fmt.Errorf("ensuring witness beads redirect: %w", err)
	}

	// Resolve CLAUDE_CONFIG_DIR from accounts.json so witness sessions
	// use the correct account. Mirrors the daemon restart path (lifecycle.go).
	accountsPath := constants.MayorAccountsPath(townRoot)
	runtimeConfigDir, _, _ := config.ResolveAccountConfigDir(accountsPath, "")
	if runtimeConfigDir == "" {
		runtimeConfigDir = os.Getenv("CLAUDE_CONFIG_DIR")
	}

	// Ensure .gitignore has required Gas Town patterns
	if err := rig.EnsureGitignorePatterns(witnessDir); err != nil {
		style.PrintWarning("could not update witness .gitignore: %v", err)
	}

	roleConfig, err := m.roleConfig()
	if err != nil {
		// Non-fatal: role config is optional. Log and continue with defaults.
		log.Printf("warning: could not load witness role config for %s: %v", m.rig.Name, err)
		roleConfig = nil
	}

	// Build extra env vars from role config and CLI overrides
	extraEnv := make(map[string]string)
	roleEnv := roleConfigEnvVars(roleConfig, townRoot, m.rig.Name)
	for key, value := range roleEnv {
		extraEnv[key] = value
	}
	for _, override := range envOverrides {
		if key, value, ok := strings.Cut(override, "="); ok {
			extraEnv[key] = value
		}
	}

	// Use unified session lifecycle
	var theme *tmux.Theme
	if _, isTmux := sp.(*session.TmuxProvider); isTmux {
		theme = tmux.ResolveSessionTheme(townRoot, m.rig.Name, "witness", "")
	}

	_, err = session.StartSession(ctx, sp, session.SessionConfig{
		SessionID:        sessionID,
		WorkDir:          witnessDir,
		Role:             "witness",
		RigName:          m.rig.Name,
		TownRoot:         townRoot,
		RuntimeConfigDir: runtimeConfigDir,
		AgentOverride:    agentOverride,
		ExtraEnv:         extraEnv,
		Theme:            theme,
		WaitForAgent:     true,
		WaitFatal:        true,
		AutoRespawn:      true,
		AcceptBypass:     true,
		Beacon: session.BeaconConfig{
			Recipient: session.BeaconRecipient("witness", "", m.rig.Name),
			Sender:    "deacon",
			Topic:     "patrol",
		},
	})
	if err != nil {
		return err
	}

	// Start nudge-queue poller (gt-dgf) - only for tmux sessions
	if _, isTmux := sp.(*session.TmuxProvider); isTmux {
		if _, pollerErr := nudge.StartPoller(townRoot, sessionID); pollerErr != nil {
			log.Printf("warning: could not start nudge poller for %s: %v", sessionID, pollerErr)
		}
	}

	// For tmux: run startup fallback and deliver prompt
	if tp, ok := sp.(*session.TmuxProvider); ok {
		runtimeConfig := config.ResolveRoleAgentConfig("witness", townRoot, m.rig.Path)
		_ = runtime.RunStartupFallback(tp.Tmux(), sessionID, "witness", runtimeConfig)
		initialPrompt := session.BuildStartupPrompt(session.BeaconConfig{
			Recipient: session.BeaconRecipient("witness", "", m.rig.Name),
			Sender:    "deacon",
			Topic:     "patrol",
		}, "Run `gt prime --hook` and begin patrol.")
		_ = runtime.DeliverStartupPromptFallback(tp.Tmux(), sessionID, initialPrompt, runtimeConfig, constants.ClaudeStartTimeout)
	}

	// Generate a run ID for logging/telemetry
	runID := uuid.New().String()

	// Stream witness's Claude Code JSONL conversation log to VictoriaLogs (opt-in).
	if os.Getenv("GT_LOG_AGENT_OUTPUT") == "true" && os.Getenv("GT_OTEL_LOGS_URL") != "" {
		if err := session.ActivateAgentLogging(sessionID, witnessDir, runID); err != nil {
			log.Printf("warning: agent log watcher setup failed for %s: %v", sessionID, err)
		}
	}

	// Record the agent instantiation event (GASTA root span).
	runtimeConfig := config.ResolveRoleAgentConfig("witness", townRoot, m.rig.Path)
	session.RecordAgentInstantiateFromDir(context.Background(), runID, runtimeConfig.ResolvedAgent,
		"witness", "witness", sessionID, m.rig.Name, townRoot, "", witnessDir)

	time.Sleep(constants.ShutdownNotifyDelay)

	return nil
}

func (m *Manager) roleConfig() (*beads.RoleConfig, error) {
	townRoot := m.townRoot()
	roleDef, err := config.LoadRoleDefinition(townRoot, m.rig.Path, "witness")
	if err != nil {
		return nil, fmt.Errorf("loading witness role config: %w", err)
	}
	return &beads.RoleConfig{
		SessionPattern: roleDef.Session.Pattern,
		WorkDirPattern: roleDef.Session.WorkDir,
		NeedsPreSync:   roleDef.Session.NeedsPreSync,
		StartCommand:   roleDef.Session.StartCommand,
		EnvVars:        roleDef.Env,
	}, nil
}

func (m *Manager) townRoot() string {
	townRoot, err := workspace.Find(m.rig.Path)
	if err != nil || townRoot == "" {
		return m.rig.Path
	}
	return townRoot
}

func roleConfigEnvVars(roleConfig *beads.RoleConfig, townRoot, rigName string) map[string]string {
	if roleConfig == nil || len(roleConfig.EnvVars) == 0 {
		return nil
	}
	expanded := make(map[string]string, len(roleConfig.EnvVars))
	for key, value := range roleConfig.EnvVars {
		expanded[key] = beads.ExpandRolePattern(value, townRoot, rigName, "", "witness", session.PrefixFor(rigName))
	}
	return expanded
}

func buildWitnessStartCommand(rigPath, rigName, townRoot, sessionName, agentOverride string, roleConfig *beads.RoleConfig, runtimeConfigDir string) (string, error) {
	if agentOverride != "" {
		roleConfig = nil
	}
	if roleConfig != nil && roleConfig.StartCommand != "" {
		rc := config.ResolveRoleAgentConfig("witness", townRoot, rigPath)
		if !config.IsResolvedAgentClaude(rc) {
			// Non-Claude agent: skip TOML start_command entirely.
			// Built-in role TOMLs hardcode "exec claude ..." which is wrong
			// for non-Claude agents. Fall through to BuildStartupCommandFromConfig
			// which uses the resolved agent's command and args.
		} else if !isBuiltinClaudeStartCommand(roleConfig.StartCommand) && !config.HasExplicitRoleAgent("witness", townRoot, rigPath) {
			// Custom (non-builtin) start_command with Claude agent and no explicit
			// role_agents mapping: use TOML pattern with template expansion.
			cmd := beads.ExpandRolePattern(roleConfig.StartCommand, townRoot, rigName, "", "witness", session.PrefixFor(rigName))
			if strings.HasPrefix(cmd, "exec ") {
				cmd = "exec env -u CLAUDECODE NODE_OPTIONS='' " + strings.TrimPrefix(cmd, "exec ")
			} else {
				cmd = "env -u CLAUDECODE NODE_OPTIONS='' " + cmd
			}
			return cmd, nil
		}
		// Non-Claude agent OR Claude with built-in start_command: fall
		// through to BuildStartupCommandFromConfig for proper agent and
		// model flag resolution.
	}
	initialPrompt := session.BuildStartupPrompt(session.BeaconConfig{
		Recipient: session.BeaconRecipient("witness", "", rigName),
		Sender:    "deacon",
		Topic:     "patrol",
	}, "Run `gt prime --hook` and begin patrol.")
	command, err := config.BuildStartupCommandFromConfig(config.AgentEnvConfig{
		Role:             "witness",
		Rig:              rigName,
		TownRoot:         townRoot,
		RuntimeConfigDir: runtimeConfigDir,
		Prompt:           initialPrompt,
		Topic:            "patrol",
		SessionName:      sessionName,
	}, rigPath, initialPrompt, agentOverride)
	if err != nil {
		return "", fmt.Errorf("building startup command: %w", err)
	}
	return command, nil
}

// isBuiltinClaudeStartCommand returns true if the start_command is the
// built-in default from role TOMLs ("exec claude --dangerously-skip-permissions").
// Custom start_commands (e.g., "exec run --town {town}") return false.
func isBuiltinClaudeStartCommand(cmd string) bool {
	trimmed := strings.TrimPrefix(cmd, "exec ")
	return trimmed == "claude --dangerously-skip-permissions"
}

// Stop stops the witness.
func (m *Manager) Stop() error {
	sp := session.GetDefaultProvider(m.townRoot())
	sessionID := m.SessionName()
	ctx := context.Background()

	// Check if session exists
	running, _ := sp.Exists(ctx, sessionID)
	if !running {
		return ErrNotRunning
	}

	// Stop the session via provider
	return sp.Stop(ctx, sessionID, false)
}
