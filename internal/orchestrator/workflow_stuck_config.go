package orchestrator

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultWorkflowStuckIdleMinutes     = 30
	defaultWorkflowStuckReworkMinutes   = 20
	defaultWorkflowStuckCooldownMinutes = 10
	defaultWorkflowStuckGraceMinutes    = 10
	envWorkflowStuckMonitor             = "GT_WORKFLOW_STUCK_MONITOR"
	envWorkflowStuckIdleMinutes         = "GT_WORKFLOW_STUCK_IDLE_MINUTES"
	envWorkflowStuckReworkMinutes       = "GT_WORKFLOW_STUCK_REWORK_MINUTES"
	envWorkflowStuckCooldownMinutes     = "GT_WORKFLOW_STUCK_COOLDOWN_MINUTES"
	envWorkflowStuckGraceMinutes        = "GT_WORKFLOW_STUCK_GRACE_MINUTES"
)

// WorkflowStuckConfig tunes daemon-side rig-flow stuck detection and repair.
type WorkflowStuckConfig struct {
	Enabled        bool
	IdleAfter      time.Duration
	ReworkLinger   time.Duration
	RepairCooldown time.Duration
	StateGrace     time.Duration
}

// WorkflowStuckConfigFromEnv loads monitor settings from the environment.
// Monitor is on by default; set GT_WORKFLOW_STUCK_MONITOR=0 to disable.
func WorkflowStuckConfigFromEnv() WorkflowStuckConfig {
	cfg := WorkflowStuckConfig{
		Enabled:        envBoolDefault(envWorkflowStuckMonitor, true),
		IdleAfter:      time.Duration(envIntDefault(envWorkflowStuckIdleMinutes, defaultWorkflowStuckIdleMinutes)) * time.Minute,
		ReworkLinger:   time.Duration(envIntDefault(envWorkflowStuckReworkMinutes, defaultWorkflowStuckReworkMinutes)) * time.Minute,
		RepairCooldown: time.Duration(envIntDefault(envWorkflowStuckCooldownMinutes, defaultWorkflowStuckCooldownMinutes)) * time.Minute,
		StateGrace:     time.Duration(envIntDefault(envWorkflowStuckGraceMinutes, defaultWorkflowStuckGraceMinutes)) * time.Minute,
	}
	if cfg.IdleAfter < 5*time.Minute {
		cfg.IdleAfter = 5 * time.Minute
	}
	if cfg.ReworkLinger < 5*time.Minute {
		cfg.ReworkLinger = 5 * time.Minute
	}
	if cfg.RepairCooldown < 2*time.Minute {
		cfg.RepairCooldown = 2 * time.Minute
	}
	return cfg
}

func envBoolDefault(key string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	switch strings.ToLower(v) {
	case "0", "false", "no", "off":
		return false
	case "1", "true", "yes", "on":
		return true
	default:
		return def
	}
}

func envIntDefault(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}
