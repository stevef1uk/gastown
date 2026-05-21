package daemon

import (
	"errors"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func resetDaemonOrchestratorHooks(t *testing.T) {
	t.Helper()
	origPing := orchestrator.PingFnForTest()
	origStart := orchestrator.StartFnForTest()
	origRestart := orchestrator.RestartFnForTest()
	t.Cleanup(func() {
		orchestrator.SetPingFnForTest(origPing)
		orchestrator.SetStartFnForTest(origStart)
		orchestrator.SetRestartFnForTest(origRestart)
	})
}

func TestEnsureOrchestratorHealthy_startsWhenStopped(t *testing.T) {
	resetDaemonOrchestratorHooks(t)
	town := t.TempDir()
	d := &Daemon{config: &Config{TownRoot: town}}

	var started bool
	orchestrator.SetStartFnForTest(func(string) error {
		started = true
		return nil
	})
	orchestrator.SetPingFnForTest(func(string) error { return nil })

	d.ensureOrchestratorHealthy()
	if !started {
		t.Fatal("expected orchestrator start")
	}
	if d.orchestratorLastRestart.IsZero() {
		t.Fatal("expected orchestratorLastRestart set after start")
	}
}

func TestEnsureOrchestratorHealthy_restartsWhenUnhealthy(t *testing.T) {
	resetDaemonOrchestratorHooks(t)
	town := t.TempDir()
	d := &Daemon{config: &Config{TownRoot: town}}

	if err := orchestrator.WritePIDForTest(town); err != nil {
		t.Fatal(err)
	}
	orchestrator.SetPingFnForTest(func(string) error { return errors.New("stuck") })
	var restarted bool
	orchestrator.SetRestartFnForTest(func(string) error {
		restarted = true
		return nil
	})

	d.ensureOrchestratorHealthy()
	if !restarted {
		t.Fatal("expected orchestrator restart")
	}
	if d.orchestratorLastRestart.IsZero() {
		t.Fatal("expected orchestratorLastRestart after restart")
	}
}

func TestEnsureOrchestratorHealthy_noRestartTimestampOnFailure(t *testing.T) {
	resetDaemonOrchestratorHooks(t)
	town := t.TempDir()
	d := &Daemon{config: &Config{TownRoot: town}}

	if err := orchestrator.WritePIDForTest(town); err != nil {
		t.Fatal(err)
	}
	orchestrator.SetPingFnForTest(func(string) error { return errors.New("stuck") })
	orchestrator.SetRestartFnForTest(func(string) error { return errors.New("boom") })

	before := d.orchestratorLastRestart
	d.ensureOrchestratorHealthy()
	if !d.orchestratorLastRestart.Equal(before) {
		t.Fatal("should not record restart time when restart failed")
	}
}

func TestEnsureOrchestratorHealthy_skipsWhenHealthy(t *testing.T) {
	resetDaemonOrchestratorHooks(t)
	town := t.TempDir()
	d := &Daemon{config: &Config{TownRoot: town}}

	if err := orchestrator.WritePIDForTest(town); err != nil {
		t.Fatal(err)
	}
	orchestrator.TouchOrchestratorHeartbeat(town)
	orchestrator.SetPingFnForTest(func(string) error { return nil })

	var restarted bool
	orchestrator.SetRestartFnForTest(func(string) error {
		restarted = true
		return nil
	})

	d.ensureOrchestratorHealthy()
	if restarted {
		t.Fatal("healthy orchestrator should not restart")
	}
	if !d.orchestratorLastRestart.IsZero() {
		t.Fatal("should not set restart time when no action")
	}
}
