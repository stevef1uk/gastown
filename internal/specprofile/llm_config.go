package specprofile

import (
	"os"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/config"
)

// ResolveLLMForSpecIndex picks LLM endpoint/model for gt rig spec-index.
// Shell env wins when set; otherwise uses town settings (architect → planner → mayor)
// Freeride gt-agent env; then Freeride defaults (not bare "llama3").
func ResolveLLMForSpecIndex(townRoot string) (endpoint, model string) {
	endpoint = strings.TrimSpace(os.Getenv("LLM_ENDPOINT"))
	model = strings.TrimSpace(os.Getenv("LLM_MODEL"))
	if endpoint != "" && model != "" {
		return endpoint, model
	}

	for _, role := range []string{"architect", "planner", "mayor"} {
		rc := config.ResolveRoleAgentConfig(role, townRoot, "")
		if rc == nil || rc.Env == nil {
			continue
		}
		if endpoint == "" {
			endpoint = strings.TrimSpace(rc.Env["LLM_ENDPOINT"])
		}
		if model == "" {
			model = strings.TrimSpace(rc.Env["LLM_MODEL"])
		}
		if endpoint != "" && model != "" {
			return endpoint, model
		}
	}

	if endpoint == "" {
		endpoint = config.DefaultFreerideProxyEndpoint
	}
	if model == "" {
		if agents := config.DefaultFreerideAgents(); agents != nil {
			if rc := agents["gt-agent-powerful"]; rc != nil && rc.Env != nil {
				model = strings.TrimSpace(rc.Env["LLM_MODEL"])
			}
		}
		if model == "" {
			model = "ollama/llama3.3"
		}
	}
	return endpoint, model
}

// HTTPTimeoutForSpecIndex returns per-request HTTP timeout (default 5m for large SPEC JSON).
func HTTPTimeoutForSpecIndex() time.Duration {
	raw := strings.TrimSpace(os.Getenv("GT_SPEC_INDEX_HTTP_TIMEOUT"))
	if raw == "" {
		return 5 * time.Minute
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 5 * time.Minute
	}
	return d
}
