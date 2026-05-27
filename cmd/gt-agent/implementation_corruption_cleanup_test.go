package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestCleanupCorruptedOpenImplementBeadFiles_removesCorruptGoAndKeepsEmptyPy(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	apiDir := filepath.Join(rigDir, "linkshelf", "internal", "api")
	pyDir := filepath.Join(rigDir, "linkshelf", "internal", "worker")
	if err := os.MkdirAll(apiDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pyDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(rigDir, ".beads"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(apiDir, "handlers.go"), []byte(">>>>>>> REPLACE"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pyDir, "__init__.py"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pyDir, "task.py"), []byte("<<<<<<< SEARCH\nprint('x')\n=======\nprint('y')\n>>>>>>> REPLACE\n"), 0644); err != nil {
		t.Fatal(err)
	}

	task := &orchestrator.Task{
		State: "implementation",
		Hooks: orchestrator.StateHooks{Track: "implementation", Artifacts: "implementation"},
		Validation: orchestrator.WorkflowValidation{
			LayoutRoot:                 "linkshelf",
			BeadTitleContains:          "Implement linkshelf/",
			RequiredFiles:              []string{"linkshelf/internal/api/handlers.go", "linkshelf/internal/worker/task.py"},
			MinImplementationFileBytes: 1,
			MinSubstantiveLines:        1,
		},
	}
	r := newStateRunner(task, dir, rig)

	prev := orchestrator.ListImplementBeadsByStatusHook
	orchestrator.ListImplementBeadsByStatusHook = func(_, _ string, _ orchestrator.WorkflowValidation, status string) ([]orchestrator.PlanBead, error) {
		if status == "open" {
			return []orchestrator.PlanBead{
				{ID: "te-go", Title: "Implement linkshelf/internal/api/handlers.go per architecture"},
				{ID: "te-py", Title: "Implement linkshelf/internal/worker/task.py per architecture"},
				{ID: "te-init", Title: "Implement linkshelf/internal/worker/__init__.py per architecture"},
			}, nil
		}
		return nil, nil
	}
	defer func() { orchestrator.ListImplementBeadsByStatusHook = prev }()

	deleted, err := r.cleanupCorruptedOpenImplementBeadFiles()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(deleted, ",")
	if !strings.Contains(joined, "linkshelf/internal/api/handlers.go") || !strings.Contains(joined, "linkshelf/internal/worker/task.py") {
		t.Fatalf("deleted=%v", deleted)
	}
	if _, err := os.Stat(filepath.Join(apiDir, "handlers.go")); !os.IsNotExist(err) {
		t.Fatalf("handlers.go should be deleted, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(pyDir, "task.py")); !os.IsNotExist(err) {
		t.Fatalf("task.py should be deleted, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(pyDir, "__init__.py")); err != nil {
		t.Fatalf("__init__.py (empty marker) should be kept, err=%v", err)
	}
}

