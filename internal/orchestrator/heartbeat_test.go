package orchestrator

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOrchestratorHeartbeat_touchAndStale(t *testing.T) {
	dir := t.TempDir()
	TouchOrchestratorHeartbeat(dir)
	hb := ReadOrchestratorHeartbeat(dir)
	if hb == nil {
		t.Fatal("expected heartbeat")
	}
	stale, exists := IsOrchestratorHeartbeatStale(dir, time.Hour)
	if stale || !exists {
		t.Fatalf("stale=%v exists=%v", stale, exists)
	}
	stale, exists = IsOrchestratorHeartbeatStale(dir, time.Nanosecond)
	if !stale || !exists {
		t.Fatalf("stale=%v exists=%v, want stale", stale, exists)
	}
}

func TestUnhealthyReason_stoppedNotUnhealthy(t *testing.T) {
	resetOrchestratorHealthTestHooks(t)
	orchestratorPingFn = func(string) error { return errors.New("should not ping") }
	if reason := UnhealthyReason(t.TempDir()); reason != "" {
		t.Fatalf("stopped orchestrator reason = %q, want empty", reason)
	}
}

func TestOrchestratorHeartbeat_emptyTownRootNoOp(t *testing.T) {
	dir := t.TempDir()
	TouchOrchestratorHeartbeat("")
	if _, err := os.Stat(OrchestratorHeartbeatFile(dir)); err == nil {
		t.Fatal("empty town root should not write heartbeat")
	}
}

func TestOrchestratorHeartbeat_invalidJSON(t *testing.T) {
	dir := t.TempDir()
	hbFile := OrchestratorHeartbeatFile(dir)
	if err := os.MkdirAll(filepath.Dir(hbFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hbFile, []byte("not-json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ReadOrchestratorHeartbeat(dir) != nil {
		t.Fatal("invalid heartbeat should return nil")
	}
}
