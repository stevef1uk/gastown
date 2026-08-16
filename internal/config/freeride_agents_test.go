package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultInstalledTownSettings_FreerideDefaults(t *testing.T) {
	t.Setenv("FREERIDE_MODELS_CONFIG", "/nonexistent")
	ts := DefaultInstalledTownSettings()

	if ts.DefaultAgent != "gt-agent-local" {
		t.Errorf("DefaultAgent = %q, want gt-agent-local", ts.DefaultAgent)
	}
	if ts.SessionTransport != "nats" {
		t.Errorf("SessionTransport = %q, want nats", ts.SessionTransport)
	}
	if ts.RoleAgents["polecat"] != "gt-agent-deepseek" {
		t.Errorf("role_agents[polecat] = %q, want gt-agent-deepseek", ts.RoleAgents["polecat"])
	}
	gemini := ts.Agents["gt-agent-gemini"]
	if gemini == nil {
		t.Fatal("missing gt-agent-gemini agent")
	}
	if gemini.Env["LLM_MODEL"] != "google/gemini-3.5-flash" {
		t.Errorf("gt-agent-gemini LLM_MODEL = %q", gemini.Env["LLM_MODEL"])
	}
	if gemini.Env["LLM_ENDPOINT"] != DefaultFreerideProxyEndpoint {
		t.Errorf("gt-agent-gemini LLM_ENDPOINT = %q", gemini.Env["LLM_ENDPOINT"])
	}
	nvidia := ts.Agents["gt-agent-nvidia"]
	if nvidia == nil {
		t.Fatal("missing gt-agent-nvidia agent")
	}
	if nvidia.Env["LLM_MODEL"] != "nvidia/llama-3.3-nemotron-super-49b-v1" {
		t.Errorf("gt-agent-nvidia LLM_MODEL = %q", nvidia.Env["LLM_MODEL"])
	}
	deepseek := ts.Agents["gt-agent-deepseek"]
	if deepseek == nil {
		t.Fatal("missing gt-agent-deepseek agent")
	}
	if deepseek.Env["LLM_MODEL"] != "deepseek/deepseek-v4-flash" {
		t.Errorf("gt-agent-deepseek LLM_MODEL = %q", deepseek.Env["LLM_MODEL"])
	}
	if deepseek.Env["LLM_TIMEOUT"] != "1200s" {
		t.Errorf("gt-agent-deepseek LLM_TIMEOUT = %q", deepseek.Env["LLM_TIMEOUT"])
	}
}

func TestDefaultFreerideRoleAgents_PolecatUsesDeepSeek(t *testing.T) {
	t.Setenv("FREERIDE_MODELS_CONFIG", "/nonexistent")
	roles := DefaultFreerideRoleAgents()
	if roles["polecat"] != "gt-agent-deepseek" {
		t.Fatalf("polecat role agent = %q, want gt-agent-deepseek", roles["polecat"])
	}
	if roles["architect"] != "gt-agent-deepseek" {
		t.Fatalf("architect role agent = %q, want gt-agent-deepseek", roles["architect"])
	}
}

func TestDefaultFreerideAgents_GeminiProfile(t *testing.T) {
	t.Setenv("FREERIDE_MODELS_CONFIG", "/nonexistent")
	agents := DefaultFreerideAgents()
	rc := agents["gt-agent-gemini"]
	if rc == nil {
		t.Fatal("gt-agent-gemini profile missing")
	}
	if rc.Env["LLM_MODEL"] != "google/gemini-3.5-flash" {
		t.Fatalf("LLM_MODEL = %q, want google/gemini-3.5-flash", rc.Env["LLM_MODEL"])
	}
	if rc.Env["LLM_ENDPOINT"] != DefaultFreerideProxyEndpoint {
		t.Fatalf("LLM_ENDPOINT = %q, want %q", rc.Env["LLM_ENDPOINT"], DefaultFreerideProxyEndpoint)
	}
}

func TestEnsureTownSettingsFile(t *testing.T) {
	t.Setenv("FREERIDE_MODELS_CONFIG", "/nonexistent")
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
	if loaded.RoleAgents["polecat"] != "gt-agent-deepseek" {
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
