package daemon

import (
	"fmt"

	"github.com/steveyegge/gastown/internal/natsserver"
)

// NatsServerManager manages the NATS server lifecycle.
type NatsServerManager struct {
	townRoot string
	config   *NatsServerConfig
	log      func(format string, v ...any)
}

// NewNatsServerManager creates a new NATS server manager.
func NewNatsServerManager(townRoot string, config *NatsServerConfig, log func(format string, v ...any)) *NatsServerManager {
	if config == nil {
		config = &NatsServerConfig{}
	}
	// Defaults
	if config.Port == 0 {
		config.Port = 4222
	}
	if config.MonitorPort == 0 {
		config.MonitorPort = 8222
	}
	return &NatsServerManager{
		townRoot: townRoot,
		config:   config,
		log:      log,
	}
}

// IsEnabled returns true if NATS server management is enabled.
func (m *NatsServerManager) IsEnabled() bool {
	return m.config != nil && m.config.Enabled
}

// Start ensures the NATS server is running (daemon boot).
func (m *NatsServerManager) Start() error {
	return m.EnsureRunning()
}

// EnsureRunning starts or recreates the NATS container when it is down or unhealthy.
// Called on each daemon recovery heartbeat (same role as ensureDoltServerRunning).
func (m *NatsServerManager) EnsureRunning() error {
	if !m.IsEnabled() {
		return nil
	}
	cfg := m.serverConfig()
	if err := natsserver.EnsureRunning(cfg); err != nil {
		return fmt.Errorf("starting nats server: %w", err)
	}
	return nil
}

func (m *NatsServerManager) serverConfig() natsserver.Config {
	return natsserver.Config{
		Port:        m.config.Port,
		MonitorPort: m.config.MonitorPort,
	}
}

// Stop ensures the NATS server is stopped.
func (m *NatsServerManager) Stop() error {
	if !m.IsEnabled() {
		return nil
	}

	if err := natsserver.Stop(); err != nil {
		return fmt.Errorf("stopping nats server: %w", err)
	}

	return nil
}

// IsRunning returns true if the NATS server is currently running.
func (m *NatsServerManager) IsRunning() bool {
	return natsserver.IsRunning()
}
