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

// processCmdline returns the full command line for pid (best-effort, cross-platform).
func processCmdline(pid int) string {
	if pid <= 0 {
		return ""
	}
	if runtime.GOOS != "windows" {
		if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil {
			return strings.ReplaceAll(string(data), "\x00", " ")
		}
	}
	out, err := exec.Command("ps", "-ww", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// processWorkingDir returns the process cwd when ps supports it (macOS/Linux).
func processWorkingDir(pid int) string {
	if pid <= 0 {
		return ""
	}
	if runtime.GOOS != "windows" {
		if link, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid)); err == nil {
			return link
		}
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "cwd=").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func parentPID(pid int) int {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "ppid=").Output()
	if err != nil {
		return 0
	}
	ppid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || ppid <= 1 {
		return 0
	}
	return ppid
}

func townNatsSessionIDs(townRoot string) []string {
	seen := map[string]bool{}
	var ids []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		ids = append(ids, id)
	}
	for _, ts := range TownSessions() {
		add(ts.SessionID)
	}
	pidDir := filepath.Join(townRoot, ".gt-nats-pids")
	entries, err := os.ReadDir(pidDir)
	if err != nil {
		return ids
	}
	for _, e := range entries {
		if !e.IsDir() {
			add(e.Name())
		}
	}
	return ids
}

func cmdlineMatchesTownSession(cmd string, sessionIDs []string) bool {
	for _, id := range sessionIDs {
		if strings.Contains(cmd, "--session "+id) {
			return true
		}
	}
	return false
}

func processPIDBelongsToTown(pid int, absTown string, sessionIDs []string) bool {
	if cwd := processWorkingDir(pid); cwd != "" {
		absCwd, _ := filepath.Abs(cwd)
		if absCwd == absTown || strings.HasPrefix(absCwd, absTown+string(filepath.Separator)) {
			return true
		}
	}
	cmd := processCmdline(pid)
	if cmd == "" {
		return false
	}
	if strings.Contains(cmd, absTown) {
		return true
	}
	base := filepath.Base(absTown)
	if base != "" && base != "." && base != "/" {
		needle := base + string(filepath.Separator)
		if strings.Contains(cmd, needle) {
			return true
		}
	}
	if strings.Contains(cmd, "nats-wrapper") && cmdlineMatchesTownSession(cmd, sessionIDs) {
		return true
	}
	if strings.Contains(cmd, "gt-agent") && strings.Contains(cmd, "[GAS TOWN]") {
		return strings.Contains(cmd, absTown) || cmdlineMatchesTownSession(cmd, sessionIDs)
	}
	return false
}

func isGasTownProcessCmd(cmd string) bool {
	return strings.Contains(cmd, "nats-wrapper") ||
		(strings.Contains(cmd, "gt-agent") && strings.Contains(cmd, "[GAS TOWN]"))
}

// processBelongsToTown reports whether pid is part of this town.
func processBelongsToTown(pid int, townRoot string) bool {
	absTown, err := filepath.Abs(townRoot)
	if err != nil || absTown == "" {
		absTown = townRoot
	}
	sessionIDs := townNatsSessionIDs(townRoot)
	if processPIDBelongsToTown(pid, absTown, sessionIDs) {
		return true
	}
	cmd := processCmdline(pid)
	if !isGasTownProcessCmd(cmd) {
		return false
	}
	for cur := parentPID(pid); cur > 1; cur = parentPID(cur) {
		if processPIDBelongsToTown(cur, absTown, sessionIDs) {
			return true
		}
		if isGasTownProcessCmd(processCmdline(cur)) {
			continue
		}
		break
	}
	return false
}

// KillTownGasTownProcesses kills gt-agent and gt nats-wrapper process trees rooted in this town.
// Returns the number of root processes killed (trees may include multiple PIDs).
func KillTownGasTownProcesses(townRoot string) int {
	if townRoot == "" {
		return 0
	}
	killed := 0
	for _, pattern := range []string{"gt-agent", "nats-wrapper"} {
		out, err := exec.Command("pgrep", "-f", pattern).Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			pid, err := strconv.Atoi(line)
			if err != nil {
				continue
			}
			if !processBelongsToTown(pid, townRoot) {
				continue
			}
			if err := killProcessTree(pid); err == nil {
				killed++
			}
		}
	}
	return killed
}
