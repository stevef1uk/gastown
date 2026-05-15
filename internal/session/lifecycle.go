// Package session provides polecat session lifecycle management.
package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/runtime"
	"github.com/steveyegge/gastown/internal/telemetry"
	"github.com/steveyegge/gastown/internal/tmux"
)

// SessionConfig describes how to create and start a tmux session.
// This unifies the common startup pattern that was previously duplicated
// across polecat, mayor, boot, deacon, witness, refinery, crew, and dog
// session managers. Each of those managers previously had to coordinate
// 4+ packages (config, runtime, session, tmux) manually.
//
// Usage pattern:
//
//	result, err := session.StartSession(t, session.SessionConfig{
//	    SessionID: "gt-myrig-toast",
//	    WorkDir:   "/path/to/worktree",
//	    Role:      "polecat",
//	    TownRoot:  "/path/to/town",
//	    Beacon:    session.BeaconConfig{...},
//	})
type SessionConfig struct {
	// SessionID is the tmux session name (e.g., "gt-wyvern-Toast", "hq-mayor").
	SessionID string

	// WorkDir is the working directory for the session.
	WorkDir string

	// Role is the agent role (e.g., "polecat", "mayor", "boot", "deacon").
	Role string

	// TownRoot is the root of the Gas Town workspace (e.g., ~/gt).
	TownRoot string

	// RigPath is the rig directory path for config resolution.
	// Empty for town-level agents (mayor, deacon, boot).
	RigPath string

	// RigName is the rig name for environment variables and theming.
	// Empty for town-level agents.
	RigName string

	// AgentName is the specific agent name within a rig.
	// Used for polecats, crew, and dogs. Empty for singletons.
	AgentName string

	// Command is a pre-built startup command. If non-empty, skips command building.
	// If empty, the command is built from Beacon + config.BuildAgentStartupCommand.
	Command string

	// Beacon configures the startup beacon message for session identification.
	// Ignored if Command is non-empty.
	Beacon BeaconConfig

	// Instructions are appended after the beacon in the startup prompt.
	// Used by roles like Boot and Deacon that need explicit instructions.
	// Ignored if Command is non-empty.
	Instructions string

	// AgentOverride optionally specifies a different agent alias (e.g., "opencode").
	AgentOverride string

	// RuntimeConfigDir overrides the config directory for the runtime.
	RuntimeConfigDir string

	// ExtraEnv adds additional environment variables beyond the standard AgentEnv set.
	// These are set in the tmux session environment after the standard vars.
	ExtraEnv map[string]string

	// Theme is the tmux theme to apply. Nil means no theme is applied.
	Theme *tmux.Theme

	// Post-start behavior options.

	// WaitForAgent waits for the agent command to appear in the pane.
	WaitForAgent bool

	// WaitFatal makes WaitForAgent failure fatal — kills the session and returns error.
	// If false, WaitForAgent failure is silently ignored.
	WaitFatal bool

	// AcceptBypass accepts the bypass permissions warning dialog if it appears.
	AcceptBypass bool

	// ReadyDelay sleeps for the runtime's configured readiness delay.
	ReadyDelay bool

	// AutoRespawn sets the auto-respawn hook so the session survives crashes.
	AutoRespawn bool

	// RemainOnExit sets remain-on-exit immediately after session creation.
	RemainOnExit bool

	// TrackPID tracks the pane PID for defense-in-depth orphan cleanup.
	TrackPID bool

	// VerifySurvived checks that the session is still alive after startup.
	VerifySurvived bool

	// Orchestrated starts the agent in orchestrated mode.
	Orchestrated bool

	// OnStart is called after the session is created and tracked.
	OnStart func(p Provider)
}

// StartResult contains the results of session startup.
type StartResult struct {
	// RuntimeConfig is the resolved runtime config for the role.
	// Callers may need this for role-specific post-startup steps
	// (e.g., handling fallback nudges, legacy fallback).
	RuntimeConfig *config.RuntimeConfig

	// RunID is the GASTA run identifier (GT_RUN) generated for this session.
	// All telemetry events emitted within the session carry this ID, enabling
	// waterfall correlation across prompts, BD calls, mail operations, and
	// agent conversation events.
	RunID string
}

