package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/nudge"
	"github.com/steveyegge/gastown/internal/util"
)

const natsAutoRespawnDelay = 2 * time.Second

// natsSessionMeta tracks a NATS session for optional auto-respawn after exit.
type natsSessionMeta struct {
	workDir      string
	command      string
	env          map[string]string
	autoRespawn  bool
	stopRespawn  bool // set on explicit Stop
}

// NatsProvider implements session.Provider using NATS for coordination
// and direct OS processes for execution.
type NatsProvider struct {
	townRoot     string
	natsURL      string
	nc           *nats.Conn
	mu           sync.RWMutex
	lastActivity map[string]time.Time           // sessionID -> last activity timestamp
	sessionEnv   map[string]map[string]string   // sessionID -> env vars
	sessionMeta  map[string]*natsSessionMeta    // sessionID -> respawn state
}

// NewNatsProvider creates a new NatsProvider.
func NewNatsProvider(townRoot string, natsURL string) (*NatsProvider, error) {
	if natsURL == "" {
		natsURL = nats.DefaultURL
	}
	nc, err := nats.Connect(natsURL)
	if err != nil {
		return nil, fmt.Errorf("connecting to NATS: %w", err)
	}

	// Ensure PID directory exists
	pidDir := filepath.Join(townRoot, ".gt-nats-pids")
	if err := os.MkdirAll(pidDir, 0755); err != nil {
		return nil, fmt.Errorf("creating NATS PID directory: %w", err)
	}

	return &NatsProvider{
		townRoot:     townRoot,
		natsURL:      natsURL,
		nc:           nc,
		lastActivity: make(map[string]time.Time),
		sessionEnv:   make(map[string]map[string]string),
		sessionMeta:  make(map[string]*natsSessionMeta),
	}, nil
}

func (p *NatsProvider) Close() {
	if p.nc != nil {
		p.nc.Close()
	}
}

func (p *NatsProvider) IsAvailable() bool {
	return p.nc != nil && p.nc.IsConnected()
}

func (p *NatsProvider) recordActivity(sessionID string) {
	p.mu.Lock()
	p.lastActivity[sessionID] = time.Now()
	p.mu.Unlock()
}

func (p *NatsProvider) Start(ctx context.Context, opts StartOptions) error {
	// NATS doesn't support themes, ignore opts.Theme
	return p.startInternal(ctx, opts.SessionID, opts.WorkDir, opts.Command, opts.Env, opts.AutoRespawn)
}

func (p *NatsProvider) startInternal(ctx context.Context, sessionID, workDir, command string, env map[string]string, autoRespawn bool) error {
	p.recordActivity(sessionID)

	p.mu.Lock()
	envCopy := copyStringMap(env)
	p.sessionEnv[sessionID] = envCopy
	p.sessionMeta[sessionID] = &natsSessionMeta{
		workDir:     workDir,
		command:     command,
		env:         envCopy,
		autoRespawn: autoRespawn,
	}
	p.mu.Unlock()

	gtPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding executable: %w", err)
	}

	// Build the nats-wrapper command
	// gt nats-wrapper --session <id> --nats-url <url> -- <command>
	wrapperArgs := []string{
		"nats-wrapper",
		"--session", sessionID,
		"--nats-url", p.natsURL,
		"--",
		"bash", "-c", command,
	}

	cmd := exec.CommandContext(ctx, gtPath, wrapperArgs...)
	cmd.Dir = workDir

	// Set environment variables
	cmd.Env = os.Environ()
	if len(env) > 0 {
		for k, v := range env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}

	// Append session output (do not truncate on restart — operators tail this file).
	logDir := filepath.Join(p.townRoot, "logs", "sessions")
	_ = os.MkdirAll(logDir, 0755)
	logPath := filepath.Join(logDir, sessionID+".log")
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		_, _ = fmt.Fprintf(logFile, "\n=== session %s start @ %s ===\n", sessionID, time.Now().Format(time.RFC3339))
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	// Start the process in a new process group to decouple it from the CLI
	util.SetDetachedProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting nats-wrapper: %w", err)
	}

	// Write PID to tracking file
	pidFile := filepath.Join(p.townRoot, ".gt-nats-pids", sessionID)
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644); err != nil {
		_ = cmd.Process.Kill()
		return fmt.Errorf("writing PID file: %w", err)
	}

	go p.waitAndMaybeRespawn(sessionID, pidFile, cmd)

	return nil
}

func copyStringMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (p *NatsProvider) waitAndMaybeRespawn(sessionID, pidFile string, cmd *exec.Cmd) {
	_ = cmd.Wait()
	_ = os.Remove(pidFile)

	p.mu.RLock()
	meta := p.sessionMeta[sessionID]
	should := meta != nil && meta.autoRespawn && !meta.stopRespawn
	workDir, command, env := "", "", map[string]string(nil)
	if meta != nil {
		workDir, command, env = meta.workDir, meta.command, meta.env
	}
	p.mu.RUnlock()

	if !should || workDir == "" || command == "" {
		return
	}
	if !natsSessionShouldRespawn(p.townRoot, sessionID) {
		return
	}

	time.Sleep(natsAutoRespawnDelay)

	p.mu.RLock()
	meta = p.sessionMeta[sessionID]
	if meta == nil || meta.stopRespawn || !meta.autoRespawn {
		p.mu.RUnlock()
		return
	}
	workDir, command, env = meta.workDir, meta.command, meta.env
	p.mu.RUnlock()

	_ = p.startInternal(context.Background(), sessionID, workDir, command, env, true)
}

func (p *NatsProvider) Stop(ctx context.Context, sessionID string, graceful bool) error {
	pidStr, err := p.getPanePID(sessionID)
	if err != nil || pidStr == "" {
		// Session may already be gone, but still clean up resources
	}

	if pidStr != "" {
		pid, _ := strconv.Atoi(pidStr)
		if graceful {
			// Try graceful kill first — signal the entire tree
			_ = signalProcessTree(pid, syscall.SIGTERM)
			// Wait a bit for graceful shutdown
			select {
			case <-time.After(5 * time.Second):
				// Fall through to force kill
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		// Force-kill the entire process tree. The wrapper spawns `script(1)`
		// which allocates a new PTY and creates its own process group, so
		// killing the wrapper's process group is insufficient.
		_ = killProcessTree(pid)
	}

	// Fallback: the wrapper may have already exited (releasing its children
	// to init), or `script` created a new session that outlived the wrapper.
	// Kill any surviving gt-agent processes whose cwd is inside this town.
	_ = killAgentBySessionID(sessionID, p.townRoot)

	_ = os.Remove(filepath.Join(p.townRoot, ".gt-nats-pids", sessionID))

	p.mu.Lock()
	delete(p.sessionEnv, sessionID)
	if meta := p.sessionMeta[sessionID]; meta != nil {
		meta.stopRespawn = true
	}
	p.mu.Unlock()

	return nil
}

// killAgentBySessionID finds and kills gt-agent processes that belong to
// this town. This is a fallback for when the wrapper process tree kill
// fails (e.g., wrapper already dead, children reparented).
func killAgentBySessionID(sessionID, townRoot string) error {
	// Find all gt-agent processes and check their cwd. gt-agent processes
	// run from subdirectories of the town root (e.g., ~/gt/mayor/,
	// ~/gt/<rig>/witness/), so we can identify town membership by cwd.
	out, err := exec.Command("pgrep", "-a", "-f", "gt-agent.*\\[GAS TOWN\\]").Output()
	if err != nil {
		return err // No gt-agent processes
	}

	absTownRoot, _ := filepath.Abs(townRoot)
	if absTownRoot == "" {
		absTownRoot = townRoot
	}

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}

		// Check cwd via /proc — only kill if inside this town
		cwd, cwdErr := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
		if cwdErr != nil {
			continue
		}
		absCwd, _ := filepath.Abs(cwd)
		if absCwd == "" {
			absCwd = cwd
		}

		// Match if cwd is within the town root
		if !strings.HasPrefix(absCwd, absTownRoot+string(filepath.Separator)) && absCwd != absTownRoot {
			continue
		}

		_ = killProcessTree(pid)
	}
	return nil
}

// killProcessTree recursively kills a process and all its descendants.
// Uses /proc/<pid>/task/<tid>/children on Linux (reliable) and falls
// back to pgrep -P <pid> on other platforms.
func killProcessTree(pid int) error {
	pids := collectDescendants(pid)
	// Kill children first (bottom-up) so parents don't respawn
	for i := len(pids) - 1; i >= 0; i-- {
		p := pids[i]
		if proc, err := os.FindProcess(p); err == nil {
			_ = proc.Kill()
		}
	}
	return nil
}

// signalProcessTree sends a signal to a process and all its descendants.
func signalProcessTree(pid int, sig os.Signal) error {
	pids := collectDescendants(pid)
	for _, p := range pids {
		if proc, err := os.FindProcess(p); err == nil {
			_ = proc.Signal(sig)
		}
	}
	return nil
}

