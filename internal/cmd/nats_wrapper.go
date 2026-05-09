package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/nats-io/nats.go"
	"github.com/spf13/cobra"
)

var (
	natsWrapperSessionID string
	natsWrapperURL       string
)

var natsWrapperCmd = &cobra.Command{
	Use:    "nats-wrapper --session <id> -- <command>",
	Short:  "Wrap a process with NATS input/output (internal)",
	Hidden: true,
	RunE:   runNatsWrapper,
}

func init() {
	natsWrapperCmd.Flags().StringVar(&natsWrapperSessionID, "session", "", "Session ID for NATS subjects")
	natsWrapperCmd.Flags().StringVar(&natsWrapperURL, "nats-url", nats.DefaultURL, "NATS server URL")
	rootCmd.AddCommand(natsWrapperCmd)
}

func runNatsWrapper(cmd *cobra.Command, args []string) error {
	if natsWrapperSessionID == "" {
		return fmt.Errorf("--session is required")
	}
	if len(args) == 0 {
		return fmt.Errorf("command is required")
	}

	nc, err := nats.Connect(natsWrapperURL)
	if err != nil {
		return fmt.Errorf("connecting to NATS: %w", err)
	}
	defer nc.Close()

	// Set up command — wrap with script(1) to provide a PTY for agents like
	// Claude Code that require a terminal.
	//
	// CRITICAL: The Go process may have an empty PATH (observed on some platforms
	// where the parent shell doesn't export PATH to child processes). We must
	// explicitly search common bin directories for script(1) instead of relying
	// on exec.LookPath, which uses the process's PATH.
	scriptPath := findScriptBinary()
	child := exec.Command(scriptPath, append([]string{"-qfec", args[0]}, args[1:]...)...)
	child.Env = buildChildEnv()

	stdin, err := child.StdinPipe()
	if err != nil {
		return fmt.Errorf("creating stdin pipe: %w", err)
	}

	// Logging subject
	logSubject := fmt.Sprintf("gt.session.%s.log", natsWrapperSessionID)
	
	// Create a multi-writer for stdout/stderr to both NATS and local output
	natsLogWriter := &natsWriter{nc: nc, subject: logSubject}
	child.Stdout = io.MultiWriter(os.Stdout, natsLogWriter)
	child.Stderr = io.MultiWriter(os.Stderr, natsLogWriter)

	// Subscribe to input subject
	inputSubject := fmt.Sprintf("gt.session.%s.input", natsWrapperSessionID)
	sub, err := nc.Subscribe(inputSubject, func(msg *nats.Msg) {
		data := msg.Data
		// Ensure newline if needed? NatsProvider adds it, but let's be safe
		_, _ = stdin.Write(data)
	})
	if err != nil {
		return fmt.Errorf("subscribing to NATS: %w", err)
	}
	defer sub.Unsubscribe()

	// Handle signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	if err := child.Start(); err != nil {
		return fmt.Errorf("starting child: %w", err)
	}

	// Forward signals to child
	go func() {
		for sig := range sigChan {
			_ = child.Process.Signal(sig)
		}
	}()

	// Wait for child to exit
	err = child.Wait()

	// Notify NATS that session is finished
	_ = nc.Publish(fmt.Sprintf("gt.session.%s.exit", natsWrapperSessionID), []byte(fmt.Sprintf("%v", err)))
	_ = nc.Flush()

	return err
}

type natsWriter struct {
	nc      *nats.Conn
	subject string
}

func (w *natsWriter) Write(p []byte) (n int, err error) {
	// Best-effort publish to NATS. If it fails, we still return success to the
	// caller (the child process) so that it doesn't crash on a broken pipe.
	// The local os.Stdout/os.Stderr (which goes to the wrapper log file)
	// will still be written by the MultiWriter.
	_ = w.nc.Publish(w.subject, p)
	return len(p), nil
}

// findScriptBinary searches for the script(1) binary in common system
// directories. This avoids relying on PATH, which may be empty in the Go
// process when started from certain environments.
func findScriptBinary() string {
	candidates := []string{
		"/usr/bin/script",
		"/bin/script",
		"/usr/local/bin/script",
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	// Fallback: let exec.Command resolve it via PATH (will likely fail if
	// PATH is empty, but preserves the original behavior as last resort).
	return "script"
}

// buildChildEnv returns the environment for the child process. If the current
// process has an empty PATH, it injects a sensible default so that common
// binaries (bash, env, etc.) can be found by the child.
func buildChildEnv() []string {
	env := os.Environ()
	hasPath := false
	for _, e := range env {
		if len(e) >= 5 && e[:5] == "PATH=" {
			hasPath = true
			break
		}
	}
	if !hasPath {
		env = append(env, "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")
	}

	// Inject session ID so that gt prime (and others) can find the current session
	// without relying on tmux-specific environment detection.
	env = append(env, "GT_SESSION_ID="+natsWrapperSessionID)

	return env
}
