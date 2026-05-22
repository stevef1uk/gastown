package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestValidateImplementationArtifacts_requiresVerifyNotDiskReady(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	rigDir := setupMockRigImplementFiles(t, dir, rig)
	v := orchestrator.DefaultWorkflowValidation()
	v.LayoutRoot = "linkshelf"
	v.BeadTitleContains = "Implement "
	v.RequiredFiles = []string{"linkshelf/internal/foo/foo.go"}
	v.QAVerifyCommand = "cd linkshelf && go test ./..."
	// All files on disk, no open beads, but verify never ran.
	err := validateImplementationArtifacts(dir, rig, false, true, false, v)
	if err == nil {
		t.Fatal("expected verify required")
	}
	if !strings.Contains(err.Error(), "verification must pass") {
		t.Fatalf("err=%v", err)
	}
	_ = rigDir
}

func TestImplementationTrack_clearsVerifyOnFailedCommand(t *testing.T) {
	task := &orchestrator.Task{
		State: "implementation",
		Hooks: orchestrator.StateHooks{Track: "implementation"},
		Validation: orchestrator.DefaultWorkflowValidation(),
	}
	r := newStateRunner(task, t.TempDir(), "mockrig")
	r.track.verifyOK = true
	var cmdErr error = errFake{}
	trackHandlers["implementation"](r, "cd linkshelf && go test ./...", cmdErr)
	if r.track.verifyOK {
		t.Fatal("verifyOK should clear after failed command")
	}
	if !r.track.hadCmdFailure {
		t.Fatal("expected hadCmdFailure")
	}
}

func TestImplementationTrack_bdCloseWithoutVerifyDoesNotSetBeadCloseOK(t *testing.T) {
	task := &orchestrator.Task{
		State: "implementation",
		Hooks: orchestrator.StateHooks{Track: "implementation", CmdGuard: "implementation"},
		Validation: orchestrator.DefaultWorkflowValidation(),
	}
	r := newStateRunner(task, t.TempDir(), "mockrig")
	r.track.verifyOK = false
	trackHandlers["implementation"](r, "bd close te-abc", nil)
	if r.track.beadCloseOK {
		t.Fatal("bd close without verify must not set beadCloseOK")
	}
}

func TestUnwrapMarkdownInlineToolLines(t *testing.T) {
	in := "`CMD: export BEADS_DIR=x && bd update te-ff0 --status=in_progress`\n"
	got := unwrapMarkdownInlineToolLines(in)
	if !strings.HasPrefix(strings.TrimSpace(got), "CMD:") {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "`") {
		t.Fatalf("backticks remain: %q", got)
	}
	cmds := parseOrchestratedCommands(got)
	if len(cmds) != 1 || strings.Contains(cmds[0], "`") {
		t.Fatalf("cmds=%v", cmds)
	}
}

type errFake struct{}

func (errFake) Error() string { return "failed" }

func setupMockRigImplementFiles(t *testing.T, town, rig string) string {
	t.Helper()
	rigDir := rigMayorRigDir(town, rig)
	layout := filepath.Join(rigDir, "linkshelf", "internal", "foo")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "foo.go"), []byte("package foo\n\nfunc X() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "linkshelf", "go.mod"), []byte("module linkshelf\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// No open implement beads: hook returns 0
	orchestrator.ListImplementBeadsByStatusHook = func(_, _ string, _ orchestrator.WorkflowValidation, status string) ([]orchestrator.PlanBead, error) {
		return nil, nil
	}
	t.Cleanup(func() { orchestrator.ListImplementBeadsByStatusHook = nil })
	return rigDir
}