// isGTAgentProcess reports whether pid is a live gt-agent patrol/orchestration process.
func isGTAgentProcess(pid int) bool {
	if !processExists(pid) {
		return false
	}
	var cmdline string
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err == nil {
		cmdline = strings.ReplaceAll(string(data), "\x00", " ")
	} else {
		// Fallback for macOS/BSD
		out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
		if err != nil {
			return false
		}
		cmdline = string(out)
	}
	return strings.Contains(cmdline, "gt-agent") && strings.Contains(cmdline, "[GAS TOWN]")
}

// hasLiveGTAgentInTree returns true if any process in root's tree is a live gt-agent.
func hasLiveGTAgentInTree(root int) bool {
	if !processExists(root) {
		return false
	}
	for _, pid := range collectDescendants(root) {
		if isGTAgentProcess(pid) {
			return true
		}
	}
	return false
}

// collectDescendants returns a slice of PIDs starting with the root PID
// followed by all descendants in breadth-first order.
func collectDescendants(root int) []int {
	var result []int
	queue := []int{root}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		result = append(result, pid)
		children := getChildPIDs(pid)
		queue = append(queue, children...)
	}
	return result
}

// getChildPIDs returns the immediate child PIDs of a process.
func getChildPIDs(pid int) []int {
	// Try Linux /proc interface first (fastest, most reliable)
	childrenFile := fmt.Sprintf("/proc/%d/task/%d/children", pid, pid)
	if data, err := os.ReadFile(childrenFile); err == nil {
		fields := strings.Fields(string(data))
		var children []int
		for _, f := range fields {
			if cpid, err := strconv.Atoi(f); err == nil {
				children = append(children, cpid)
			}
		}
		return children
	}

	// Fallback: pgrep -P (works on most Unix systems)
	out, err := exec.Command("pgrep", "-P", strconv.Itoa(pid)).Output()
	if err != nil {
		return nil
	}
	var children []int
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if cpid, err := strconv.Atoi(line); err == nil {
			children = append(children, cpid)
		}
	}
	return children
}

func (p *NatsProvider) Exists(ctx context.Context, sessionID string) (bool, error) {
	pidStr, err := p.getPanePID(sessionID)
	if err != nil || pidStr == "" {
		return false, nil
	}

	// Check if process exists. Use full path to ps since PATH may be empty
	// in the Go process environment (common when spawned by systemd, tmux,
	// or other parent processes that don't inherit a full shell environment).
	cmd := exec.CommandContext(ctx, util.FindPsBinary(), "-p", pidStr, "-o", "pid=")
	if err := cmd.Run(); err == nil {
		return true, nil
	}

	// Stale PID file (process exited without cleanup goroutine finishing).
	_ = os.Remove(filepath.Join(p.townRoot, ".gt-nats-pids", sessionID))
	return false, nil
}

func (p *NatsProvider) List(ctx context.Context) ([]string, error) {
	pidDir := filepath.Join(p.townRoot, ".gt-nats-pids")
	entries, err := os.ReadDir(pidDir)
	if err != nil {
		return nil, nil
	}

	var sessions []string
	for _, entry := range entries {
		if !entry.IsDir() {
			sessions = append(sessions, entry.Name())
		}
	}
	return sessions, nil
}

func (p *NatsProvider) Inject(ctx context.Context, sessionID string, data string) error {
	p.recordActivity(sessionID)
	// Publish to the input subject of the session
	subject := fmt.Sprintf("gt.session.%s.input", sessionID)
	return p.nc.Publish(subject, []byte(data))
}

func (p *NatsProvider) NudgeSession(ctx context.Context, sessionID, message, sender string) error {
	p.recordActivity(sessionID)

	// 1. Direct delivery via NATS input subject.
	// This provides immediate injection for agents wrapped in nats-wrapper
	// (e.g. Claude Code).
	if p.nc != nil {
		inputSubject := fmt.Sprintf("gt.session.%s.input", sessionID)
		prefixed := fmt.Sprintf("\n[from %s] %s\n", sender, message)
		_ = p.nc.Publish(inputSubject, []byte(prefixed))
	}

	// 2. Cooperative delivery via nudge queue.
	// gt-agent (Mayor, Architect, etc.) drains this queue at the start of
	// each patrol cycle.
	if err := nudge.Enqueue(p.townRoot, sessionID, nudge.QueuedNudge{
		Sender:    sender,
		Message:   message,
		Priority:  nudge.PriorityNormal,
		Timestamp: time.Now(),
	}); err != nil {
		return fmt.Errorf("queuing nudge: %w", err)
	}

	return nil
}

