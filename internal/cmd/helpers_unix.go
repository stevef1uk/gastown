//go:build !windows

package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/tmux"
	"github.com/steveyegge/gastown/internal/workspace"
)

// attachToTmuxSession attaches to a session (tmux or NATS).
func attachToTmuxSession(sessionID string) error {
	townRoot, _ := workspace.FindFromCwd()
	sp := session.GetDefaultProvider(townRoot)

	// For TmuxProvider, we use the existing syscall.Exec logic to replace
	// the current process with tmux for direct terminal control.
	if _, ok := sp.(*session.TmuxProvider); ok {
		tmuxPath, err := exec.LookPath("tmux")
		if err != nil {
			return fmt.Errorf("tmux not found: %w", err)
		}

		// Base args with UTF-8 and socket support
		baseArgs := []string{"tmux", "-u"}
		if socket := tmux.GetDefaultSocket(); socket != "" {
			baseArgs = append(baseArgs, "-L", socket)
		}

		var args []string
		if isInSameTmuxSocket() {
			// Same tmux socket: switch to the target session
			args = append(baseArgs, "switch-client", "-t", sessionID)
		} else {
			// Outside tmux or different socket: attach to the session
			args = append(baseArgs, "attach-session", "-t", sessionID)
		}

		// Replace the Go process with tmux for direct terminal control
		return syscall.Exec(tmuxPath, args, os.Environ())
	}

	// For other providers (NATS), use the provider's AttachSession implementation
	// which typically tails logs.
	return sp.AttachSession(context.Background(), sessionID)
}

// execAgent execs the configured agent, replacing the current process.
// Used when we're already in the target session and just need to start the agent.
// If prompt is provided, it's passed as the initial prompt.
func execAgent(cfg *config.RuntimeConfig, prompt string) error {
	if cfg == nil {
		cfg = config.DefaultRuntimeConfig()
	}

	agentPath, err := exec.LookPath(cfg.Command)
	if err != nil {
		return fmt.Errorf("%s not found: %w", cfg.Command, err)
	}

	// exec replaces current process with agent
	// args[0] must be the command name (convention for exec)
	args := append([]string{cfg.Command}, cfg.Args...)
	if prompt != "" {
		args = append(args, prompt)
	}
	return syscall.Exec(agentPath, args, os.Environ())
}

// execRuntime execs the runtime CLI, replacing the current process.
// Used when we're already in the target session and just need to start the runtime.
// If prompt is provided, it's passed according to the runtime's prompt mode.
func execRuntime(prompt, rigPath, configDir string) error {
	townRoot := filepath.Dir(rigPath)
	runtimeConfig := config.ResolveRoleAgentConfig("crew", townRoot, rigPath)
	args := runtimeConfig.BuildArgsWithPrompt(prompt)
	if len(args) == 0 {
		return fmt.Errorf("runtime command not configured")
	}

	binPath, err := exec.LookPath(args[0])
	if err != nil {
		return fmt.Errorf("runtime command not found: %w", err)
	}

	env := os.Environ()
	if runtimeConfig.Session != nil && runtimeConfig.Session.ConfigDirEnv != "" && configDir != "" {
		env = append(env, fmt.Sprintf("%s=%s", runtimeConfig.Session.ConfigDirEnv, configDir))
	}

	return syscall.Exec(binPath, args, env)
}
