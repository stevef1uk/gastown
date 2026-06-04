package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunWorkflowStuckRepair_patchesIntegrationContract(t *testing.T) {
	dir := t.TempDir()
	rig := "testrig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	gastownDir := filepath.Join(rigDir, ".gastown")
	if err := os.MkdirAll(gastownDir, 0755); err != nil {
		t.Fatal(err)
	}
	profile := `{
  "version": 1,
  "validation": {
    "layout_root": "linkshelf",
    "bead_title_contains": "Implement linkshelf/",
    "required_files": ["linkshelf/cmd/server/main.go"]
  }
}`
	if err := os.WriteFile(filepath.Join(gastownDir, "workflow-profile.json"), []byte(profile), 0644); err != nil {
		t.Fatal(err)
	}
	spec := "| GET | /api/links | 200 | — |\n"
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	plan := "# Implementation plan\n\n## Bead map\n\n### te-1: linkshelf/cmd/server/main.go\n"
	if err := os.WriteFile(filepath.Join(rigDir, "plan.md"), []byte(plan), 0644); err != nil {
		t.Fatal(err)
	}

	v := ValidationForPlanningSync(dir, rig, DefaultWorkflowValidation())
	log, err := RunWorkflowStuckRepair(dir, rig, v, []WorkflowStuckSignal{SignalMissingIntegrationContract})
	if err != nil {
		t.Fatal(err)
	}
	if log == nil {
		t.Fatal("expected repair log")
	}
	data, err := os.ReadFile(filepath.Join(rigDir, "plan.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "## Integration contract") {
		t.Fatalf("plan.md not patched:\n%s", data)
	}
}
