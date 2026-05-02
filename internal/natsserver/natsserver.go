package natsserver

import (
	"fmt"
	"os/exec"
	"strings"
)

// ContainerName is the canonical name for the Gas Town NATS container.
const ContainerName = "gt-nats"

// Config holds NATS server configuration.
type Config struct {
	// Port is the NATS client port (default 4222).
	Port int

	// MonitorPort is the NATS monitoring port (default 8222).
	MonitorPort int
}

// Start brings up the NATS server in a Docker container.
// If it's already running, it does nothing.
// If it exists but is stopped, it starts it.
// Otherwise, it runs a new container.
func Start(cfg Config) error {
	if cfg.Port == 0 {
		cfg.Port = 4222
	}
	if cfg.MonitorPort == 0 {
		cfg.MonitorPort = 8222
	}

	if IsRunning() {
		return nil
	}

	if exists() {
		if err := exec.Command("docker", "start", ContainerName).Run(); err != nil {
			return fmt.Errorf("starting existing nats container: %w", err)
		}
		return nil
	}

	// Pull and run the latest NATS image.
	// -p <host>:<container>
	cmd := exec.Command("docker", "run", "-d",
		"--name", ContainerName,
		"-p", fmt.Sprintf("%d:4222", cfg.Port),
		"-p", fmt.Sprintf("%d:8222", cfg.MonitorPort),
		"nats:latest")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("running nats container: %w", err)
	}

	return nil
}

// Stop terminates and removes the NATS container.
func Stop() error {
	if !exists() {
		return nil
	}

	// Stop gracefully (ignore error if already stopped)
	_ = exec.Command("docker", "stop", ContainerName).Run()

	// Remove the container
	if err := exec.Command("docker", "rm", ContainerName).Run(); err != nil {
		return fmt.Errorf("removing nats container: %w", err)
	}

	return nil
}

// IsRunning returns true if the NATS container is currently running.
func IsRunning() bool {
	out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", ContainerName).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

func exists() bool {
	err := exec.Command("docker", "inspect", ContainerName).Run()
	return err == nil
}
