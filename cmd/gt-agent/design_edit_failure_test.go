package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestDesignFailedEditCannotBeClearedByRead(t *testing.T) {
	town := t.TempDir()
	dir := filepath.Join(town, "sample", "mayor", "rig")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"SPEC.md": "# Specification\n", "architecture.md": "# Architecture\nSome complete content.\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	r := newStateRunner(&orchestrator.Task{State: "design", Hooks: orchestrator.StateHooks{Track: "design", Artifacts: "design"}, Validation: orchestrator.WorkflowValidation{MinArchitectureBytes: 1}}, town, "sample")
	r.track.designArchWritten = true // A previous successful edit does not excuse a later failure.
	trackHandlers["design"](r, "sed -i 'bad replacement' architecture.md", errors.New("syntax error"))
	trackHandlers["design"](r, "cat architecture.md && wc -c architecture.md", nil)
	if err := artifactValidators["design"](r, "success"); err == nil || !strings.Contains(err.Error(), "edit failed") {
		t.Fatalf("read-only command masked failed edit: %v", err)
	}
	trackHandlers["design"](r, "sed -i 's/old/new/' architecture.md", nil)
	if r.track.hadCmdFailure {
		t.Fatal("successful correction did not clear failed-edit state")
	}
}

func TestDesignContractCanReplaceObsoleteProfileIDs(t *testing.T) {
	town := t.TempDir()
	dir := filepath.Join(town, "sample", "mayor", "rig")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	arch := "# Architecture\n## Planned file layout\n- `demo/app.py`\n## Delivery phases\n```yaml\ndelivery_phases:\n  - id: core\n    title: Core\n    required_files: [demo/app.py]\n    qa_verify_command: cd demo && python -c 'import app'\n```\n## Requirements\n### core\nProvide an importable application.\n"
	for name, text := range map[string]string{"SPEC.md": "# Specification\n# Phase 1 — Core\n", "architecture.md": arch} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	r := newStateRunner(&orchestrator.Task{State: "design", Hooks: orchestrator.StateHooks{Track: "design", Artifacts: "design"}, Validation: orchestrator.WorkflowValidation{LayoutRoot: "demo", MinArchitectureBytes: 1, DeliveryPhases: []orchestrator.DeliveryPhase{{ID: "obsolete", RequiredFiles: []string{"demo/app.py"}}}}}, town, "sample")
	r.track.designArchWritten = true
	if err := artifactValidators["design"](r, "success"); err != nil {
		t.Fatalf("obsolete generated IDs blocked corrected SPEC contract: %v", err)
	}
}
