package orchestrator

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func resetOrchestratorHealthTestHooks(t *testing.T) {
	t.Helper()
	origPing := orchestratorPingFn
	origStart := orchestratorStartFn
	origRestart := orchestratorRestartFn
	t.Cleanup(func() {
		orchestratorPingFn = origPing
		orchestratorStartFn = origStart
		orchestratorRestartFn = origRestart
	})
}

func writeOrchestratorPID(t *testing.T, townRoot string, pid int) {
	t.Helper()
	pidPath := filepath.Join(townRoot, PidFile)
	if err := os.MkdirAll(filepath.Dir(pidPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(pid)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeStaleOrchestratorHeartbeat(t *testing.T, townRoot string) {
	t.Helper()
	hbFile := OrchestratorHeartbeatFile(townRoot)
	if err := os.MkdirAll(filepath.Dir(hbFile), 0o755); err != nil {
		t.Fatal(err)
	}
	old := OrchestratorHeartbeat{Timestamp: time.Now().UTC().Add(-10 * time.Minute)}
	data, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hbFile, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestUnhealthyReason_runningPingFails(t *testing.T) {
	resetOrchestratorHealthTestHooks(t)
	town := t.TempDir()
	writeOrchestratorPID(t, town, os.Getpid())

	orchestratorPingFn = func(string) error {
		return errors.New("connection refused")
	}
	reason := UnhealthyReason(town)
	if reason == "" || !strings.Contains(reason, "mcp ping") || !strings.Contains(reason, "connection refused") {
		t.Fatalf("reason = %q, want mcp ping failure", reason)
	}
}

func TestUnhealthyReason_runningHeartbeatStale(t *testing.T) {
	resetOrchestratorHealthTestHooks(t)
	town := t.TempDir()
	writeOrchestratorPID(t, town, os.Getpid())
	writeStaleOrchestratorHeartbeat(t, town)

	orchestratorPingFn = func(string) error { return nil }
	reason := UnhealthyReason(town)
	if reason == "" || !strings.Contains(reason, "heartbeat stale") {
		t.Fatalf("reason = %q, want heartbeat stale", reason)
	}
}

func TestUnhealthyReason_runningHealthy(t *testing.T) {
	resetOrchestratorHealthTestHooks(t)
	town := t.TempDir()
	writeOrchestratorPID(t, town, os.Getpid())
	TouchOrchestratorHeartbeat(town)

	orchestratorPingFn = func(string) error { return nil }
	if reason := UnhealthyReason(town); reason != "" {
		t.Fatalf("reason = %q, want healthy", reason)
	}
}

func TestEnsureHealthy_startsWhenStopped(t *testing.T) {
	resetOrchestratorHealthTestHooks(t)
	town := t.TempDir()
	var started bool
	orchestratorStartFn = func(string) error {
		started = true
		return nil
	}
	action, err := EnsureHealthy(town, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !started || action != "started" {
		t.Fatalf("started=%v action=%q", started, action)
	}
	if ReadOrchestratorHeartbeat(town) == nil {
		t.Fatal("expected heartbeat after start")
	}
}

func TestEnsureHealthy_noOpWhenHealthy(t *testing.T) {
	resetOrchestratorHealthTestHooks(t)
	town := t.TempDir()
	writeOrchestratorPID(t, town, os.Getpid())
	TouchOrchestratorHeartbeat(town)
	orchestratorPingFn = func(string) error { return nil }

	var restarted bool
	orchestratorRestartFn = func(string) error {
		restarted = true
		return nil
	}
	action, err := EnsureHealthy(town, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if restarted || action != "" {
		t.Fatalf("restarted=%v action=%q", restarted, action)
	}
}

func TestEnsureHealthy_restartsOnPingFailure(t *testing.T) {
	resetOrchestratorHealthTestHooks(t)
	town := t.TempDir()
	writeOrchestratorPID(t, town, os.Getpid())
	orchestratorPingFn = func(string) error { return errors.New("timeout") }

	var restarted bool
	orchestratorRestartFn = func(string) error {
		restarted = true
		return nil
	}
	action, err := EnsureHealthy(town, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if !restarted {
		t.Fatal("expected restart")
	}
	if action == "" || !strings.Contains(action, "restarted") || !strings.Contains(action, "mcp ping") {
		t.Fatalf("action = %q", action)
	}
}

func TestEnsureHealthy_graceSkipsHeartbeatOnlyStale(t *testing.T) {
	resetOrchestratorHealthTestHooks(t)
	town := t.TempDir()
	writeOrchestratorPID(t, town, os.Getpid())
	writeStaleOrchestratorHeartbeat(t, town)
	orchestratorPingFn = func(string) error { return nil }

	var restarted bool
	orchestratorRestartFn = func(string) error {
		restarted = true
		return nil
	}
	action, err := EnsureHealthy(town, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if restarted || action != "" {
		t.Fatalf("grace should skip restart: restarted=%v action=%q", restarted, action)
	}
}

func TestEnsureHealthy_restartsStaleHeartbeatAfterGrace(t *testing.T) {
	resetOrchestratorHealthTestHooks(t)
	town := t.TempDir()
	writeOrchestratorPID(t, town, os.Getpid())
	writeStaleOrchestratorHeartbeat(t, town)
	orchestratorPingFn = func(string) error { return nil }

	var restarted bool
	orchestratorRestartFn = func(string) error {
		restarted = true
		return nil
	}
	action, err := EnsureHealthy(town, time.Now().Add(-orchestratorRestartGrace-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if !restarted || !strings.Contains(action, "restarted") || !strings.Contains(action, "heartbeat stale") {
		t.Fatalf("restarted=%v action=%q", restarted, action)
	}
}
