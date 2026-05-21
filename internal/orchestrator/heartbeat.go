package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// OrchestratorHeartbeatStaleThreshold is how old the orchestrator heartbeat may be
// before the daemon treats the MCP service as stuck (belt-and-suspenders with Ping).
// Prefer false positives over missed lockups — keep this below daemon recovery interval.
const OrchestratorHeartbeatStaleThreshold = 2 * time.Minute

// OrchestratorHeartbeat represents daemon/orchestrator-heartbeat.json.
type OrchestratorHeartbeat struct {
	Timestamp time.Time `json:"timestamp"`
}

// OrchestratorHeartbeatFile returns the path to the orchestrator liveness file.
func OrchestratorHeartbeatFile(townRoot string) string {
	return filepath.Join(townRoot, "daemon", "orchestrator-heartbeat.json")
}

// TouchOrchestratorHeartbeat updates the orchestrator liveness timestamp (best-effort).
func TouchOrchestratorHeartbeat(townRoot string) {
	if townRoot == "" {
		return
	}
	hb := OrchestratorHeartbeat{Timestamp: time.Now().UTC()}
	data, err := json.Marshal(hb)
	if err != nil {
		return
	}
	hbFile := OrchestratorHeartbeatFile(townRoot)
	_ = os.MkdirAll(filepath.Dir(hbFile), 0755)
	_ = os.WriteFile(hbFile, data, 0644)
}

// ReadOrchestratorHeartbeat reads the orchestrator heartbeat, or nil if missing/invalid.
func ReadOrchestratorHeartbeat(townRoot string) *OrchestratorHeartbeat {
	data, err := os.ReadFile(OrchestratorHeartbeatFile(townRoot))
	if err != nil {
		return nil
	}
	var hb OrchestratorHeartbeat
	if err := json.Unmarshal(data, &hb); err != nil || hb.Timestamp.IsZero() {
		return nil
	}
	return &hb
}

// IsOrchestratorHeartbeatStale reports whether the heartbeat file is older than threshold.
// The second return is false when no heartbeat file exists yet.
func IsOrchestratorHeartbeatStale(townRoot string, threshold time.Duration) (stale bool, exists bool) {
	hb := ReadOrchestratorHeartbeat(townRoot)
	if hb == nil {
		return false, false
	}
	return time.Since(hb.Timestamp) >= threshold, true
}

// StartHeartbeatLoop periodically touches the liveness file while the orchestrator runs.
func StartHeartbeatLoop(townRoot string) {
	if townRoot == "" {
		return
	}
	go func() {
		TouchOrchestratorHeartbeat(townRoot)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			TouchOrchestratorHeartbeat(townRoot)
		}
	}()
}
