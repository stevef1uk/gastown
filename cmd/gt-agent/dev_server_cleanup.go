package main

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

// protectedDevPorts are never targeted for dev-server cleanup (shared town infra).
var protectedDevPorts = map[int]bool{
	3307:  true, // Dolt
	4222:  true, // NATS
	11434: true, // Freeride / local LLM proxy
}

var localhostPortRE = regexp.MustCompile(`(?i)(?:localhost|127\.0\.0\.1):(\d{2,5})`)

// devServerTracker records dev servers started during a QA or polecat work session.
type devServerTracker struct {
	ports     map[int]struct{}
	goRunSeen bool
}

func newDevServerTracker() *devServerTracker {
	return &devServerTracker{ports: make(map[int]struct{})}
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
	// PORT=8080 go run … / -addr :8080 style hints when curl omitted.
	if port := envPortFromCommand(cmd); port > 0 && !protectedDevPorts[port] {
		t.ports[port] = struct{}{}
	}
}

func envPortFromCommand(cmd string) int {
	lower := strings.ToLower(cmd)
	for _, prefix := range []string{"port=", "addr=:", "listen=:", "http_addr=:"} {
		idx := strings.Index(lower, prefix)
		if idx < 0 {
			continue
		}
		rest := cmd[idx+len(prefix):]
		digits := ""
		for _, c := range rest {
			if c >= '0' && c <= '9' {
				digits += string(c)
			} else {
				break
			}
		}
		if digits == "" {
			continue
		}
		if p, err := strconv.Atoi(digits); err == nil && p > 0 && p <= 65535 {
			return p
		}
	}
	return 0
}

func (t *devServerTracker) needsCleanup() bool {
	if t == nil {
		return false
	}
	return t.goRunSeen || len(t.ports) > 0
}

// shutdownStartedDevServers stops listeners on tracked localhost ports and any
// stray `go run … cmd/server` processes left from smoke tests.
func shutdownStartedDevServers(t *devServerTracker) {
	if t == nil || !t.needsCleanup() {
		return
	}
	for port := range t.ports {
		killTCPListeners(port)
	}
	if t.goRunSeen {
		killGoRunServerProcesses()
	}
}

func killTCPListeners(port int) {
	if port < 1 || protectedDevPorts[port] {
		return
	}
	spec := fmt.Sprintf("%d/tcp", port)
	// fuser is standard on Debian; fall back to lsof if missing.
	if path, err := exec.LookPath("fuser"); err == nil {
		out, _ := exec.Command(path, "-k", spec).CombinedOutput()
		if msg := strings.TrimSpace(string(out)); msg != "" {
			orchestratedPrintf("[gt-agent] stopped listener on :%d: %s\n", port, msg)
		} else {
			orchestratedPrintf("[gt-agent] stopped listener on :%d\n", port)
		}
		return
	}
	if path, err := exec.LookPath("lsof"); err == nil {
		out, _ := exec.Command(path, "-ti", fmt.Sprintf(":%d", port)).CombinedOutput()
		pids := strings.Fields(strings.TrimSpace(string(out)))
		if len(pids) == 0 {
			return
		}
		args := append([]string{"-TERM"}, pids...)
		_, _ = exec.Command("kill", args...).CombinedOutput()
		orchestratedPrintf("[gt-agent] stopped listener on :%d (pids %s)\n", port, strings.Join(pids, ","))
	}
}

func killGoRunServerProcesses() {
	pkill, err := exec.LookPath("pkill")
	if err != nil {
		return
	}
	// Narrow pattern: polecat/QA smoke uses `go run` on cmd/server mains.
	patterns := []string{
		`go run.*cmd/server`,
		`go run.*\/server/main`,
	}
	for _, pat := range patterns {
		out, _ := exec.Command(pkill, "-f", pat).CombinedOutput()
		if msg := strings.TrimSpace(string(out)); msg != "" {
			orchestratedPrintf("[gt-agent] stopped go run server: %s\n", msg)
		}
	}
}

// commandStartsDevServer reports whether a shell command may bind a local HTTP port.
func commandStartsDevServer(cmd string) bool {
	return strings.Contains(strings.ToLower(cmd), "go run")
}

// freeDevServersBeforeCommand stops listeners and go run server processes implied by cmd
// before polecat/QA smoke tests (avoids "address already in use" from prior sessions).
func freeDevServersBeforeCommand(cmd string) {
	if !commandStartsDevServer(cmd) {
		return
	}
	tr := newDevServerTracker()
	tr.noteCommand(cmd)
	orchestratedPrintf("[gt-agent] freeing dev server ports before go run\n")
	shutdownStartedDevServers(tr)
}

// buildStaleDevServerTracker collects ports/processes to scrub at polecat/QA task start.
func buildStaleDevServerTracker(v orchestrator.WorkflowValidation, mayorRigDir string) *devServerTracker {
	tr := newDevServerTracker()
	for _, q := range []string{v.QAVerifyCommand, v.ActivePhaseQAVerifyCommand()} {
		tr.noteCommand(q)
	}
	if orchestrator.WorkflowUsesGo(v) && orchestrator.GoServerMainExists(mayorRigDir, v) {
		tr.goRunSeen = true
		if len(tr.ports) == 0 {
			tr.ports[8080] = struct{}{}
		}
	}
	return tr
}

func scrubStaleDevServersAtTaskStart(v orchestrator.WorkflowValidation, mayorRigDir string) {
	tr := buildStaleDevServerTracker(v, mayorRigDir)
	if !tr.needsCleanup() {
		return
	}
	orchestratedPrintf("[gt-agent] scrubbing stale dev servers at task start\n")
	shutdownStartedDevServers(tr)
}

func roleNeedsDevServerCleanup(role string) bool {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "qa", "polecat":
		return true
	default:
		return false
	}
}

func trackNeedsDevServerCleanup(track string) bool {
	switch strings.TrimSpace(track) {
	case "implementation", "qa":
		return true
	default:
		return false
	}
}
