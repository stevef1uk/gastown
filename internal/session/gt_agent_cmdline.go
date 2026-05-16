package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// GTAgentHasFlagInSession reports whether a live gt-agent in the session's process
// tree was started with substr in its argv (e.g. "--orchestrated").
func GTAgentHasFlagInSession(townRoot, sessionID, substr string) bool {
	if townRoot == "" || sessionID == "" || substr == "" {
		return false
	}
	pidFile := filepath.Join(townRoot, ".gt-nats-pids", sessionID)
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return false
	}
	root, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || root <= 0 {
		return false
	}
	for _, pid := range collectDescendants(root) {
		if gtAgentCmdlineContains(pid, substr) {
			return true
		}
	}
	return false
}

func gtAgentCmdlineContains(pid int, substr string) bool {
	if !processExists(pid) {
		return false
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return false
	}
	cmdline := strings.ReplaceAll(string(data), "\x00", " ")
	return strings.Contains(cmdline, "gt-agent") && strings.Contains(cmdline, "[GAS TOWN]") && strings.Contains(cmdline, substr)
}
