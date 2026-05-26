package orchestrator

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var localhostPortRE = regexp.MustCompile(`(?i)(?:localhost|127\.0\.0\.1):(\d{2,5})`)

// protectedDevPorts are never killed during rig dev-server cleanup.
var protectedDevPorts = map[int]bool{
	3307:  true,
	4222:  true,
	11434: true,
}

// StopDevServersForRig frees listeners and stray go run server processes for a workflow profile.
func StopDevServersForRig(v WorkflowValidation, mayorRigDir string) error {
	tr := buildDevServerTracker(v, mayorRigDir)
	if !tr.needsCleanup() {
		return nil
	}
	for port := range tr.ports {
		killTCPListeners(port)
	}
	if tr.goRunSeen {
		killGoRunServerProcesses()
	}
	killPythonDevServerProcesses()
	return nil
}

func killPythonDevServerProcesses() {
	pkill, err := exec.LookPath("pkill")
	if err != nil {
		return
	}
	for _, pat := range []string{`uvicorn`, `gunicorn`, `hypercorn`} {
		_, _ = exec.Command(pkill, "-f", pat).CombinedOutput()
	}
}

type devServerTracker struct {
	ports     map[int]struct{}
	goRunSeen bool
}

func buildDevServerTracker(v WorkflowValidation, mayorRigDir string) *devServerTracker {
	tr := &devServerTracker{ports: make(map[int]struct{})}
	for _, q := range []string{v.QAVerifyCommand, v.ActivePhaseQAVerifyCommand()} {
		tr.noteCommand(q)
	}
	if WorkflowUsesGo(v) && GoServerMainExists(mayorRigDir, v) {
		tr.goRunSeen = true
		if len(tr.ports) == 0 {
			tr.ports[8080] = struct{}{}
		}
	}
	return tr
}

func (t *devServerTracker) needsCleanup() bool {
	return t != nil && (t.goRunSeen || len(t.ports) > 0)
}

func (t *devServerTracker) noteCommand(cmd string) {
	if t == nil || strings.TrimSpace(cmd) == "" {
		return
	}
	lower := strings.ToLower(cmd)
	if strings.Contains(lower, "go run") {
		t.goRunSeen = true
	}
	for _, m := range localhostPortRE.FindAllStringSubmatch(cmd, -1) {
		if len(m) < 2 {
			continue
		}
		port, err := strconv.Atoi(m[1])
		if err != nil || port < 1 || port > 65535 || protectedDevPorts[port] {
			continue
		}
		t.ports[port] = struct{}{}
	}
}

func killTCPListeners(port int) {
	_, _ = KillTCPListenersOnPort(port)
}

// KillTCPListenersOnPort frees a localhost TCP port using lsof+kill on macOS and Linux.
// Some Linux images ship a busybox fuser without -k; lsof is the portable path.
// Returns PIDs signalled on the last kill attempt (may be empty if none found).
func KillTCPListenersOnPort(port int) ([]string, error) {
	if port < 1 || protectedDevPorts[port] {
		return nil, nil
	}
	if fuserSupportsKill() {
		spec := fmt.Sprintf("%d/tcp", port)
		if path, err := exec.LookPath("fuser"); err == nil {
			_, _ = exec.Command(path, "-k", spec).CombinedOutput()
		}
	}
	return killTCPListenersLsof(port)
}

var (
	fuserKillProbed bool
	fuserKillOK     bool
	fuserKillMu     sync.Mutex
)

func fuserSupportsKill() bool {
	fuserKillMu.Lock()
	defer fuserKillMu.Unlock()
	if fuserKillProbed {
		return fuserKillOK
	}
	fuserKillProbed = true
	path, err := exec.LookPath("fuser")
	if err != nil {
		return false
	}
	out, err := exec.Command(path, "-k", "1/tcp").CombinedOutput()
	text := strings.ToLower(string(out))
	if err != nil {
		text += " " + strings.ToLower(err.Error())
	}
	fuserKillOK = !strings.Contains(text, "unknown option") &&
		!strings.Contains(text, "invalid option") &&
		!strings.Contains(text, "unrecognized option")
	return fuserKillOK
}

func lsofPIDsOnTCPPort(lsofPath string, port int) []string {
	for _, args := range [][]string{
		{"-nP", fmt.Sprintf("-iTCP:%d", port), "-sTCP:LISTEN", "-t"},
		{"-ti", fmt.Sprintf(":%d", port)},
	} {
		out, err := exec.Command(lsofPath, args...).CombinedOutput()
		if err != nil {
			continue
		}
		pids := strings.Fields(strings.TrimSpace(string(out)))
		if len(pids) > 0 {
			return pids
		}
	}
	return nil
}

func killTCPListenersLsof(port int) ([]string, error) {
	path, err := exec.LookPath("lsof")
	if err != nil {
		return nil, err
	}
	var lastPIDs []string
	for _, sig := range []string{"TERM", "KILL"} {
		pids := lsofPIDsOnTCPPort(path, port)
		if len(pids) == 0 {
			return lastPIDs, nil
		}
		lastPIDs = pids
		args := append([]string{"-" + sig}, pids...)
		_, _ = exec.Command("kill", args...).CombinedOutput()
		if sig == "TERM" {
			time.Sleep(100 * time.Millisecond)
		}
	}
	return lastPIDs, nil
}

func killGoRunServerProcesses() {
	pkill, err := exec.LookPath("pkill")
	if err != nil {
		return
	}
	for _, pat := range []string{`go run.*cmd/server`, `go run.*\/server/main`} {
		_, _ = exec.Command(pkill, "-f", pat).CombinedOutput()
	}
}
