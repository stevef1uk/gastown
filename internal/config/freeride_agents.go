package config

// DefaultFreerideProxyEndpoint is the OpenAI-compatible URL for a local Freeride proxy.
const DefaultFreerideProxyEndpoint = "http://localhost:11434/v1/chat/completions"

func freerideGTAgent(model string, extra map[string]string) *RuntimeConfig {
	env := map[string]string{
		"LLM_ENDPOINT": DefaultFreerideProxyEndpoint,
		"LLM_MODEL":    model,
	}
	for k, v := range extra {
		env[k] = v
	}
	return &RuntimeConfig{
		Command: "gt-agent",
		Args:    []string{},
		Env:     env,
	}
}

// DefaultFreerideAgents returns named gt-agent profiles routed through Freeride.
func DefaultFreerideAgents() map[string]*RuntimeConfig {
	return map[string]*RuntimeConfig{
		"gt-agent-local": freerideGTAgent("meta/llama-3.3-70b-instruct", nil),
		"gt-agent-nvidia": freerideGTAgent("nvidia/llama-3.3-nemotron-super-49b-v1", nil),
		"gt-agent-powerful": freerideGTAgent("ollama/llama3.3", nil),
		"gt-agent-fast": freerideGTAgent("ollama/ministral-3:8b", nil),
		"gt-agent-mayor-fast": freerideGTAgent("ollama/ministral-3:8b", map[string]string{
			"LLM_TURN_TIMEOUT":     "30s",
			"GT_AGENT_CMD_TIMEOUT": "75s",
		}),
	}
}

// DefaultFreerideRoleAgents maps Gas Town roles to Freeride-backed gt-agent profiles.
func DefaultFreerideRoleAgents() map[string]string {
	return map[string]string{
		"architect": "gt-agent-powerful",
		"crew":      "gt-agent-local",
		"deacon":    "gt-agent-local",
		"mechanic":  "gt-agent-local",
		"mayor":     "gt-agent-local",
		"planner":   "gt-agent-local",
		"setup":     "gt-agent-local",
		"polecat":   "gt-agent-nvidia",
		"qa":        "gt-agent-local",
		"refinery":  "gt-agent-local",
		"witness":   "gt-agent-local",
	}
}
