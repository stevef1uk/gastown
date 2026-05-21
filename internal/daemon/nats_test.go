package daemon

import (
	"bytes"
	"log"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/natsserver"
)

func TestNatsServerManager_EnsureRunning_disabled(t *testing.T) {
	m := NewNatsServerManager(t.TempDir(), &NatsServerConfig{Enabled: false}, nil)
	if err := m.EnsureRunning(); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureNatsServerRunning_logsStartWhenWasDown(t *testing.T) {
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	m := NewNatsServerManager(t.TempDir(), &NatsServerConfig{Enabled: true, Port: 4222, MonitorPort: 8222}, logger.Printf)
	d := &Daemon{natsServer: m, logger: logger}

	running := false
	restore := natsserver.SetDockerHooksForTest(natsserver.DockerHooks{
		InspectRunning: func() bool { return running },
		InspectExists:  func() bool { return false },
		Run: func(natsserver.Config) error {
			running = true
			return nil
		},
		Health: func(int) bool { return true },
	})
	defer restore()

	d.ensureNatsServerRunning()
	if !strings.Contains(buf.String(), "NATS server started") {
		t.Fatalf("expected start log, got %q", buf.String())
	}
}
