package config

import (
	"encoding/json"
	"os"
)

// DefaultFreerideProxyEndpoint is the OpenAI-compatible URL for a local Freeride proxy.
const DefaultFreerideProxyEndpoint = "http://localhost:11434/v1/chat/completions"

// FreerideAgentModel maps an agent name to its LLM model and optional extra env vars.
type FreerideAgentModel struct {
	Model    string            `json:"model"`
	ExtraEnv map[string]string `json:"extra_env,omitempty"`
}

// FreerideModelsConfig holds the complete configuration for freeride agent models.
type FreerideModelsConfig struct {
	Agents     map[string]*FreerideAgentModel `json:"agents"`
	RoleAgents map[string]string              `json:"role_agents"`
}

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

// DefaultFreerideModelsConfigPath returns the path for the freeride models config file.
// Checks FREERIDE_MODELS_CONFIG env var first; defaults to gastown/freeride_models.json.
func DefaultFreerideModelsConfigPath() string {
	if p := os.Getenv("FREERIDE_MODELS_CONFIG"); p != "" {
		return p
	}
	return "gastown/freeride_models.json"
}

// loadFreerideModelsConfig loads freeride agent model configuration from a JSON file.
// Returns nil if the file does not exist or cannot be parsed.
func loadFreerideModelsConfig(path string) *FreerideModelsConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg FreerideModelsConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	return &cfg
}

// buildAgentsFromConfig converts a FreerideModelsConfig into the RuntimeConfig map.
func buildAgentsFromConfig(cfg *FreerideModelsConfig) map[string]*RuntimeConfig {
	agents := make(map[string]*RuntimeConfig, len(cfg.Agents))
	for name, am := range cfg.Agents {
		env := map[string]string{
			"LLM_ENDPOINT": DefaultFreerideProxyEndpoint,
			"LLM_MODEL":    am.Model,
		}
		for k, v := range am.ExtraEnv {
			env[k] = v
		}
		agents[name] = &RuntimeConfig{
			Command: "gt-agent",
			Args:    []string{},
			Env:     env,
		}
	}
	return agents
}

// DefaultFreerideAgents returns named gt-agent profiles routed through Freeride.
// Loads model configuration from gastown/freeride_models.json (or FREERIDE_MODELS_CONFIG env var),
// falling back to compiled-in defaults if the file is not available.
func DefaultFreerideAgents() map[string]*RuntimeConfig {
	if cfg := loadFreerideModelsConfig(DefaultFreerideModelsConfigPath()); cfg != nil && len(cfg.Agents) > 0 {
		return buildAgentsFromConfig(cfg)
	}

	return map[string]*RuntimeConfig{
		"gt-agent-local":     freerideGTAgent("google/gemini-3.5-flash", nil),
		"gt-agent-nvidia":    freerideGTAgent("nvidia/llama-3.3-nemotron-super-49b-v1", nil),
		"gt-agent-gemini":    freerideGTAgent("google/gemini-3.5-flash", map[string]string{"LLM_TIMEOUT": "1200s"}),
		"gt-agent-powerful":  freerideGTAgent("ollama/llama3.3", nil),
		"gt-agent-fast":      freerideGTAgent("ollama/ministral-3:8b", nil),
		"gt-agent-mayor-fast": freerideGTAgent("ollama/ministral-3:8b", map[string]string{
			"LLM_TURN_TIMEOUT": "30s",
			"GT_AGENT_CMD_TIMEOUT": "75s",
		}),
	}
}

// DefaultFreerideRoleAgents maps Gas Town roles to Freeride-backed gt-agent profiles.
// Loads role mappings from gastown/freeride_models.json (or FREERIDE_MODELS_CONFIG env var),
// falling back to compiled-in defaults if the file is not available.
func DefaultFreerideRoleAgents() map[string]string {
	if cfg := loadFreerideModelsConfig(DefaultFreerideModelsConfigPath()); cfg != nil && cfg.RoleAgents != nil {
		return cfg.RoleAgents
	}

	return map[string]string{
		"architect": "gt-agent-gemini",
		"crew":      "gt-agent-local",
		"deacon":    "gt-agent-local",
		"mechanic":  "gt-agent-local",
		"mayor":     "gt-agent-local",
		"planner":   "gt-agent-local",
		"setup":     "gt-agent-local",
		"polecat":   "gt-agent-gemini",
		"qa":        "gt-agent-gemini",
		"refinery":  "gt-agent-local",
		"witness":   "gt-agent-local",
	}
}
