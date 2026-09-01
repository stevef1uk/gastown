package main

import (
	"fmt"
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
	countOpenMatchingBeadsHook = func(_, _ string, _ orchestrator.WorkflowValidation) (int, error) {
		return 0, nil
	}
	t.Cleanup(func() { countOpenMatchingBeadsHook = nil })
	// Force on-disk phase verify to fail — do not rely on go test timing/toolchain in CI.
	implementationPhaseVerifyOKHook = func(_, _ string, _ orchestrator.WorkflowValidation) error {
		return fmt.Errorf("disk verify not green")
	}
	t.Cleanup(func() { implementationPhaseVerifyOKHook = nil })
	v := orchestrator.DefaultWorkflowValidation()
	v.LayoutRoot = "linkshelf"
	v.BeadTitleContains = "Implement "
	v.RequiredFiles = []string{"linkshelf/internal/foo/foo.go"}
	v.QAVerifyCommand = "cd linkshelf && go test ./..."
	// All files on disk, no open beads, but verify never ran in this session.
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
		State:      "implementation",
		Hooks:      orchestrator.StateHooks{Track: "implementation"},
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

func TestImplementationTrack_preservesVerifyOnFailedGoTestWhenCanonicalIsGoBuild(t *testing.T) {
	town := t.TempDir()
	rig := "testgt3"
	rigDir := filepath.Join(town, rig, "mayor", "rig", "linkshelf")
	storeDir := filepath.Join(rigDir, "internal", "store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "go.mod"), []byte("module linkshelf\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "store_test.go"), []byte("package store\nfunc TestBroken(t *testing.T) {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := orchestrator.WorkflowValidation{
		LayoutRoot:      "linkshelf",
		TestRunner:      "go",
		QAVerifyCommand: "cd linkshelf && go test ./...",
		RequiredFiles: []string{
			"linkshelf/internal/store/schema.go",
			"linkshelf/internal/store/store.go",
			"linkshelf/internal/store/store_test.go",
		},
	}
	orchestrator.ListImplementBeadsByStatusHook = func(_, _ string, _ orchestrator.WorkflowValidation, status string) ([]orchestrator.PlanBead, error) {
		if status == "in_progress" {
			return []orchestrator.PlanBead{{ID: "te-schema", Title: "Implement linkshelf/internal/store/schema.go"}}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { orchestrator.ListImplementBeadsByStatusHook = nil })
	task := &orchestrator.Task{
		State:      "implementation",
		Hooks:      orchestrator.StateHooks{Track: "implementation"},
		Validation: v,
	}
	r := newStateRunner(task, town, rig)
	r.track.verifyOK = true
	r.track.activeBead = "te-schema"
	r.track.activeBeadPath = "linkshelf/internal/store/schema.go"
	trackHandlers["implementation"](r, "cd testgt3/mayor/rig/linkshelf && go test -count=1 ./internal/store/...", errFake{})
	if !r.track.verifyOK {
		t.Fatal("verifyOK must stay true when canonical verify is go build and foreign go test fails")
	}
	if !r.track.hadCmdFailure {
		t.Fatal("expected hadCmdFailure")
	}
}

func TestImplementationTrack_bdCloseSetsBeadCloseOKRegardlessOfVerify(t *testing.T) {
	task := &orchestrator.Task{
		State:      "implementation",
		Hooks:      orchestrator.StateHooks{Track: "implementation", CmdGuard: "implementation"},
		Validation: orchestrator.DefaultWorkflowValidation(),
	}
	r := newStateRunner(task, t.TempDir(), "mockrig")
	r.track.verifyOK = false
	trackHandlers["implementation"](r, "bd close te-abc", nil)
	if !r.track.beadCloseOK {
		t.Fatal("successful bd close must set beadCloseOK even when verifyOK is false (bead guards already verified the bead)")
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

func TestOpenBeadCircuitBreaker_tripsAfterThreshold(t *testing.T) {
	v := orchestrator.WorkflowValidation{
		LayoutRoot:        "linkshelf",
		BeadTitleContains: "Implement ",
		RequiredFiles:     []string{"linkshelf/internal/foo/foo.go"},
	}
	countOpenMatchingBeadsHook = func(_, _ string, _ orchestrator.WorkflowValidation) (int, error) {
		return 1, nil
	}
	t.Cleanup(func() { countOpenMatchingBeadsHook = nil })
	task := &orchestrator.Task{
		State:      "implementation",
		Hooks:      orchestrator.StateHooks{Track: "implementation", CmdGuard: "implementation"},
		Validation: v,
	}
	r := newStateRunner(task, t.TempDir(), "mockrig")

	for i := 0; i < OpenBeadCircuitBreakerThreshold; i++ {
		if r.openBeadCircuitBreakerTripped() {
			t.Fatalf("breaker tripped early at iteration %d", i)
		}
		msg, reject := r.rejectImplementationOpenBeadsSuccess("success", "All implementation beads closed.")
		if !reject {
			t.Fatalf("expected rejection at iteration %d", i)
		}
		if !strings.Contains(msg, "success JSON while implement beads are still **open**") {
			t.Fatalf("unexpected rejection message at iteration %d: %s", i, msg)
		}
	}
	if !r.openBeadCircuitBreakerTripped() {
		t.Fatal("expected open-bead circuit breaker to trip after threshold")
	}
}

func TestOpenBeadCircuitBreaker_resetsPerNewRunner(t *testing.T) {
	countOpenMatchingBeadsHook = func(_, _ string, _ orchestrator.WorkflowValidation) (int, error) {
		return 1, nil
	}
	t.Cleanup(func() { countOpenMatchingBeadsHook = nil })
	task := &orchestrator.Task{
		State:      "implementation",
		Hooks:      orchestrator.StateHooks{Track: "implementation", CmdGuard: "implementation"},
		Validation: orchestrator.WorkflowValidation{LayoutRoot: "linkshelf", RequiredFiles: []string{"linkshelf/internal/foo/foo.go"}},
	}
	r := newStateRunner(task, t.TempDir(), "mockrig")
	for i := 0; i < 2; i++ {
		r.rejectImplementationOpenBeadsSuccess("success", "All implementation beads closed.")
	}
	if r.openBeadCircuitBreakerTripped() {
		t.Fatal("breaker must not trip after only 2 rejections")
	}
}

func TestOpenBeadCircuitBreaker_noTripWhenNoOpenBeads(t *testing.T) {
	task := &orchestrator.Task{
		State:      "implementation",
		Hooks:      orchestrator.StateHooks{Track: "implementation", CmdGuard: "implementation"},
		Validation: orchestrator.WorkflowValidation{LayoutRoot: "linkshelf", RequiredFiles: []string{"linkshelf/internal/foo/foo.go"}},
	}
	r := newStateRunner(task, t.TempDir(), "mockrig")
	countOpenMatchingBeadsHook = func(_, _ string, _ orchestrator.WorkflowValidation) (int, error) {
		return 0, nil
	}
	t.Cleanup(func() { countOpenMatchingBeadsHook = nil })
	msg, reject := r.rejectImplementationOpenBeadsSuccess("success", "All implementation beads closed.")
	if reject {
		t.Fatalf("did not expect rejection when no open beads: %s", msg)
	}
	if r.openBeadCircuitBreakerTripped() {
		t.Fatal("breaker must not trip when there are no open beads")
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
	fooGo := `package foo

func X() int {
	return 1
}

func Y() int {
	return X() + 1
}
`
	if err := os.WriteFile(filepath.Join(layout, "foo.go"), []byte(fooGo), 0644); err != nil {
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