func (p *NatsProvider) GetEnvironment(ctx context.Context, sessionID string) (map[string]string, error) {
	p.mu.RLock()
	env, ok := p.sessionEnv[sessionID]
	p.mu.RUnlock()
	if !ok {
		return make(map[string]string), nil
	}
	// Return a copy to prevent external mutation
	result := make(map[string]string)
	for k, v := range env {
		result[k] = v
	}
	return result, nil
}

func (p *NatsProvider) SetEnvironment(ctx context.Context, sessionID, key, value string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sessionEnv[sessionID] == nil {
		p.sessionEnv[sessionID] = make(map[string]string)
	}
	p.sessionEnv[sessionID][key] = value
	return nil
}

func (p *NatsProvider) SetRemainOnExit(ctx context.Context, sessionID string, enabled bool) error {
	return nil // Not applicable to direct processes
}

func (p *NatsProvider) SetGlobalEnvironment(key, value string) error {
	// TODO: Store global env for future Start calls if needed
	return nil
}

func (p *NatsProvider) UnsetGlobalEnvironment(key string) error {
	return nil
}

// UnsetEnvironment removes an environment variable from a session.
// For NATS, we just delete it from the session's environment map.
func (p *NatsProvider) UnsetEnvironment(ctx context.Context, sessionID, key string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.sessionEnv[sessionID] != nil {
		delete(p.sessionEnv[sessionID], key)
	}
	return nil
}

func (p *NatsProvider) Configure(ctx context.Context, sessionID string, cfg any) error {
	return nil // Not applicable
}

func (p *NatsProvider) IsAgentRunning(ctx context.Context, id string) (bool, error) {
	pidStr, err := p.getPanePID(id)
	if err != nil || pidStr == "" {
		return false, nil
	}
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return false, nil
	}
	if !processExists(pid) {
		_ = os.Remove(filepath.Join(p.townRoot, ".gt-nats-pids", id))
		return false, nil
	}
	return hasLiveGTAgentInTree(pid), nil
}

func (p *NatsProvider) CleanupOrphanedSessions(isGTSession func(string) bool) (int, error) {
	return 0, nil // NATS doesn't have "sessions" that outlive processes in the same way
}

func (p *NatsProvider) EnsureSessionFresh(ctx context.Context, opts StartOptions) error {
	_ = p.Stop(ctx, opts.SessionID, false)
	return p.Start(ctx, opts)
}

func (p *NatsProvider) WaitForRuntimeReady(ctx context.Context, sessionID string, rc *config.RuntimeConfig, timeout time.Duration) error {
	deadlineCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		deadlineCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	// Wait for the nats-wrapper process to appear (gt-agent may start later).
	for {
		running, _ := p.Exists(deadlineCtx, sessionID)
		if running {
			break
		}
		select {
		case <-deadlineCtx.Done():
			return fmt.Errorf("agent not running")
		case <-time.After(100 * time.Millisecond):
		}
	}

	// Honor the shared runtime delay floor used by startup fallback.
	// NATS does not support prompt-based readiness detection yet, so delay
	// is our only transport-agnostic readiness gate.
	if rc != nil && rc.Tmux != nil && rc.Tmux.ReadyDelayMs > 0 {
		delay := time.Duration(rc.Tmux.ReadyDelayMs) * time.Millisecond
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-deadlineCtx.Done():
			if errors.Is(deadlineCtx.Err(), context.DeadlineExceeded) {
				return fmt.Errorf("runtime ready timeout after %s", timeout)
			}
			return deadlineCtx.Err()
		case <-timer.C:
		}
	}

	return nil
}

func (p *NatsProvider) CheckSessionHealth(ctx context.Context, sessionID string, maxInactivity time.Duration) constants.ZombieStatus {
	exists, _ := p.Exists(ctx, sessionID)
	if !exists {
		return constants.SessionDead
	}

	p.mu.RLock()
	lastAct, ok := p.lastActivity[sessionID]
	p.mu.RUnlock()

	if ok && time.Since(lastAct) > maxInactivity {
		return constants.AgentHung
	}

	return constants.SessionHealthy
}

func (p *NatsProvider) GetLastActivity(ctx context.Context, sessionID string) (time.Time, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	lastAct, ok := p.lastActivity[sessionID]
	if !ok {
		return time.Time{}, fmt.Errorf("no activity recorded for session %s", sessionID)
	}
	return lastAct, nil
}

