package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBootstrapNATSBroker_noConfig(t *testing.T) {
	if err := BootstrapNATSBroker(t.TempDir()); err != nil {
		t.Fatalf("BootstrapNATSBroker: %v", err)
	}
}

func TestBootstrapNATSBroker_disabled(t *testing.T) {
	tmpDir := t.TempDir()
	mayorDir := filepath.Join(tmpDir, "mayor")
	if err := os.MkdirAll(mayorDir, 0755); err != nil {
		t.Fatal(err)
	}
	configJSON := `{
		"type": "daemon-patrol-config",
		"version": 1,
		"patrols": {
			"nats_server": {"enabled": false}
		}
	}`
	path := filepath.Join(mayorDir, "daemon.json")
	if err := os.WriteFile(path, []byte(configJSON), 0644); err != nil {
		t.Fatal(err)
	}
	if err := BootstrapNATSBroker(tmpDir); err != nil {
		t.Fatalf("BootstrapNATSBroker: %v", err)
	}
}
