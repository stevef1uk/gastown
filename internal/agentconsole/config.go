package agentconsole

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// DefaultPort is the HTTP listen port for gt-agent-console when not overridden.
const DefaultPort = 8091

// DefaultBind is the HTTP bind address for gt-agent-console when not overridden.
const DefaultBind = "127.0.0.1"

// ListenConfig holds the resolved HTTP listen address for the agent console.
type ListenConfig struct {
	Bind string
	Port int
}

// Addr returns host:port for http.Server.Addr.
func (c ListenConfig) Addr() string {
	return fmt.Sprintf("%s:%d", c.Bind, c.Port)
}

// URL returns the user-facing base URL (http://host:port).
func (c ListenConfig) URL() string {
	return fmt.Sprintf("http://%s", c.Addr())
}

// ResolveListenConfig reads bind/port from environment variables and optional CLI overrides.
// Precedence: explicit CLI values (non-zero port / non-empty bind when passed via flags) win over env;
// env wins over defaults. Pass port=0 and bind="" when a flag was not set on the command line.
func ResolveListenConfig(portFlag int, bindFlag string) (ListenConfig, error) {
	cfg := ListenConfig{Bind: DefaultBind, Port: DefaultPort}
	if b := strings.TrimSpace(os.Getenv("GT_AGENT_CONSOLE_BIND")); b != "" {
		cfg.Bind = b
	}
	if p := strings.TrimSpace(os.Getenv("GT_AGENT_CONSOLE_PORT")); p != "" {
		v, err := strconv.Atoi(p)
		if err != nil || v <= 0 || v > 65535 {
			return ListenConfig{}, fmt.Errorf("invalid GT_AGENT_CONSOLE_PORT %q", p)
		}
		cfg.Port = v
	}
	if bindFlag != "" {
		cfg.Bind = bindFlag
	}
	if portFlag > 0 {
		cfg.Port = portFlag
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		return ListenConfig{}, fmt.Errorf("invalid agent console port %d", cfg.Port)
	}
	if strings.TrimSpace(cfg.Bind) == "" {
		return ListenConfig{}, fmt.Errorf("agent console bind address is empty")
	}
	return cfg, nil
}
