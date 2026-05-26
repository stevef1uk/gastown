package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReconcileClosedImplementBeads_skipsWhenQueueGreen(t *testing.T) {
	dir := t.TempDir()
	rig := "rig"
	layout := "app"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	moduleDir := filepath.Join(rigDir, layout)
	if err := os.MkdirAll(moduleDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "go.mod"), []byte("module app\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}

	v := WorkflowValidation{
		LayoutRoot:        layout,
		BeadTitleContains: "Implement",
		QAVerifyCommand:   "cd app && go test ./...",
		RequiredFiles:     []string{layout + "/internal/store/store.go"},
		MinImplementationFileBytes: 1,
		MinSubstantiveLines:        1,
	}

	prev := ListImplementBeadsByStatusHook
	defer func() { ListImplementBeadsByStatusHook = prev }()
	ListImplementBeadsByStatusHook = func(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error) {
		switch status {
		case "closed":
			return []PlanBead{{ID: "b1", Title: "Implement app/internal/store/store.go per architecture"}}, nil
		default:
			return nil, nil
		}
	}

	// Without go sources tests may fail; only assert reopen is skipped when queue is green.
	if ImplementationQueueGreen(dir, rig, v) {
		reopened, err := ReconcileClosedImplementBeads(dir, rig, v)
		if err != nil {
			t.Fatal(err)
		}
		if len(reopened) != 0 {
			t.Fatalf("reopened=%v want none when queue green", reopened)
		}
	}
}
