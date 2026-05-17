// Package agentllm holds shared LLM endpoint defaults for gt-agent and dev tools.
package agentllm

import (
	"net"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	// DefaultEndpoint matches gt-agent when LLM_ENDPOINT is unset (local freeride / Ollama proxy).
	DefaultEndpoint = "http://localhost:11434/v1/chat/completions"
	// DefaultModel matches gt-agent's fallback when LLM_MODEL is unset.
	DefaultModel = "meta-llama/llama-3.2-3b-instruct:free"
)

// ResolveEndpoint returns LLM_ENDPOINT or the local proxy default.
func ResolveEndpoint() string {
	if e := strings.TrimSpace(os.Getenv("LLM_ENDPOINT")); e != "" {
		return e
	}
	return DefaultEndpoint
}

// ResolveModel returns LLM_MODEL or the shared default.
func ResolveModel() string {
	if m := strings.TrimSpace(os.Getenv("LLM_MODEL")); m != "" {
		return m
	}
	return DefaultModel
}

// ResolveTimeout parses LLM_TIMEOUT or returns fallback.
func ResolveTimeout(fallback time.Duration) time.Duration {
	if s := strings.TrimSpace(os.Getenv("LLM_TIMEOUT")); s != "" {
		if d, err := time.ParseDuration(s); err == nil && d > 0 {
			return d
		}
	}
	return fallback
}

// IsLocalProxy reports whether endpoint targets a loopback LLM proxy (freeride, Ollama, etc.).
func IsLocalProxy(endpoint string) bool {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}

// AuthToken returns an API token from the environment, if any.
func AuthToken() string {
	for _, key := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return ""
}

// RequiresAuthToken is true for remote endpoints that need a real provider key.
func RequiresAuthToken(endpoint string) bool {
	return !IsLocalProxy(endpoint) && AuthToken() == ""
}
