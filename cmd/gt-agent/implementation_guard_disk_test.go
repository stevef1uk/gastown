package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestRejectImplementationSuccessWithoutDisk_blocksWhenFileMissing(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	beadPath := "linkshelf/internal/api/handlers.go"
	if err := os.MkdirAll(filepath.Join(rigDir, "linkshelf", "internal", "api"), 0755); err != nil {
		t.Fatal(err)
	}

	task := &orchestrator.Task{
		WorkflowID: "wf-1",
		State:      "implementation",
		Hooks:      orchestrator.StateHooks{Track: "implementation", Artifacts: "implementation"},
		Validation: orchestrator.WorkflowValidation{
			LayoutRoot:                 "linkshelf",
			BeadTitleContains:          "Implement linkshelf/",
			RequiredFiles:              []string{beadPath},
			MinImplementationFileBytes: 1,
			MinSubstantiveLines:        1,
		},
	}
	r := newStateRunner(task, dir, rig)
	r.track.activeBead = "te-xhq"
	r.track.activeBeadPath = beadPath

	msg, reject := r.rejectImplementationSuccessWithoutDisk("success")
	if !reject || !strings.Contains(msg, "missing on disk") || !strings.Contains(msg, "WRITE") {
		t.Fatalf("reject=%v msg=%q", reject, msg)
	}

	body := "package api\n\nimport \"net/http\"\n\nfunc RegisterHandlers() {\n\thttp.HandleFunc(\"GET /\", func(http.ResponseWriter, *http.Request) {})\n}\n"
	if err := os.WriteFile(filepath.Join(rigDir, beadPath), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	if _, reject := r.rejectImplementationSuccessWithoutDisk("success"); reject {
		t.Fatal("should allow success when file exists")
	}
}

func TestRejectImplementationSuccessWithoutDisk_triggersCorruptionCleanup(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	beadPath := "linkshelf/internal/api/handlers.go"
	if err := os.MkdirAll(filepath.Join(rigDir, "linkshelf", "internal", "api"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(rigDir, ".beads"), 0755); err != nil {
		t.Fatal(err)
	}
	// Active bead path is missing to trigger rejectImplementationSuccessWithoutDisk.
	// Separate open bead file is corruption-marker-only and should be auto-deleted.
	corruptRel := "linkshelf/internal/api/handlers_test.go"
	if err := os.WriteFile(filepath.Join(rigDir, corruptRel), []byte(">>>>>>> REPLACE"), 0644); err != nil {
		t.Fatal(err)
	}

	task := &orchestrator.Task{
		WorkflowID: "wf-1",
		State:      "implementation",
		Hooks:      orchestrator.StateHooks{Track: "implementation", Artifacts: "implementation"},
		Validation: orchestrator.WorkflowValidation{
			LayoutRoot:                 "linkshelf",
			BeadTitleContains:          "Implement linkshelf/",
			RequiredFiles:              []string{beadPath, corruptRel},
			MinImplementationFileBytes: 1,
			MinSubstantiveLines:        1,
		},
	}
	r := newStateRunner(task, dir, rig)
	r.track.activeBead = "te-xhq"
	r.track.activeBeadPath = beadPath

	prev := orchestrator.ListImplementBeadsByStatusHook
	orchestrator.ListImplementBeadsByStatusHook = func(_, _ string, _ orchestrator.WorkflowValidation, status string) ([]orchestrator.PlanBead, error) {
		if status == "open" {
			return []orchestrator.PlanBead{{ID: "te-corrupt", Title: "Implement linkshelf/internal/api/handlers_test.go per architecture"}}, nil
		}
		return nil, nil
	}
	defer func() { orchestrator.ListImplementBeadsByStatusHook = prev }()

	msg, reject := r.rejectImplementationSuccessWithoutDisk("success")
	if !reject {
		t.Fatal("expected reject")
	}
	if !strings.Contains(msg, "Auto-cleanup removed corrupted open-bead artifacts") ||
		!strings.Contains(msg, "linkshelf/internal/api/handlers_test.go") {
		t.Fatalf("missing cleanup details in message: %q", msg)
	}
	if _, err := os.Stat(filepath.Join(rigDir, corruptRel)); !os.IsNotExist(err) {
		t.Fatalf("corrupted file should be deleted, err=%v", err)
	}
}