// StartSession creates a session following the standard Gas Town lifecycle.
//
// The lifecycle handles:
//  1. Resolve runtime config for the role
//  2. Ensure settings/plugins exist for the agent
//  3. Build startup command (if not provided)
//  4. Create session with command
//  5. Set environment variables (standard + extra)
//  6. Apply theme (if configured)
//  7. Optional post-start: wait for agent, accept bypass, ready delay,
//     auto-respawn, PID tracking, verify survived
func StartSession(ctx context.Context, p Provider, cfg *SessionConfig) (*StartResult, error) {
	// Generate the GASTA run ID — the root identifier for all telemetry emitted
	// by this agent session and its subprocesses (bd, mail, …).
	runID := uuid.New().String()
	ctx = telemetry.WithRunID(ctx, runID)

	var retErr error
	defer func() { telemetry.RecordSessionStart(ctx, cfg.SessionID, cfg.Role, retErr) }()

	if cfg.SessionID == "" {
		return nil, fmt.Errorf("SessionID is required")
	}
	if cfg.WorkDir == "" {
		return nil, fmt.Errorf("WorkDir is required")
	}
	if cfg.Role == "" {
		return nil, fmt.Errorf("Role is required")
	}

	// 0. Ensure stale session is gone
	if _, err := KillExistingSession(ctx, p, cfg.SessionID, true); err != nil {
		return nil, fmt.Errorf("cleaning existing session: %w", err)
	}

	// 1. Resolve runtime config.
	runtimeConfig := config.ResolveRoleAgentConfig(cfg.Role, cfg.TownRoot, cfg.RigPath)

	// 2. Ensure settings/plugins exist for the agent.
	settingsDir := config.RoleSettingsDir(cfg.Role, cfg.RigPath)
	if settingsDir == "" {
		settingsDir = cfg.WorkDir
	}
	if err := runtime.EnsureSettingsForRole(settingsDir, cfg.WorkDir, cfg.Role, runtimeConfig); err != nil {
		return nil, fmt.Errorf("ensuring runtime settings: %w", err)
	}

	// 2.5. Write .gt-agent identity file.
	if err := ensureAgentIdentityFile(*cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not write .gt-agent identity file: %v\n", err)
	}

	// 3. Build startup command if not provided.
	command := cfg.Command
	if command == "" {
		prompt := buildPrompt(*cfg)
		var err error
		command, err = buildCommand(*cfg, prompt)
		if err != nil {
			return nil, fmt.Errorf("building startup command: %w", err)
		}
	}

	// Prepend runtime config dir env if needed.
	if runtimeConfig.Session != nil && runtimeConfig.Session.ConfigDirEnv != "" && cfg.RuntimeConfigDir != "" {
		command = config.PrependEnv(command, map[string]string{
			runtimeConfig.Session.ConfigDirEnv: cfg.RuntimeConfigDir,
		})
	}

	// 4. Compute environment variables.
	envVars := config.AgentEnv(config.AgentEnvConfig{
		Role:             cfg.Role,
		Rig:              cfg.RigName,
		RigPath:          cfg.RigPath,
		AgentName:        cfg.AgentName,
		TownRoot:         cfg.TownRoot,
		RuntimeConfigDir: cfg.RuntimeConfigDir,
		Agent:            cfg.AgentOverride,
		SessionName:      cfg.SessionID,
	})
	envVars = MergeRuntimeLivenessEnv(envVars, runtimeConfig)
	envVars["GT_RUN"] = runID
	for k, v := range cfg.ExtraEnv {
		envVars[k] = v
	}

	// 4. Create session with command and env vars
	opts := StartOptions{
		SessionID: cfg.SessionID,
		WorkDir:   cfg.WorkDir,
		Command:   command,
		Env:       envVars,
		Theme:     cfg.Theme,
	}
	if err := p.Start(ctx, opts); err != nil {
		return nil, fmt.Errorf("creating session: %w", err)
	}

	// 5. Set remain-on-exit immediately if requested.
	if cfg.RemainOnExit {
		_ = p.SetRemainOnExit(ctx, cfg.SessionID, true)
	}

	// 6. Wait for agent to start.
	if cfg.WaitForAgent {
		if tp, ok := p.(*TmuxProvider); ok {
			if err := tp.t.WaitForCommand(cfg.SessionID, constants.SupportedShells, constants.ClaudeStartTimeout); err != nil {
				if cfg.WaitFatal {
					_ = tp.t.KillSessionWithProcesses(cfg.SessionID)
					return nil, fmt.Errorf("waiting for %s to start: %w", cfg.Role, err)
				}
			}
		}
	}

	// 7. Auto-respawn hook.
	if cfg.AutoRespawn {
		if tp, ok := p.(*TmuxProvider); ok {
			if err := tp.t.SetAutoRespawnHook(cfg.SessionID); err != nil {
				fmt.Printf("warning: failed to set auto-respawn hook for %s: %v\n", cfg.Role, err)
			}
		}
	}

	// 8. Accept startup dialogs.
	if cfg.AcceptBypass {
		if tp, ok := p.(*TmuxProvider); ok {
			_ = tp.t.AcceptStartupDialogs(cfg.SessionID)
		}
	}

	// 9. Ready delay.
	if cfg.ReadyDelay {
		if tp, ok := p.(*TmuxProvider); ok {
			if err := tp.t.WaitForRuntimeReady(cfg.SessionID, runtimeConfig, constants.ClaudeStartTimeout); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: agent readiness detection timed out for %s: %v\n", cfg.SessionID, err)
			}
		} else {
			running, _ := p.IsAgentRunning(ctx, cfg.SessionID)
			if !running {
				fmt.Fprintf(os.Stderr, "Warning: agent not running for %s\n", cfg.SessionID)
			}
		}
	}

	// 10. Verify session survived startup.
	if cfg.VerifySurvived {
		running, err := p.Exists(ctx, cfg.SessionID)
		if err != nil {
			_ = p.Stop(ctx, cfg.SessionID, false)
			return nil, fmt.Errorf("verifying session: %w", err)
		}
		if !running {
			return nil, fmt.Errorf("session %s died during startup", cfg.SessionID)
		}
	}

	// 11. Set GT_PANE_ID
	if tp, ok := p.(*TmuxProvider); ok {
		if paneID, err := tp.Tmux().GetPaneID(cfg.SessionID); err == nil {
			_ = p.SetEnvironment(ctx, cfg.SessionID, "GT_PANE_ID", paneID)
		}
	}

	// 12. Track PID for defense-in-depth orphan cleanup (non-fatal)
	if tp, ok := p.(*TmuxProvider); ok {
		_ = TrackSessionPID(cfg.TownRoot, cfg.SessionID, tp.Tmux())
	}

	// 13. Stream agent conversation events to VictoriaLogs (opt-in).
	if os.Getenv("GT_LOG_AGENT_OUTPUT") == "true" && os.Getenv("GT_OTEL_LOGS_URL") != "" {
		if err := ActivateAgentLogging(cfg.SessionID, cfg.WorkDir, runID); err != nil {
			fmt.Fprintf(os.Stderr, "warning: agent log watcher setup failed for %s: %v\n", cfg.SessionID, err)
		}
	}

	// 14. Record agent instantiation event
	if cfg.OnStart != nil {
		cfg.OnStart(p)
	}

	RecordAgentInstantiateFromDir(ctx, runID, runtimeConfig.ResolvedAgent,
		cfg.Role, cfg.AgentName, cfg.SessionID, cfg.RigName, cfg.TownRoot, "", cfg.WorkDir)

	return &StartResult{RuntimeConfig: runtimeConfig, RunID: runID}, nil
}

