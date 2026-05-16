package orchestrator

import (
	"testing"

	"github.com/steveyegge/gastown/internal/config"
)

func TestPipelineOnlyEnabled(t *testing.T) {
	t.Setenv("GT_ORCHESTRATOR_PIPELINE_ONLY", "")
	dir := t.TempDir()
	path := config.TownSettingsPath(dir)
	settings := config.NewTownSettings()
	settings.Orchestrator = &config.OrchestratorConfig{PipelineOnly: true}
	if err := config.SaveTownSettings(path, settings); err != nil {
		t.Fatal(err)
	}

	if !PipelineOnlyEnabled(dir, false) {
		t.Fatal("expected settings pipeline_only")
	}
	if !PipelineOnlyEnabled(dir, true) {
		t.Fatal("expected cli flag")
	}

	t.Setenv("GT_ORCHESTRATOR_PIPELINE_ONLY", "1")
	settings.Orchestrator.PipelineOnly = false
	if err := config.SaveTownSettings(path, settings); err != nil {
		t.Fatal(err)
	}
	if !PipelineOnlyEnabled(dir, false) {
		t.Fatal("expected env override")
	}

	t.Setenv("GT_ORCHESTRATOR_PIPELINE_ONLY", "")
	settings.Orchestrator.PipelineOnly = false
	if err := config.SaveTownSettings(path, settings); err != nil {
		t.Fatal(err)
	}
	if PipelineOnlyEnabled(dir, false) {
		t.Fatal("expected disabled")
	}
}