func (p *NatsProvider) StopAllSessions(ctx context.Context) error {
	// Not implemented for NATS yet (would need to kill all local wrapper processes)
	return nil
}

func (p *NatsProvider) GetMainPID(ctx context.Context, sessionID string) (string, error) {
	pidFile := filepath.Join(p.townRoot, ".gt-nats-pids", sessionID)
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return "", fmt.Errorf("reading PID file: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

func (p *NatsProvider) GetServerPID(ctx context.Context) (int, error) {
	return 0, nil // No local server PID for NATS
}

func (p *NatsProvider) GetWorkDir(ctx context.Context, sessionID string) (string, error) {
	pid, err := p.GetMainPID(ctx, sessionID)
	if err != nil {
		return "", err
	}
	// On Linux, we can get the cwd of a process from /proc/<pid>/cwd
	cwd, err := os.Readlink(fmt.Sprintf("/proc/%s/cwd", pid))
	if err != nil {
		return "", fmt.Errorf("reading /proc/%s/cwd: %w", pid, err)
	}
	return cwd, nil
}

func (p *NatsProvider) getPanePID(name string) (string, error) {
	pidFile := filepath.Join(p.townRoot, ".gt-nats-pids", name)
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(string(data)), nil
}

// IsIdle returns true if the session has had no activity for 30 seconds.
// Activity is tracked via Start, Inject, and SendKeysDebounced calls.
func (p *NatsProvider) IsIdle(ctx context.Context, sessionID string) (bool, error) {
	exists, err := p.Exists(ctx, sessionID)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}

	p.mu.RLock()
	lastAct, ok := p.lastActivity[sessionID]
	p.mu.RUnlock()

	if !ok {
		// No activity recorded — assume idle
		return true, nil
	}

	// Idle if no activity for 30 seconds
	return time.Since(lastAct) > 30*time.Second, nil
}

// CapturePane returns the last N lines from the session log file.
func (p *NatsProvider) CapturePane(ctx context.Context, sessionID string, lines int) (string, error) {
	logFile := filepath.Join(p.townRoot, "logs", "sessions", sessionID+".log")
	data, err := os.ReadFile(logFile)
	if err != nil {
		return "", err
	}

	allLines := strings.Split(string(data), "\n")
	if len(allLines) <= lines {
		return string(data), nil
	}
	return strings.Join(allLines[len(allLines)-lines:], "\n"), nil
}

// AttachSession tails the session log file for terminal parity with tmux attach.
func (p *NatsProvider) AttachSession(ctx context.Context, sessionID string) error {
	logFile := filepath.Join(p.townRoot, "logs", "sessions", sessionID+".log")

	// Verify log file exists
	if _, err := os.Stat(logFile); os.IsNotExist(err) {
		return fmt.Errorf("no log file for session %s (session may not be running)", sessionID)
	}

	// Tail the log file: show all existing content and follow new output
	cmd := exec.CommandContext(ctx, "tail", "-n", "+1", "-f", logFile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	return cmd.Run()
}

// SendKeysDebounced publishes input to the session via NATS.
func (p *NatsProvider) SendKeysDebounced(ctx context.Context, sessionID string, keys string, debounceMs int) error {
	p.recordActivity(sessionID)
	// NATS doesn't need debouncing at the transport level;
	// the caller handles debouncing. Just publish.
	subject := fmt.Sprintf("gt.session.%s.input", sessionID)
	return p.nc.Publish(subject, []byte(keys))
}

// GetSessionInfo returns provider-agnostic session metadata.
func (p *NatsProvider) SendNotificationBanner(ctx context.Context, sessionID, from, subject string) error {
	msg := fmt.Sprintf("📬 NEW MAIL from %s: %s", from, subject)
	return p.NudgeSession(ctx, sessionID, msg, "system")
}

func (p *NatsProvider) GetSessionInfo(ctx context.Context, sessionID string) (*SessionInfo, error) {
	exists, err := p.Exists(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("session not found")
	}

	pidStr, _ := p.getPanePID(sessionID)
	pid, _ := strconv.Atoi(pidStr)

	p.mu.RLock()
	lastAct := p.lastActivity[sessionID]
	p.mu.RUnlock()

	return &SessionInfo{
		Name:     sessionID,
		Windows:  1,
		Attached: false,
		Activity: lastAct.Format(time.RFC3339),
		PID:      pid,
	}, nil
}
