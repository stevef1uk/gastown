package agentconsole

import (
	"testing"
)

func TestResolveListenConfig_defaults(t *testing.T) {
	t.Setenv("GT_AGENT_CONSOLE_PORT", "")
	t.Setenv("GT_AGENT_CONSOLE_BIND", "")
	cfg, err := ResolveListenConfig(0, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != DefaultPort {
		t.Fatalf("port = %d want %d", cfg.Port, DefaultPort)
	}
	if cfg.Bind != DefaultBind {
		t.Fatalf("bind = %q want %q", cfg.Bind, DefaultBind)
	}
	if cfg.URL() != "http://127.0.0.1:8091" {
		t.Fatalf("url = %q", cfg.URL())
	}
}

func TestResolveListenConfig_envAndFlags(t *testing.T) {
	t.Setenv("GT_AGENT_CONSOLE_PORT", "9000")
	t.Setenv("GT_AGENT_CONSOLE_BIND", "0.0.0.0")
	cfg, err := ResolveListenConfig(8091, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 8091 {
		t.Fatalf("port = %d want 8091 (flag wins)", cfg.Port)
	}
	if cfg.Bind != "127.0.0.1" {
		t.Fatalf("bind = %q want 127.0.0.1 (flag wins)", cfg.Bind)
	}
}

func TestResolveListenConfig_invalidEnv(t *testing.T) {
	t.Setenv("GT_AGENT_CONSOLE_PORT", "nope")
	_, err := ResolveListenConfig(0, "")
	if err == nil {
		t.Fatal("expected error for invalid env port")
	}
}
