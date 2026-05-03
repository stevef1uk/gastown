package session

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/steveyegge/gastown/internal/util"
)

// NatsProvider implements session.Provider using NATS for coordination
// and direct OS processes for execution.
type NatsProvider struct {
	townRoot    string
	natsURL     string
	nc          *nats.Conn
	mu          sync.RWMutex
	subscribers map[string]*nats.Subscription
	stdinPipes  map[string]io.WriteCloser
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
		townRoot:    townRoot,
		natsURL:     natsURL,
		nc:          nc,
		subscribers: make(map[string]*nats.Subscription),
		stdinPipes:  make(map[string]io.WriteCloser),
	}, nil
}

func (p *NatsProvider) Close() {
	if p.nc != nil {
		p.nc.Close()
	}
}

func (p *NatsProvider) Start(ctx context.Context, sessionID, workDir, command string, env map[string]string) error {
	// We use a shell to execute the command string
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Dir = workDir

	// Set environment variables
	if len(env) > 0 {
		cmd.Env = os.Environ()
		for k, v := range env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}

	// Create stdin pipe for sending commands to the process
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("creating stdin pipe: %w", err)
	}

	// Create a log file for the session output
	logDir := filepath.Join(p.townRoot, "logs", "sessions")
	_ = os.MkdirAll(logDir, 0755)
	logFile, err := os.Create(filepath.Join(logDir, sessionID+".log"))
	if err == nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	// Start the process in a new process group to decouple it from the CLI
	util.SetDetachedProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		stdinPipe.Close()
		return fmt.Errorf("starting process: %w", err)
	}

	// Write PID to tracking file
	pidFile := filepath.Join(p.townRoot, ".gt-nats-pids", sessionID)
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644); err != nil {
		_ = cmd.Process.Kill()
		stdinPipe.Close()
		return fmt.Errorf("writing PID file: %w", err)
	}

	// Set up NATS subscriber to forward messages to process stdin
	subject := fmt.Sprintf("gt.nudge.%s", sessionID)
	sub, err := p.nc.Subscribe(subject, func(msg *nats.Msg) {
		// Append newline to ensure the command is processed
		content := string(msg.Data)
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		_, _ = stdinPipe.Write([]byte(content))
	})
	if err != nil {
		_ = cmd.Process.Kill()
		stdinPipe.Close()
		_ = os.Remove(pidFile)
		return fmt.Errorf("subscribing to NATS: %w", err)
	}

	p.mu.Lock()
	p.subscribers[sessionID] = sub
	p.stdinPipes[sessionID] = stdinPipe
	p.mu.Unlock()

	// Start a goroutine to wait for the process and clean up
	go func() {
		_ = cmd.Wait()
		_ = os.Remove(pidFile)

		p.mu.Lock()
		if sub, ok := p.subscribers[sessionID]; ok {
			_ = sub.Unsubscribe()
			delete(p.subscribers, sessionID)
		}
		if pipe, ok := p.stdinPipes[sessionID]; ok {
			_ = pipe.Close()
			delete(p.stdinPipes, sessionID)
		}
		p.mu.Unlock()
	}()

	return nil
}

func (p *NatsProvider) Stop(ctx context.Context, sessionID string, graceful bool) error {
	pidStr, err := p.getPanePID(sessionID)
	if err != nil || pidStr == "" {
		// Session may already be gone, but still clean up resources
	}

	if pidStr != "" {
		if graceful {
			// Try graceful kill first
			killCmd := exec.Command("kill", "-TERM", "-"+pidStr)
			_ = killCmd.Run()
			// Wait a bit for graceful shutdown
			select {
			case <-time.After(5 * time.Second):
				// Fall through to force kill
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		// Kill the process group
		killCmd := exec.Command("kill", "-9", "-"+pidStr)
		_ = killCmd.Run()

		// Also try individual PID just in case
		killCmd = exec.Command("kill", "-9", pidStr)
		_ = killCmd.Run()
	}

	_ = os.Remove(filepath.Join(p.townRoot, ".gt-nats-pids", sessionID))

	// Clean up NATS subscriber and stdin pipe
	p.mu.Lock()
	if sub, ok := p.subscribers[sessionID]; ok {
		_ = sub.Unsubscribe()
		delete(p.subscribers, sessionID)
	}
	if pipe, ok := p.stdinPipes[sessionID]; ok {
		_ = pipe.Close()
		delete(p.stdinPipes, sessionID)
	}
	p.mu.Unlock()

	return nil
}

func (p *NatsProvider) Exists(ctx context.Context, sessionID string) (bool, error) {
	pidStr, err := p.getPanePID(sessionID)
	if err != nil || pidStr == "" {
		return false, nil
	}

	// Check if process exists
	cmd := exec.CommandContext(ctx, "ps", "-p", pidStr, "-o", "pid=")
	err = cmd.Run()
	return err == nil, nil
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
	// Append newline to ensure the command is processed
	if !strings.HasSuffix(data, "\n") {
		data += "\n"
	}

	p.mu.RLock()
	pipe, ok := p.stdinPipes[sessionID]
	p.mu.RUnlock()

	if !ok {
		return fmt.Errorf("session %s not found or stdin not available", sessionID)
	}

	_, err := pipe.Write([]byte(data))
	return err
}

func (p *NatsProvider) GetEnvironment(ctx context.Context, sessionID string) (map[string]string, error) {
	// TODO: Implement environment persistence
	return make(map[string]string), nil
}

func (p *NatsProvider) SetEnvironment(ctx context.Context, sessionID, key, value string) error {
	// For NatsProvider, environment should ideally be set before spawning.
	return nil
}

func (p *NatsProvider) SetRemainOnExit(ctx context.Context, sessionID string, enabled bool) error {
	return nil // Not applicable to direct processes
}

func (p *NatsProvider) Configure(ctx context.Context, sessionID string, cfg any) error {
	return nil // Not applicable
}

func (p *NatsProvider) getPanePID(name string) (string, error) {
	pidFile := filepath.Join(p.townRoot, ".gt-nats-pids", name)
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(string(data)), nil
}
