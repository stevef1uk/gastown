package beads

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// EnsureIssuePrefix persists issue_prefix for a rig/town beads database.
// bd init --prefix should set this, but newer bd rejects `bd config set issue_prefix`.
// Gas Town also writes config.yaml and the Dolt config table directly.
func EnsureIssuePrefix(workDir, beadsDir, prefix string) error {
	prefix = strings.TrimSpace(strings.TrimSuffix(prefix, "-"))
	if prefix == "" {
		return fmt.Errorf("empty beads prefix")
	}
	if beadsDir == "" {
		return fmt.Errorf("empty beads directory")
	}
	if workDir == "" {
		workDir = filepath.Dir(beadsDir)
	}

	if err := EnsureConfigYAML(beadsDir, prefix); err != nil {
		return fmt.Errorf("config.yaml: %w", err)
	}

	env := beadsEnvForDir(workDir, beadsDir)

	// Best-effort for older bd versions.
	pfxCmd := exec.Command("bd", "config", "set", "issue_prefix", prefix)
	pfxCmd.Dir = workDir
	pfxCmd.Env = env
	if out, err := pfxCmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" && !strings.Contains(msg, "cannot be set via") {
			// Non-fatal when SQL path succeeds; surface only unexpected errors later.
			_ = msg
		}
	}

	b := NewWithBeadsDir(workDir, beadsDir)
	if _, err := b.SQL("create table if not exists config (`key` varchar(255) primary key, `value` varchar(255))"); err != nil {
		return fmt.Errorf("ensure config table: %w", err)
	}
	esc := strings.ReplaceAll(prefix, "'", "''")
	if _, err := b.SQL(fmt.Sprintf("replace into config (`key`, `value`) values ('issue_prefix', '%s')", esc)); err != nil {
		return fmt.Errorf("set issue_prefix in database: %w", err)
	}
	return nil
}

func beadsEnvForDir(workDir, beadsDir string) []string {
	env := os.Environ()
	filtered := make([]string, 0, len(env)+2)
	for _, e := range env {
		if !strings.HasPrefix(e, "BEADS_DIR=") {
			filtered = append(filtered, e)
		}
	}
	filtered = append(filtered, "BEADS_DIR="+beadsDir)
	if workDir != "" {
		filtered = append(filtered, "GIT_CEILING_DIRECTORIES="+workDir)
	}
	if dbEnv := DatabaseEnv(beadsDir); dbEnv != "" {
		filtered = append(filtered, dbEnv)
	}
	return filtered
}
