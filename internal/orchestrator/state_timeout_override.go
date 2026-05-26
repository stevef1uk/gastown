package orchestrator

import (
	"os"
	"strconv"
	"strings"
)

// StateTimeoutEnvVar overrides rig-flow state_timeout_seconds for every FSM state when set
// to a positive integer (seconds). Use a lower value (e.g. 3600) for faster paid models.
const StateTimeoutEnvVar = "GT_STATE_TIMEOUT_SECONDS"

func stateTimeoutFromEnv() (int, bool) {
	raw := strings.TrimSpace(os.Getenv(StateTimeoutEnvVar))
	if raw == "" {
		return 0, false
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}
