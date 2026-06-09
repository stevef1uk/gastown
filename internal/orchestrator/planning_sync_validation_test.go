package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidationForPlanningSync_usesProfileNotRuntimeFlatPaths(t *testing.T) {
	dir := t.TempDir()
	rig := "testrig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig", ".gastown")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	profile := `{
  "version": 1,
  "validation": {
    "layout_root": "linkshelf",
    "bead_title_contains": "Implement linkshelf/",
    "required_files": [
      "linkshelf/internal/api/handlers.go",
      "linkshelf/internal/store/store.go"
    ]
  }
}`
	if err := os.WriteFile(filepath.Join(rigDir, "workflow-profile.json"), []byte(profile), 0644); err != nil {
		t.Fatal(err)
	}
	runtime := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		RequiredFiles: []string{"linkshelf/handlers.go", "linkshelf/store_test.go"},
	}
	got := ValidationForPlanningSync(dir, rig, runtime)
	if len(got.RequiredFiles) != 2 {
		t.Fatalf("RequiredFiles len=%d want 2 from profile", len(got.RequiredFiles))
	}
	if got.RequiredFiles[0] != "linkshelf/internal/api/handlers.go" {
		t.Fatalf("got %q want profile nested path", got.RequiredFiles[0])
	}
}
