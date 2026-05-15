package orchestrator

import (
	"path/filepath"

	"github.com/steveyegge/gastown/internal/config"
)

// TownOrchestratorSettings holds orchestrator options from settings/config.json.
type TownOrchestratorSettings struct {
	DefaultWorkflow string `json:"default_workflow"`
	AutoStart       bool   `json:"auto_start"`
}

// LoadTownOrchestratorSettings reads orchestrator.* from town settings.
func LoadTownOrchestratorSettings(townRoot string) TownOrchestratorSettings {
	path := filepath.Join(townRoot, "settings", "config.json")
	settings, err := config.LoadOrCreateTownSettings(path)
	if err != nil || settings == nil || settings.Orchestrator == nil {
		return TownOrchestratorSettings{}
	}
	return TownOrchestratorSettings{
		DefaultWorkflow: settings.Orchestrator.DefaultWorkflow,
		AutoStart:       settings.Orchestrator.AutoStart,
	}
}
