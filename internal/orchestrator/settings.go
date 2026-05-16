package orchestrator

import (
	"os"
	"path/filepath"

	"github.com/steveyegge/gastown/internal/config"
)

// TownOrchestratorSettings holds orchestrator options from settings/config.json.
type TownOrchestratorSettings struct {
	DefaultWorkflow string `json:"default_workflow"`
	AutoStart       bool   `json:"auto_start"`
	PipelineOnly    bool   `json:"pipeline_only"`
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
		PipelineOnly:    settings.Orchestrator.PipelineOnly,
	}
}

// PipelineOnlyEnabled reports whether gt up should skip legacy autonomous pipeline
// agents (per-bead polecats, crew restore, town hq-polecat/architect/qa) while the
// orchestrator handles rig-flow. Enabled when CLI flag is set, env
// GT_ORCHESTRATOR_PIPELINE_ONLY is set, or settings orchestrator.pipeline_only is true.
func PipelineOnlyEnabled(townRoot string, cliFlag bool) bool {
	if cliFlag || os.Getenv("GT_ORCHESTRATOR_PIPELINE_ONLY") != "" {
		return true
	}
	return LoadTownOrchestratorSettings(townRoot).PipelineOnly
}
