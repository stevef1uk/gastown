package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestImplementationProgress_saveLoadVerifyAndClear(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "mockrig"
	p := newImplementationProgress("wf-7", "implementation", rig)
	p.mark(implVerifyKey("te-store"))
	p.ActiveBead = "te-store"
	p.ActiveBeadPath = "linkshelf/internal/store/store.go"
	if err := saveImplementationProgress(dir, rig, p); err != nil {
		t.Fatal(err)
	}
	got := loadImplementationProgress(dir, rig, "wf-7", "implementation")
	if got == nil || !got.done(implVerifyKey("te-store")) {
		t.Fatalf("load = %+v", got)
	}
	clearImplementationProgress(dir, rig)
	if _, err := os.Stat(implementationProgressPath(dir, rig)); !os.IsNotExist(err) {
		t.Fatalf("file still exists: %v", err)
	}
}

func TestInitImplementationProgress_restoresVerifyOK(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "mockrig"
	wf := "wf-1"
	p := newImplementationProgress(wf, "implementation", rig)
	p.mark(implVerifyKey("te-store"))
	p.ActiveBead = "te-store"
	p.ActiveBeadPath = "linkshelf/internal/store/store.go"
	if err := saveImplementationProgress(dir, rig, p); err != nil {
		t.Fatal(err)
	}
	runner := newStateRunner(&orchestrator.Task{
		WorkflowID: wf,
		State:      "implementation",
		Hooks: orchestrator.StateHooks{
			Track: "implementation",
		},
	}, dir, rig)
	runner.v = orchestrator.WorkflowValidation{LayoutRoot: "linkshelf", TestRunner: "go"}
	block := runner.initImplementationProgress()
	if !runner.track.verifyOK {
		t.Fatal("expected verifyOK restored from progress")
	}
	if !strings.Contains(block, "te-store") || !strings.Contains(block, "Verify already passed") {
		t.Fatalf("block = %q", block)
	}
}

func TestClearImplementationProgressIfLeaving(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "mockrig"
	p := newImplementationProgress("wf-1", "implementation", rig)
	if err := saveImplementationProgress(dir, rig, p); err != nil {
		t.Fatal(err)
	}
	clearImplementationProgressIfLeaving(dir, rig, "implementation", "implementation")
	if loadImplementationProgress(dir, rig, "wf-1", "implementation") == nil {
		t.Fatal("should not clear when staying in implementation")
	}
	clearImplementationProgressIfLeaving(dir, rig, "implementation", "qa_review")
	if loadImplementationProgress(dir, rig, "wf-1", "implementation") != nil {
		t.Fatal("should clear when leaving implementation")
	}
}

func TestNoteImplementationVerifyFailure_persistsReopenPaths(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "mockrig"
	wf := "wf-2"
	runner := newStateRunner(&orchestrator.Task{
		WorkflowID: wf,
		State:      "implementation",
		Hooks:      orchestrator.StateHooks{Track: "implementation", AppendGoCompileContext: true},
	}, dir, rig)
	runner.v = orchestrator.WorkflowValidation{
		LayoutRoot:        "linkshelf",
		TestRunner:        "go",
		BeadTitleContains: "Implement linkshelf/",
		RequiredFiles: []string{
			"linkshelf/internal/api/handlers.go",
			"linkshelf/cmd/server/main.go",
		},
	}
	runner.implProgress = newImplementationProgress(wf, "implementation", rig)
	runner.track.activeBead = "te-main"
	runner.track.activeBeadPath = "linkshelf/cmd/server/main.go"

	orchestrator.ListImplementBeadsByStatusHook = func(_, _ string, _ orchestrator.WorkflowValidation, status string) ([]orchestrator.PlanBead, error) {
		switch status {
		case "in_progress":
			return []orchestrator.PlanBead{{ID: "te-main", Title: "Implement linkshelf/cmd/server/main.go per architecture"}}, nil
		case "closed":
			return []orchestrator.PlanBead{{ID: "te-h", Title: "Implement linkshelf/internal/api/handlers.go per architecture"}}, nil
		default:
			return nil, nil
		}
	}
	t.Cleanup(func() { orchestrator.ListImplementBeadsByStatusHook = nil })

	runner.noteImplementationVerifyFailure("go build ./...", "linkshelf/cmd/server/main.go:10:2: undefined: api.ListLinks")
	got := loadImplementationProgress(dir, rig, wf, "implementation")
	if got == nil || len(got.LastVerifyFailPaths) == 0 {
		t.Fatalf("progress = %+v", got)
	}
	found := false
	for _, p := range got.LastVerifyFailPaths {
		if strings.Contains(p, "handlers.go") {
			found = true
		}
	}
	if !found {
		t.Fatalf("LastVerifyFailPaths = %v", got.LastVerifyFailPaths)
	}
	block := runner.formatImplementationProgressBlock()
	if !strings.Contains(block, "te-h") || !strings.Contains(block, "Reopen closed") {
		t.Fatalf("block = %q", block)
	}
}

func TestImplementationProgressPath_underRigQA(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	want := filepath.Join(dir, "mockrig", "qa", "implementation-progress.json")
	if got := implementationProgressPath(dir, "mockrig"); got != want {
		t.Fatalf("path = %q want %q", got, want)
	}
}
