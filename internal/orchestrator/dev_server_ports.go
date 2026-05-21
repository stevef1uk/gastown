package orchestrator

import (
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
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
	return nil
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
	if port < 1 || protectedDevPorts[port] {
		return
	}
	if runtime.GOOS == "darwin" {
		killTCPListenersLsof(port)
		return
	}
	spec := fmt.Sprintf("%d/tcp", port)
	if path, err := exec.LookPath("fuser"); err == nil {
		_, _ = exec.Command(path, "-k", spec).CombinedOutput()
	}
	killTCPListenersLsof(port)
}

func killTCPListenersLsof(port int) {
	path, err := exec.LookPath("lsof")
	if err != nil {
		return
	}
	portSpec := fmt.Sprintf(":%d", port)
	for _, sig := range []string{"TERM", "KILL"} {
		out, _ := exec.Command(path, "-ti", portSpec).CombinedOutput()
		pids := strings.Fields(strings.TrimSpace(string(out)))
		if len(pids) == 0 {
			return
		}
		args := append([]string{"-" + sig}, pids...)
		_, _ = exec.Command("kill", args...).CombinedOutput()
		if sig == "TERM" {
			time.Sleep(100 * time.Millisecond)
		}
	}
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