// RecordAgentInstantiateFromDir resolves the git branch/commit from workDir and
// emits the agent.instantiate root telemetry event.
func RecordAgentInstantiateFromDir(ctx context.Context, runID, resolvedAgent, role, agentName, sessionID, rigName, townRoot, issueID, workDir string) {
	agentType := resolvedAgent
	if agentType == "" {
		agentType = "claudecode"
	}
	branch, commit := "", ""
	if g := git.NewGit(workDir); g != nil {
		if b, err := g.CurrentBranch(); err == nil {
			branch = b
		}
		if c, err := g.Rev("HEAD"); err == nil {
			commit = c
		}
	}
	telemetry.RecordAgentInstantiate(ctx, telemetry.AgentInstantiateInfo{
		RunID:     runID,
		AgentType: agentType,
		Role:      role,
		AgentName: agentName,
		SessionID: sessionID,
		RigName:   rigName,
		TownRoot:  townRoot,
		IssueID:   issueID,
		GitBranch: branch,
		GitCommit: commit,
	})
}

// StopSession stops a session with optional graceful shutdown.
func StopSession(p Provider, sessionID string, graceful bool) error {
	ctx := context.Background()
	running, err := p.Exists(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("checking session: %w", err)
	}
	if !running {
		return fmt.Errorf("session not found: %s", sessionID)
	}

	if graceful {
		_ = p.Inject(ctx, sessionID, "C-c")
		WaitForSessionExit(p, sessionID, constants.GracefulShutdownTimeout)
	}

	DeactivateAgentLogging(sessionID)

	if err := p.Stop(ctx, sessionID, true); err != nil {
		return fmt.Errorf("killing session: %w", err)
	}

	return nil
}

