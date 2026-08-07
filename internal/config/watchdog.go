package config

import (
	"os"
	"path/filepath"
)

// WatchdogDir returns the directory holding the rig audit/watchdog artifacts
// (exec-audit.jsonl, rigs-audit.jsonl, .enabled flag). It lives OUTSIDE the
// town so a rig wipe or `rm -rf $TOWN` cannot destroy the audit trail.
// Override with GT_WATCHDOG_DIR; defaults to ~/.config/gt-watchdog.
func WatchdogDir() string {
	if dir := os.Getenv("GT_WATCHDOG_DIR"); dir != "" {
		return dir
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".config", "gt-watchdog")
	}
	return filepath.Join(os.TempDir(), "gt-watchdog")
}

// WatchdogEnabled reports whether rig auditing is active. Auditing is gated by
// the presence of the .enabled flag file in the watchdog dir, so it can be
// toggled at runtime without restarting anything: touch the flag to enable,
// remove it to disable. Missing flag or unresolvable home => disabled.
func WatchdogEnabled() bool {
	if _, err := os.Stat(filepath.Join(WatchdogDir(), ".enabled")); err == nil {
		return true
	}
	return false
}

// MaxAuditFileBytes caps a single audit log at ~10MB. The purge timer rotates
// oversized logs; this is a belt-and-suspenders guard so an unmanaged process
// can never grow a log unboundedly.
func MaxAuditFileBytes() int64 {
	return 10 * 1024 * 1024
}
