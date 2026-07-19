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
	
	// Try default locations
	for _, p := range []string{
		"gastown/models.json",
		filepath.Join(os.Getenv("HOME"), "gt", "gastown", "models.json"),
	} {
		if cfg := loadModelsConfig(p); cfg != nil {
			modelsConfig = cfg
			modelsConfigPath = p
			return modelsConfig
		}
	}
	
	// Return defaults if no config found
	modelsConfig = &ModelsConfig{
		Models: map[string]string{
			"judge":      "deepseek/deepseek-v4-flash",
			"architect":  "deepseek/deepseek-v4-flash",
			"planner":    "google/gemini-3.5-flash",
			"polecat":    "deepseek/deepseek-v4-flash",
			"qa":         "google/gemini-3.5-flash",
			"mayor":      "google/gemini-3.5-flash",
			"default":    "google/gemini-3.5-flash",
		},
		AgentModels: map[string]string{
			"gt-agent-local":    "google/gemini-3.5-flash",
			"gt-agent-gemini":   "google/gemini-3.5-flash",
			"gt-agent-deepseek": "deepseek/deepseek-v4-flash",
		},
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