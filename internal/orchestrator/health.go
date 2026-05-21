package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	// OrchestratorPingTimeout is the NATS round-trip budget for liveness probes.
	OrchestratorPingTimeout = 5 * time.Second
	// orchestratorRestartGrace is how long after Start/Restart before heartbeat-only kills apply.
	orchestratorRestartGrace = 45 * time.Second
)

// orchestratorPingFn is swapped in tests to avoid requiring a live NATS broker.
var orchestratorPingFn = defaultOrchestratorPing

func defaultOrchestratorPing(townRoot string) error {
	_, err := CallWithTimeout(townRoot, "call_tool", map[string]interface{}{
		"name":      "ping",
		"arguments": map[string]interface{}{},
	}, OrchestratorPingTimeout)
	return err
}

// Ping checks that the orchestrator MCP service responds on NATS.
func Ping(townRoot string) error {
	return orchestratorPingFn(townRoot)
}

// UnhealthyReason returns why the orchestrator should be restarted, or "" if healthy or stopped.
// A stopped process (no PID) is not "unhealthy" — use EnsureHealthy to start it.
func UnhealthyReason(townRoot string) string {
	running, _, err := IsRunning(townRoot)
	if err != nil {
		return fmt.Sprintf("pid check: %v", err)
	}
	if !running {
		return ""
	}
	if err := Ping(townRoot); err != nil {
		return "mcp ping: " + strings.TrimSpace(err.Error())
	}
	stale, exists := IsOrchestratorHeartbeatStale(townRoot, OrchestratorHeartbeatStaleThreshold)
	if exists && stale {
		return fmt.Sprintf("heartbeat stale (>%s)", OrchestratorHeartbeatStaleThreshold)
	}
	return ""
}

// orchestratorStartFn and orchestratorRestartFn are swapped in tests.
var orchestratorStartFn = Start
var orchestratorRestartFn = defaultOrchestratorRestart

func defaultOrchestratorRestart(townRoot string) error {
	_ = Stop(townRoot)
	if running, pid, _ := IsRunning(townRoot); running && pid > 0 {
		if proc, err := os.FindProcess(pid); err == nil {
			_ = proc.Signal(syscall.SIGKILL)
		}
		_ = os.Remove(filepath.Join(townRoot, PidFile))
	}
	if err := orchestratorStartFn(townRoot); err != nil {
		return err
	}
	TouchOrchestratorHeartbeat(townRoot)
	return nil
}

// Restart stops a running orchestrator (force-kill if needed) and starts a fresh process.
func Restart(townRoot string) error {
	return orchestratorRestartFn(townRoot)
}

// EnsureHealthy starts the orchestrator when stopped and restarts it when UnhealthyReason is set.
// lastRestartAt is optional grace: after a recent restart, heartbeat-only failures are ignored when ping OK.
func EnsureHealthy(townRoot string, lastRestartAt time.Time) (action string, err error) {
	running, _, _ := IsRunning(townRoot)
	if !running {
		if err := orchestratorStartFn(townRoot); err != nil {
			return "", err
		}
		TouchOrchestratorHeartbeat(townRoot)
		return "started", nil
	}
	reason := UnhealthyReason(townRoot)
	if reason == "" {
		return "", nil
	}
	if strings.HasPrefix(reason, "heartbeat stale") && !lastRestartAt.IsZero() &&
		time.Since(lastRestartAt) < orchestratorRestartGrace {
		return "", nil
	}
	if err := Restart(townRoot); err != nil {
		return "", fmt.Errorf("restart (%s): %w", reason, err)
	}
	return "restarted: " + reason, nil
}

// Test-only hooks (used by health_test.go and daemon/orchestrator_health_test.go).

func SetPingFnForTest(fn func(string) error)       { orchestratorPingFn = fn }
func PingFnForTest() func(string) error           { return orchestratorPingFn }
func SetStartFnForTest(fn func(string) error)      { orchestratorStartFn = fn }
func StartFnForTest() func(string) error          { return orchestratorStartFn }
func SetRestartFnForTest(fn func(string) error)    { orchestratorRestartFn = fn }
func RestartFnForTest() func(string) error        { return orchestratorRestartFn }

// WritePIDForTest writes the current process PID as the orchestrator pid file.
func WritePIDForTest(townRoot string) error {
	pidPath := filepath.Join(townRoot, PidFile)
	if err := os.MkdirAll(filepath.Dir(pidPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o644)
}
