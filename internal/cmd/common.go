package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/steveyegge/gastown/internal/tmux"
)

// getCurrentSessionName returns the current Gas Town session name.
// It prioritizes GT_SESSION (set by NATS transport or recent code),
// then falls back to GT_ROLE (the role name), and finally attempts
// to detect the tmux session name.
func getCurrentSessionName() (string, error) {
	// 1. Direct environment variable (most reliable)
	if sessionName := os.Getenv("GT_SESSION"); sessionName != "" {
		return sessionName, nil
	}

	// 2. Resolve from GT_ROLE (e.g. greenplace/witness -> gt-greenplace-witness)
	if role := os.Getenv("GT_ROLE"); role != "" {
		resolved, err := resolveRoleToSession(role)
		if err == nil && resolved != "" {
			return resolved, nil
		}
	}

	// 3. Tmux introspection (fallback for manual shell sessions)
	if pane := os.Getenv("TMUX_PANE"); pane != "" {
		out, err := tmux.BuildCommand("display-message", "-t", pane, "-p", "#{session_name}").Output()
		if err == nil {
			return strings.TrimSpace(string(out)), nil
		}
	}

	return "", fmt.Errorf("could not determine current Gas Town session (set GT_SESSION or run within tmux)")
}

// splitLines splits a string into lines and trims whitespace.
func splitLines(s string) []string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return lines
}
