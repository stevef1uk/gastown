package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// serviceProcessRole deliberately accepts only service entrypoints, never wrappers,
// shell commands, agents, or a different gt subcommand mentioning these words.
func serviceProcessRole(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 3 && filepath.Base(fields[0]) == "gt" && fields[2] == "run" {
		if fields[1] == "daemon" || fields[1] == "orchestrator" {
			return fields[1]
		}
	}
	if len(fields) == 1 && filepath.Base(fields[0]) == "gt-orchestrator" {
		return "orchestrator"
	}
	return ""
}

func serviceProcessCwd(pid int) (string, error) {
	if runtime.GOOS == "linux" {
		return os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	}
	// macOS ps does not support cwd; lsof's field output preserves spaces in paths.
	out, err := exec.Command("lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn").Output()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "n") {
			return strings.TrimPrefix(line, "n"), nil
		}
	}
	return "", fmt.Errorf("no cwd for PID %d", pid)
}

func findTownServiceProcesses(townRoot, role string) ([]int, error) {
	out, err := exec.Command("ps", "-ax", "-ww", "-o", "pid=,command=").Output()
	if err != nil {
		return nil, fmt.Errorf("list service processes: %w", err)
	}
	return townServicePIDs(string(out), townRoot, role, serviceProcessCwd)
}

func townServicePIDs(list, townRoot, role string, cwd func(int) (string, error)) ([]int, error) {
	root, err := filepath.EvalSymlinks(townRoot)
	if err != nil {
		return nil, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, line := range strings.Split(list, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 1 || pid == os.Getpid() {
			continue
		}
		command := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), fields[0]))
		if serviceProcessRole(command) != role {
			continue
		}
		dir, err := cwd(pid)
		if err != nil {
			if isProcessRunning(pid) {
				return nil, fmt.Errorf("cannot establish town ownership of %s PID %d: %w", role, pid, err)
			}
			continue // Process exited during discovery.
		}
		dir, err = filepath.EvalSymlinks(dir)
		if err != nil {
			if isProcessRunning(pid) {
				return nil, fmt.Errorf("resolve cwd of %s PID %d: %w", role, pid, err)
			}
			continue
		}
		if dir == root {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

// Stop untracked duplicates as well as the PID-file owner. Daemons must be swept
// before agents, since any surviving daemon can respawn them during shutdown.
func stopDuplicateTownServices(townRoot, role string, dryRun bool) error {
	pids, err := findTownServiceProcesses(townRoot, role)
	if err != nil {
		return err
	}
	if len(pids) == 0 {
		return nil
	}
	if dryRun {
		printDownStatus(role+" processes", true, fmt.Sprintf("would stop PIDs %v", pids))
		return nil
	}
	for _, pid := range pids {
		proc, err := os.FindProcess(pid)
		if err == nil {
			err = proc.Signal(syscall.SIGTERM)
		}
		if err != nil && isProcessRunning(pid) {
			return fmt.Errorf("stop %s PID %d: %w", role, pid, err)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		remaining, err := findTownServiceProcesses(townRoot, role)
		if err != nil {
			return err
		}
		if len(remaining) == 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Re-discover and re-check ownership before escalating (also catches respawns).
	remaining, err := findTownServiceProcesses(townRoot, role)
	if err != nil {
		return err
	}
	for _, pid := range remaining {
		proc, err := os.FindProcess(pid)
		if err == nil {
			err = proc.Kill()
		}
		if err != nil && isProcessRunning(pid) {
			return fmt.Errorf("kill %s PID %d: %w", role, pid, err)
		}
	}
	deadline = time.Now().Add(time.Second)
	for {
		remaining, err = findTownServiceProcesses(townRoot, role)
		if err != nil {
			return err
		}
		if len(remaining) == 0 {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s processes still running after cleanup: %v", role, remaining)
		}
		time.Sleep(100 * time.Millisecond)
	}
	printDownStatus(role+" processes", true, fmt.Sprintf("stopped PIDs %v", pids))
	return nil
}
