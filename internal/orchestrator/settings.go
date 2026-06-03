package orchestrator

import (
	"os"
	"path/filepath"

	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/constants"
)

const pipelineOnlyMarkerFile = "pipeline-only-marker"

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

// PipelineOnlyMarkerPath is the marker file gt up --orchestrator-only writes so the
// daemon suppresses patrol LLM agents until gt down clears it.
func PipelineOnlyMarkerPath(townRoot string) string {
	return filepath.Join(townRoot, constants.DirMayor, ".gastown", pipelineOnlyMarkerFile)
}

// SetPipelineOnlyMarker records or clears CLI pipeline-only mode for the daemon.
func SetPipelineOnlyMarker(townRoot string, active bool) error {
	path := PipelineOnlyMarkerPath(townRoot)
	if !active {
		err := os.Remove(path)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("1\n"), 0644)
}

func pipelineOnlyMarkerActive(townRoot string) bool {
	_, err := os.Stat(PipelineOnlyMarkerPath(townRoot))
	return err == nil
}

// PipelineOnlyEnabled reports whether gt up and the daemon should skip legacy
// autonomous pipeline and patrol LLM agents while the orchestrator runs rig-flow.
// Enabled when CLI flag is set, env GT_ORCHESTRATOR_PIPELINE_ONLY is set,
// settings orchestrator.pipeline_only is true, or the CLI marker file exists.
func PipelineOnlyEnabled(townRoot string, cliFlag bool) bool {
	if cliFlag || os.Getenv("GT_ORCHESTRATOR_PIPELINE_ONLY") != "" {
		return true
	}
	if LoadTownOrchestratorSettings(townRoot).PipelineOnly {
		return true
	}
	return pipelineOnlyMarkerActive(townRoot)
}

// SuppressPatrolAgents reports whether deacon/boot/mechanic/witness/refinery should
// stay stopped while the orchestrator is active. Requires pipeline-only mode and a
// running orchestrator (daemon heartbeat path).
func SuppressPatrolAgents(townRoot string) bool {
	running, _, _ := IsRunning(townRoot)
	return running && PipelineOnlyEnabled(townRoot, false)
}
