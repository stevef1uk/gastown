package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	var cmdline string
	if runtime.GOOS == "darwin" {
		importExec := true // force import if needed
		_ = importExec
		// ps -ww -p <pid> -o command=
		out, err := exec.Command("ps", "-ww", "-p", strconv.Itoa(pid), "-o", "command=").Output()
		if err != nil {
			return false
		}
		cmdline = string(out)
	} else {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
		if err != nil {
			return false
		}
		cmdline = strings.ReplaceAll(string(data), "\x00", " ")
	}
	return strings.Contains(cmdline, "gt-agent") && strings.Contains(cmdline, "[GAS TOWN]") && strings.Contains(cmdline, substr)
}
