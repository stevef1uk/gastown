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
	child := exec.Command("script", append([]string{"-qfec", args[0]}, args[1:]...)...)
	child.Env = os.Environ()

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
	if err := w.nc.Publish(w.subject, p); err != nil {
		return 0, err
	}
	return len(p), nil
}
