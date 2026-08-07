package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/steveyegge/gastown/internal/config"
)

// execAuditLock serializes writes to the audit log from concurrent agent sessions.
var execAuditLock sync.Mutex

// auditExecRecord is one immutable entry in the exec audit trail. Every shell
// command that reaches runOrchestratedCommand is captured here verbatim so the
// root cause of destructive rig operations (rm -rf, gt rig remove, etc.) can be
// traced after the fact — session logs truncate and typescript mirrors live
// inside the rig, which is itself deleted in a wipe.
type auditExecRecord struct {
	TS          string `json:"ts"`
	Session     string `json:"session,omitempty"`
	WorkDir     string `json:"workdir"`
	Pid         int    `json:"pid,omitempty"`
	Command     string `json:"command"`
	TimeoutSec  int    `json:"timeout_sec,omitempty"`
	TimedOut    bool   `json:"timed_out,omitempty"`
	Err         string `json:"err,omitempty"`
	DurationMs  int64  `json:"duration_ms,omitempty"`
	OutHead     string `json:"out_head,omitempty"`
	Role        string `json:"role,omitempty"`
	AgentPID    int    `json:"agent_pid,omitempty"`
	AgentCmd    string `json:"agent_cmdline,omitempty"`
}

// auditExecPath returns the JSONL audit log path. The log lives OUTSIDE the
// town (under the user's ~/.config/gt-watchdog dir) so that a rig wipe or a
// full `rm -rf $TOWN` cannot destroy the evidence.
func auditExecPath() string {
	return filepath.Join(config.WatchdogDir(), "exec-audit.jsonl")
}

// auditExec captures a shell command execution into the persistent audit log.
// The command is recorded verbatim (never truncated) along with its working
// directory, PID, and outcome so later forensic analysis can attribute rig
// mutations to the exact command that caused them.
func auditExec(rec auditExecRecord) {
	// Skip Go test binaries (name ends .test) and gt binaries built into
	// os.TempDir() (e.g. /tmp/gt-integration-test) so test runs don't flood
	// the real audit log.
	if strings.HasSuffix(os.Args[0], ".test") || isExecTempPath(rec.WorkDir) {
		return
	}
	// Audit only when enabled; skip once the log passes the size cap (the
	// purge timer rotates it).
	if !config.WatchdogEnabled() {
		return
	}
	path := auditExecPath()
	if st, err := os.Stat(path); err == nil && st.Size() > config.MaxAuditFileBytes() {
		return
	}
	rec.TS = time.Now().Format(time.RFC3339Nano)
	rec.Command = strings.TrimSpace(rec.Command)
	rec.Role = os.Getenv("GT_ROLE")
	rec.AgentPID = os.Getpid()
	if cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", os.Getpid())); err == nil {
		rec.AgentCmd = strings.Join(strings.Fields(strings.ReplaceAll(string(cmdline), "\x00", " ")), " ")
	}

	data, err := json.Marshal(rec)
	if err != nil {
		return
	}

	execAuditLock.Lock()
	defer execAuditLock.Unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(data, '\n'))
}

// auditExecStart records the beginning of a command execution.
func auditExecStart(cmd, workDir, session string, pid, timeoutSec int) {
	auditExec(auditExecRecord{
		Session:    session,
		WorkDir:    workDir,
		Pid:        pid,
		Command:    cmd,
		TimeoutSec: timeoutSec,
	})
}

// auditExecDone records the completion of a command execution.
func auditExecDone(cmd, workDir, session string, pid int, timedOut bool, err error, duration time.Duration, out []byte) {
	outHead := ""
	if len(out) > 0 {
		head := out
		if len(head) > 400 {
			head = head[:400]
		}
		outHead = strings.TrimSpace(string(head))
	}
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	auditExec(auditExecRecord{
		Session:    session,
		WorkDir:    workDir,
		Pid:        pid,
		Command:    cmd,
		TimedOut:   timedOut,
		Err:        errStr,
		DurationMs: duration.Milliseconds(),
		OutHead:    outHead,
	})
}

// isExecTempPath reports whether a work directory is under the process temp
// dir. Test harnesses run commands in tmp towns, which never belong in the
// production audit trail.
func isExecTempPath(workDir string) bool {
	tmp := os.TempDir()
	if tmp == "" || workDir == "" {
		return false
	}
	return strings.HasPrefix(filepath.Clean(workDir), filepath.Clean(tmp)+string(filepath.Separator))
}
