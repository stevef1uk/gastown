package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultInstalledTownSettings_FreerideDefaults(t *testing.T) {
	t.Parallel()
	ts := DefaultInstalledTownSettings()

	if ts.DefaultAgent != "gt-agent-local" {
		t.Errorf("DefaultAgent = %q, want gt-agent-local", ts.DefaultAgent)
	}
	if ts.SessionTransport != "nats" {
		t.Errorf("SessionTransport = %q, want nats", ts.SessionTransport)
	}
	if ts.RoleAgents["polecat"] != "gt-agent-nvidia" {
		t.Errorf("role_agents[polecat] = %q, want gt-agent-nvidia", ts.RoleAgents["polecat"])
	}
	nvidia := ts.Agents["gt-agent-nvidia"]
	if nvidia == nil {
		t.Fatal("missing gt-agent-nvidia agent")
	}
	if nvidia.Env["LLM_MODEL"] != "nvidia/llama-3.3-nemotron-super-49b-v1" {
		t.Errorf("gt-agent-nvidia LLM_MODEL = %q", nvidia.Env["LLM_MODEL"])
	}
	if nvidia.Env["LLM_ENDPOINT"] != DefaultFreerideProxyEndpoint {
		t.Errorf("gt-agent-nvidia LLM_ENDPOINT = %q", nvidia.Env["LLM_ENDPOINT"])
	}
}

func TestEnsureTownSettingsFile(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	path := TownSettingsPath(tmpDir)

	created, err := EnsureTownSettingsFile(tmpDir)
	if err != nil {
		t.Fatalf("EnsureTownSettingsFile: %v", err)
	}
	if !created {
		t.Fatal("expected created=true on first call")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("settings file missing: %v", err)
	}

	loaded, err := LoadOrCreateTownSettings(path)
	if err != nil {
		t.Fatalf("LoadOrCreateTownSettings: %v", err)
	}
	if loaded.RoleAgents["polecat"] != "gt-agent-nvidia" {
		t.Errorf("loaded polecat role = %q", loaded.RoleAgents["polecat"])
	}

	created, err = EnsureTownSettingsFile(tmpDir)
	if err != nil {
		t.Fatalf("second EnsureTownSettingsFile: %v", err)
	}
	if created {
		t.Fatal("expected created=false when file already exists")
	}

	// Overwrite with sentinel and ensure we do not clobber.
	if err := os.WriteFile(path, []byte(`{"type":"town-settings","version":1,"default_agent":"sentinel"}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	created, err = EnsureTownSettingsFile(tmpDir)
	if err != nil {
		t.Fatalf("third EnsureTownSettingsFile: %v", err)
	}
	if created {
		t.Fatal("expected created=false for existing file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "sentinel") {
		t.Fatalf("EnsureTownSettingsFile clobbered existing file: %s", data)
	}
}

func TestEnsureTownSettingsFile_Path(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	want := filepath.Join(tmpDir, "settings", "config.json")
	_, err := EnsureTownSettingsFile(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("expected %s: %v", want, err)
	}
}