func mapKeysSorted(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// MergeRuntimeLivenessEnv ensures liveness-critical env vars are present.
func MergeRuntimeLivenessEnv(envVars map[string]string, runtimeConfig *config.RuntimeConfig) map[string]string {
	if envVars == nil {
		envVars = make(map[string]string)
	}
	if runtimeConfig == nil {
		return envVars
	}

	if _, hasGTAgent := envVars["GT_AGENT"]; !hasGTAgent && runtimeConfig.ResolvedAgent != "" {
		envVars["GT_AGENT"] = runtimeConfig.ResolvedAgent
	}

	if _, hasProcessNames := envVars["GT_PROCESS_NAMES"]; !hasProcessNames {
		var processNames []string
		if runtimeConfig.Tmux != nil && len(runtimeConfig.Tmux.ProcessNames) > 0 {
			processNames = runtimeConfig.Tmux.ProcessNames
		} else {
			agentForLookup := runtimeConfig.ResolvedAgent
			commandForLookup := runtimeConfig.Command
			argsForLookup := runtimeConfig.Args
			if existing, ok := envVars["GT_AGENT"]; ok && existing != "" {
				agentForLookup = existing
				if existing != runtimeConfig.ResolvedAgent {
					commandForLookup = ""
					argsForLookup = nil
				}
			}
			processNames = config.ResolveProcessNames(agentForLookup, commandForLookup, argsForLookup...)
		}
		if len(processNames) > 0 {
			envVars["GT_PROCESS_NAMES"] = strings.Join(processNames, ",")
		}
	}

	return envVars
}

// KillExistingSession terminates a session if it exists.
// If checkAlive is true, only kills zombie sessions (transport alive but agent dead).
func KillExistingSession(ctx context.Context, p Provider, sessionID string, checkAlive bool) (bool, error) {
	exists, err := p.Exists(ctx, sessionID)
	if err != nil {
		return false, fmt.Errorf("checking session: %w", err)
	}
	if !exists {
		return false, nil
	}

	if checkAlive {
		alive, err := p.IsAgentRunning(ctx, sessionID)
		if err == nil && alive {
			return false, nil // Session is healthy, don't kill
		}
	}

	// Kill it
	if err := p.Stop(ctx, sessionID, true); err != nil {
		return true, fmt.Errorf("killing session: %w", err)
	}

	return true, nil
}

// buildPrompt creates the startup prompt from beacon + instructions.
func buildPrompt(cfg SessionConfig) string {
	if cfg.Instructions != "" {
		return BuildStartupPrompt(cfg.Beacon, cfg.Instructions)
	}
	return FormatStartupBeacon(cfg.Beacon)
}

// buildCommand creates the startup command using the config package.
func buildCommand(cfg SessionConfig, prompt string) (string, error) {
	var cmd string
	var err error
	if cfg.AgentOverride != "" {
		cmd, err = config.BuildAgentStartupCommandWithAgentOverride(
			cfg.Role, cfg.RigName, cfg.TownRoot, cfg.RigPath, prompt, cfg.AgentOverride)
	} else {
		cmd = config.BuildAgentStartupCommand(
			cfg.Role, cfg.RigName, cfg.TownRoot, cfg.RigPath, prompt)
	}

	if err == nil && cfg.Orchestrated {
		cmd += " --orchestrated"
	}
	return cmd, err
}

// ShutdownDelay is the standard delay after session creation.
// Some roles use this instead of the runtime's ready delay.
func ShutdownDelay() time.Duration {
	return constants.ShutdownNotifyDelay
}

// ensureAgentIdentityFile writes a JSON file identifying the agent's role and rig.
// This is read by gt-agent on startup to resolve its identity.
func ensureAgentIdentityFile(cfg SessionConfig) error {
	type agentIdentityFile struct {
		Role string `json:"role"`
		Rig  string `json:"rig,omitempty"`
		Name string `json:"name,omitempty"`
	}

	id := agentIdentityFile{
		Role: cfg.Role,
		Rig:  cfg.RigName,
		Name: cfg.AgentName,
	}

	data, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return err
	}

	path := filepath.Join(cfg.WorkDir, ".gt-agent")
	return os.WriteFile(path, data, 0644)
}
