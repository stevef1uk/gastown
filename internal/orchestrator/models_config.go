package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
)

var (
	modelsConfig *ModelsConfig
	modelsConfigPath string
)

// ModelsConfig holds the LLM model configuration
type ModelsConfig struct {
	Models      map[string]string `json:"models"`
	AgentModels map[string]string `json:"agentModels"`
}

// GetModelsConfig returns the models configuration, loading from file if not cached
func GetModelsConfig() *ModelsConfig {
	if modelsConfig != nil {
		return modelsConfig
	}
	
	// Check environment variable first
	if p := os.Getenv("GASTOWN_MODELS_CONFIG"); p != "" {
		if cfg := loadModelsConfig(p); cfg != nil {
			modelsConfig = cfg
			modelsConfigPath = p
			return modelsConfig
		}
	}
	
	// Try default locations — all file-based, no hard-coded model map.
	// Order matters: project-local overrides win over town/global.
	// Keep this list in sync with gastown/internal/config/models.json
	// (the version-controlled defaults) and gastown/models.json
	// (per-repo overrides).
	candidates := []string{
		"gastown/models.json",
		"models.json",
		"gastown/internal/config/models.json",
		"internal/config/models.json",
	}
	if home, _ := os.UserHomeDir(); home != "" {
		candidates = append(candidates,
			filepath.Join(home, "gt", "gastown", "models.json"),
			filepath.Join(home, "gt", "settings", "config.json"),
		)
	}
	for _, p := range candidates {
		if cfg := loadModelsConfig(p); cfg != nil {
			// Only accept files that actually define models; empty files fall through
			// to the next candidate instead of masking a richer default.
			if len(cfg.Models) > 0 || len(cfg.AgentModels) > 0 {
				modelsConfig = cfg
				modelsConfigPath = p
				return modelsConfig
			}
		}
	}
	
	// No config file found — return empty config so caller can handle
	// missing model explicitly instead of silently using hard-coded fallbacks.
	// Models must be configured via gastown config file (gastown/models.json
	// or $HOME/gt/gastown/models.json, or GASTOWN_MODELS_CONFIG env var);
	// see internal/config/models.json for the version-controlled defaults.
	modelsConfig = &ModelsConfig{
		Models:      map[string]string{},
		AgentModels: map[string]string{},
	}
	return modelsConfig
}

func loadModelsConfig(path string) *ModelsConfig {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var cfg ModelsConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil
	}
	return &cfg
}

// GetModel returns the model for a given role
func GetModel(role string) string {
	cfg := GetModelsConfig()
	if model, ok := cfg.Models[role]; ok {
		return model
	}
	return cfg.Models["default"]
}

// GetAgentModel returns the model for a given agent
func GetAgentModel(agent string) string {
	cfg := GetModelsConfig()
	if model, ok := cfg.AgentModels[agent]; ok {
		return model
	}
	return cfg.Models["default"]
}