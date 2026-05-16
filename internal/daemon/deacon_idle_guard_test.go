package daemon

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	beadsdk "github.com/steveyegge/beads"
)

// writeFakeTmuxWithSession creates a fake tmux session with a live agent pane so
// checkDeaconHeartbeat uses IsAgentRunning (not just has-session) without spawning Deacon.
func writeFakeTmuxWithSession(t *testing.T, dir string) {
	t.Helper()
	writeFakeTmuxWithAgent(t, dir, "claude")
}

// TestCheckDeaconHeartbeat_IdleGuard verifies that the nudge is suppressed when
// the Deacon heartbeat is stale but no active work is in flight (idle guard).
func TestCheckDeaconHeartbeat_IdleGuard(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping on Windows — fake tmux requires bash")
	}

	tests := []struct {
		name             string
		heartbeatAge     time.Duration
		stores           map[string]beadsdk.Storage
		wantNudgeLog     bool
		wantIdleGuardLog bool
		desc             string
	}{
		{
			name:         "idle: stale heartbeat, no work — nudge suppressed",
			heartbeatAge: 10 * time.Minute,
			stores: map[string]beadsdk.Storage{
				"hq": &searchStorage{results: map[string][]*beadsdk.Issue{}},
			},
			wantNudgeLog:     false,
			wantIdleGuardLog: true,
			desc:             "Idle guard must suppress nudge when no work is in flight",
		},
		{
			name:         "active work: stale heartbeat, in_progress bead — nudge sent",
			heartbeatAge: 10 * time.Minute,
			stores: map[string]beadsdk.Storage{
				"hq": &searchStorage{results: map[string][]*beadsdk.Issue{
					"in_progress": {{ID: "sc-abc"}},
				}},
			},
			wantNudgeLog:     true,
			wantIdleGuardLog: false,
			desc:             "Nudge must fire when in_progress work exists",
		},
		{
			name:         "hooked only: stale heartbeat, patrol wisp — nudge suppressed",
			heartbeatAge: 10 * time.Minute,
			stores: map[string]beadsdk.Storage{
				"hq": &searchStorage{results: map[string][]*beadsdk.Issue{
					"hooked": {{ID: "hq-wisp-34zi"}},
				}},
			},
			wantNudgeLog:     false,
			wantIdleGuardLog: true,
			desc:             "Patrol wisps in hooked state do not count as active work; nudge must be suppressed",
		},
		{
			name:         "store error: stale heartbeat, store fails — nudge sent conservatively",
			heartbeatAge: 10 * time.Minute,
			stores: map[string]beadsdk.Storage{
				"hq": &searchStorage{err: fmt.Errorf("db offline")},
			},
			wantNudgeLog:     true,
			wantIdleGuardLog: false,
			desc:             "Nudge must fire conservatively when work state is unknown",
		},
		{
			name:         "very stale: heartbeat >= 20 min but town log fresh — skip kill (agent alive)",
			heartbeatAge: 21 * time.Minute,
			stores: map[string]beadsdk.Storage{
				"hq": &searchStorage{results: map[string][]*beadsdk.Issue{}},
			},
			wantNudgeLog:     false,
			wantIdleGuardLog: false,
			desc:             "Very stale heartbeat with fresh town log skips kill (log indicates agent is alive)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			townRoot := t.TempDir()
			fakeBinDir := t.TempDir()
			tmuxLog := filepath.Join(t.TempDir(), "tmux.log")
			if err := os.WriteFile(tmuxLog, []byte{}, 0o644); err != nil {
				t.Fatalf("create tmux log: %v", err)
			}

			writeFakeTmuxWithSession(t, fakeBinDir)
			t.Setenv("PATH", fakeBinDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("TMUX_LOG", tmuxLog)

			writeDeaconHeartbeat(t, townRoot, tc.heartbeatAge)

			// Create a fresh town log for test cases that need it.
			// This prevents false-positive kills when the agent is actually alive.
			if strings.Contains(tc.name, "town log fresh") {
				logsDir := filepath.Join(townRoot, "logs")
				if err := os.MkdirAll(logsDir, 0755); err != nil {
					t.Fatalf("create logs dir: %v", err)
				}
				townLogPath := filepath.Join(logsDir, "town.log")
				if err := os.WriteFile(townLogPath, []byte("recent output\n"), 0644); err != nil {
					t.Fatalf("create town log: %v", err)
				}
			}

			d := newTestDaemonWithStores(t, townRoot, tc.stores)

			logBuf := &strings.Builder{}
			d.logger = log.New(logBuf, "", 0)

			d.checkDeaconHeartbeat()

			logOutput := logBuf.String()

			hasIdleGuardLog := strings.Contains(logOutput, "nudge skipped")
			if hasIdleGuardLog != tc.wantIdleGuardLog {
				t.Errorf("%s\nidle guard log present=%v, want=%v\nlog:\n%s",
					tc.desc, hasIdleGuardLog, tc.wantIdleGuardLog, logOutput)
			}

			hasNudgeLog := strings.Contains(logOutput, "nudging session")
			if hasNudgeLog != tc.wantNudgeLog {
				t.Errorf("%s\nnudge log present=%v, want=%v\nlog:\n%s",
					tc.desc, hasNudgeLog, tc.wantNudgeLog, logOutput)
			}
		})
	}
}
