package natsserver

import (
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"
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

// docker hooks for tests (override in natsserver_test.go).
var (
	dockerInspectRunningFn = dockerInspectRunning
	dockerInspectExistsFn  = dockerInspectExists
	dockerStartFn          = dockerStart
	dockerRunFn            = dockerRun
	dockerStopFn           = dockerStop
	dockerRmFn             = dockerRm
	healthCheckFn          = healthCheckHTTP
)

// EnsureRunning starts the NATS container when it is not running (idempotent).
func EnsureRunning(cfg Config) error {
	if IsRunning() && IsHealthy(cfg) {
		return nil
	}
	return Start(cfg)
}

// Start brings up the NATS server in a Docker container.
// If it's already running and healthy, it does nothing.
// If it exists but is stopped, it starts it (recreates on failed start).
// Otherwise, it runs a new container.
func Start(cfg Config) error {
	if cfg.Port == 0 {
		cfg.Port = 4222
	}
	if cfg.MonitorPort == 0 {
		cfg.MonitorPort = 8222
	}

	if IsRunning() {
		if IsHealthy(cfg) {
			return nil
		}
		_ = Stop()
	}

	if dockerInspectExistsFn() {
		if err := dockerStartFn(); err != nil {
			_ = Stop()
			if err := dockerRunFn(cfg); err != nil {
				return fmt.Errorf("running nats container after recreate: %w", err)
			}
			return waitHealthy(cfg)
		}
		return waitHealthy(cfg)
	}

	if err := dockerRunFn(cfg); err != nil {
		return fmt.Errorf("running nats container: %w", err)
	}
	return waitHealthy(cfg)
}

func waitHealthy(cfg Config) error {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if IsRunning() && IsHealthy(cfg) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !IsRunning() {
		return fmt.Errorf("nats container not running after start")
	}
	return fmt.Errorf("nats health check failed on monitor port %d", cfg.MonitorPort)
}

// IsHealthy reports whether the NATS monitoring endpoint responds OK.
func IsHealthy(cfg Config) bool {
	if cfg.MonitorPort == 0 {
		cfg.MonitorPort = 8222
	}
	return healthCheckFn(cfg.MonitorPort)
}

func healthCheckHTTP(monitorPort int) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", monitorPort))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// Stop terminates and removes the NATS container.
func Stop() error {
	if !dockerInspectExistsFn() {
		return nil
	}
	_ = dockerStopFn()
	if err := dockerRmFn(); err != nil {
		return fmt.Errorf("removing nats container: %w", err)
	}
	return nil
}

// IsRunning returns true if the NATS container is currently running.
func IsRunning() bool {
	return dockerInspectRunningFn()
}

func dockerInspectRunning() bool {
	out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", ContainerName).Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
}

func dockerInspectExists() bool {
	err := exec.Command("docker", "inspect", ContainerName).Run()
	return err == nil
}

func dockerStart() error {
	return exec.Command("docker", "start", ContainerName).Run()
}

func dockerRun(cfg Config) error {
	cmd := exec.Command("docker", "run", "-d",
		"--name", ContainerName,
		"-p", fmt.Sprintf("%d:4222", cfg.Port),
		"-p", fmt.Sprintf("%d:8222", cfg.MonitorPort),
		"nats:latest")
	return cmd.Run()
}

func dockerStop() error {
	return exec.Command("docker", "stop", ContainerName).Run()
}

func dockerRm() error {
	return exec.Command("docker", "rm", ContainerName).Run()
}

// DockerHooks overrides docker/health probes (tests only). Nil fields keep defaults.
type DockerHooks struct {
	InspectRunning func() bool
	InspectExists  func() bool
	Start          func() error
	Run            func(Config) error
	Stop           func() error
	Rm             func() error
	Health         func(monitorPort int) bool
}

// SetDockerHooksForTest installs hooks and returns a restore function.
func SetDockerHooksForTest(h DockerHooks) (restore func()) {
	prev := DockerHooks{
		InspectRunning: dockerInspectRunningFn,
		InspectExists:  dockerInspectExistsFn,
		Start:          dockerStartFn,
		Run:            dockerRunFn,
		Stop:           dockerStopFn,
		Rm:             dockerRmFn,
		Health:         healthCheckFn,
	}
	if h.InspectRunning != nil {
		dockerInspectRunningFn = h.InspectRunning
	}
	if h.InspectExists != nil {
		dockerInspectExistsFn = h.InspectExists
	}
	if h.Start != nil {
		dockerStartFn = h.Start
	}
	if h.Run != nil {
		dockerRunFn = h.Run
	}
	if h.Stop != nil {
		dockerStopFn = h.Stop
	}
	if h.Rm != nil {
		dockerRmFn = h.Rm
	}
	if h.Health != nil {
		healthCheckFn = h.Health
	}
	return func() {
		dockerInspectRunningFn = prev.InspectRunning
		dockerInspectExistsFn = prev.InspectExists
		dockerStartFn = prev.Start
		dockerRunFn = prev.Run
		dockerStopFn = prev.Stop
		dockerRmFn = prev.Rm
		healthCheckFn = prev.Health
	}
}
