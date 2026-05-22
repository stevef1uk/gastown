package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func implementationTask(t *testing.T, wf string, files ...string) *orchestrator.Task {
	t.Helper()
	task := rigFlowTask(t, "implementation", linkshelfImplementValidation(files...))
	task.WorkflowID = wf
	return task
}

func TestImplementationCmdGuard_rejectsCatMissingTestFile(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	v := linkshelfImplementValidation(
		"linkshelf/internal/store/store.go",
		"linkshelf/internal/store/store_test.go",
	)
	task := implementationTask(t, "wf-guard", v.RequiredFiles...)
	r := newStateRunner(task, dir, rig)
	r.v = v
	r.track.activeBead = "te-store"
	r.track.activeBeadPath = "linkshelf/internal/store/store.go"

	err := r.validateCommand("cat linkshelf/internal/store/store_test.go")
	if err == nil {
		t.Fatal("implementation cmd_guard should reject cat on missing separate test file")
	}
	if !strings.Contains(err.Error(), "separate implement bead") {
		t.Fatalf("err = %v", err)
	}
}

func TestImplementationProgress_restartSession(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	wf := "wf-impl-restart"
	v := linkshelfImplementValidation("linkshelf/internal/store/store.go")
	task := implementationTask(t, wf, v.RequiredFiles...)
	sessionA := newStateRunner(task, dir, rig)
	if block := sessionA.initImplementationProgress(); block != "" {
		t.Fatalf("fresh session should not inject progress, got %q", block)
	}

	verifyCmd := "cd " + rig + "/mayor/rig/linkshelf && go build -o /dev/null ./internal/store/..."
	sessionA.track.activeBead = "te-store"
	sessionA.track.activeBeadPath = "linkshelf/internal/store/store.go"
	sessionA.track.verifyOK = true
	var combined strings.Builder
	sessionA.afterCommand(verifyCmd, nil, dir, "sess-a", nil, &combined)

	if !sessionA.implProgress.done(implVerifyKey("te-store")) {
		t.Fatalf("progress = %+v", sessionA.implProgress.Completed)
	}
	onDisk := loadImplementationProgress(dir, rig, wf, "implementation")
	if onDisk == nil || !onDisk.done(implVerifyKey("te-store")) {
		t.Fatalf("on disk = %+v", onDisk)
	}

	sessionB := newStateRunner(task, dir, rig)
	block := sessionB.initImplementationProgress()
	if block == "" {
		t.Fatal("restarted session must inject implementation progress block")
	}
	if !strings.Contains(block, "Verify passed") && !strings.Contains(block, "resume without repeating") {
		t.Fatalf("block missing progress guidance: %q", block)
	}
	if !strings.Contains(block, "te-store") {
		t.Fatalf("block should mention active bead: %q", block)
	}
	sessionB.track.activeBead = "te-store"
	sessionB.track.activeBeadPath = "linkshelf/internal/store/store.go"
	if sessionB.track.verifyOK {
		t.Fatal("verifyOK must not restore from progress alone; run Verify in this session")
	}
}

func TestImplementationProgress_persistBeadClose(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	wf := "wf-close"
	task := implementationTask(t, wf, "linkshelf/internal/store/store.go")
	r := newStateRunner(task, dir, rig)
	r.implProgress = newImplementationProgress(wf, "implementation", rig)
	r.track.activeBead = "te-store"

	closeCmd := "export BEADS_DIR=x && cd " + rig + "/mayor/rig && bd close te-store"
	var combined strings.Builder
	r.afterCommand(closeCmd, nil, dir, "sess", nil, &combined)

	got := loadImplementationProgress(dir, rig, wf, "implementation")
	if got == nil || !got.done(implClosedKey("te-store")) {
		t.Fatalf("closed bead not persisted: %+v", got)
	}
}

func TestEnsureTestBeadSkeletonAfterBdUpdate(t *testing.T) {
	dir := t.TempDir()
	rig := "testrig"
	mayor := filepath.Join(dir, rig, "mayor", "rig")
	if err := os.MkdirAll(filepath.Join(mayor, "linkshelf", "internal", "store"), 0755); err != nil {
		t.Fatal(err)
	}
	writeLinkshelfStoreTree(t, mayor, "package store\n")

	v := linkshelfImplementValidation(
		"linkshelf/internal/store/store.go",
		"linkshelf/internal/store/store_test.go",
	)
	task := implementationTask(t, "wf-skel", v.RequiredFiles...)
	r := newStateRunner(task, dir, rig)

	orchestrator.ListImplementBeadsByStatusHook = func(_, _ string, _ orchestrator.WorkflowValidation, status string) ([]orchestrator.PlanBead, error) {
		if status == "in_progress" || status == "open" {
			return []orchestrator.PlanBead{{
				ID:    "te-test",
				Title: "Implement linkshelf/internal/store/store_test.go per architecture",
			}}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { orchestrator.ListImplementBeadsByStatusHook = nil })

	updateCmd := "export BEADS_DIR=x && cd " + rig + "/mayor/rig && bd update te-test --status=in_progress"
	var combined strings.Builder
	r.afterCommand(updateCmd, nil, mayor, "sess", nil, &combined)

	testFile := filepath.Join(mayor, "linkshelf/internal/store/store_test.go")
	data, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("skeleton not created: %v", err)
	}
	if !strings.Contains(string(data), "TestPlaceholder") {
		t.Fatalf("unexpected skeleton: %s", data)
	}
	if r.track.activeBead != "te-test" {
		t.Fatalf("track = %+v", r.track)
	}
	if r.track.activeBeadPath != "linkshelf/internal/store/store_test.go" {
		t.Fatalf("activeBeadPath = %q", r.track.activeBeadPath)
	}
}

func TestAppendGoCompileSourceContext_reopenClosedBeadHints(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	mayor := filepath.Join(dir, rig, "mayor", "rig")
	handlersDir := filepath.Join(mayor, "linkshelf/internal/api")
	if err := os.MkdirAll(handlersDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(handlersDir, "handlers.go"), []byte("package api\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mainDir := filepath.Join(mayor, "linkshelf/cmd/server")
	if err := os.MkdirAll(mainDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mainDir, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	v := linkshelfImplementValidation(
		"linkshelf/internal/api/handlers.go",
		"linkshelf/cmd/server/main.go",
	)
	closedHandlersBeadHook()
	t.Cleanup(func() { orchestrator.ListImplementBeadsByStatusHook = nil })

	var b strings.Builder
	out := "linkshelf/cmd/server/main.go:12:9: undefined: api.ListLinks"
	appendGoCompileSourceContext(&b, dir, rig, mayor, "linkshelf", "linkshelf/cmd/server/main.go", v,
		"go build ./...", out)
	got := b.String()
	for _, want := range []string{
		"Reopen closed implement beads",
		"te-h",
		"handlers.go",
		"bd update te-h --status=open",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestNativeREAD_missingSeparateTestFileNudge(t *testing.T) {
	dir := t.TempDir()
	rig := "testrig"
	mayor := filepath.Join(dir, rig, "mayor", "rig")
	writeLinkshelfStoreTree(t, mayor, "package store\n")

	v := linkshelfImplementValidation(
		"linkshelf/internal/store/store.go",
		"linkshelf/internal/store/store_test.go",
	)
	task := implementationTask(t, "wf-read", v.RequiredFiles...)
	r := newStateRunner(task, dir, rig)
	r.track.activeBead = "te-store"
	r.track.activeBeadPath = "linkshelf/internal/store/store.go"

	var combined strings.Builder
	had, _ := r.processOrchestratedTools("READ: linkshelf/internal/store/store_test.go\n", "sess", &combined)
	if !had {
		t.Fatal("expected native tool attempt")
	}
	got := combined.String()
	if !strings.Contains(got, "separate implement bead") {
		t.Fatalf("want reopen/test-bead nudge, got:\n%s", got)
	}
	if !r.track.hadCmdFailure {
		t.Fatal("READ missing file should be tracked as cmd failure")
	}
}

func TestImplementationProgress_noteVerifyFailureThenResume(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	wf := "wf-fail-resume"
	v := linkshelfImplementValidation(
		"linkshelf/internal/api/handlers.go",
		"linkshelf/cmd/server/main.go",
	)
	task := implementationTask(t, wf, v.RequiredFiles...)

	sessionA := newStateRunner(task, dir, rig)
	sessionA.implProgress = newImplementationProgress(wf, "implementation", rig)
	sessionA.track.activeBead = "te-main"
	sessionA.track.activeBeadPath = "linkshelf/cmd/server/main.go"
	closedHandlersBeadHook()
	t.Cleanup(func() { orchestrator.ListImplementBeadsByStatusHook = nil })

	sessionA.noteImplementationVerifyFailure("go build ./...",
		"linkshelf/cmd/server/main.go:9:2: undefined: api.ListLinks")

	sessionB := newStateRunner(task, dir, rig)
	sessionB.track.activeBead = "te-main"
	sessionB.track.activeBeadPath = "linkshelf/cmd/server/main.go"
	block := sessionB.initImplementationProgress()
	if !strings.Contains(block, "te-h") || !strings.Contains(block, "Reopen closed") {
		t.Fatalf("resume block should carry reopen hints: %q", block)
	}
}

func TestFormatImplementationProgressBlock_emptyWhenNoCheckpoints(t *testing.T) {
	r := newStateRunner(implementationTask(t, "wf-empty", "linkshelf/go.mod"), t.TempDir(), "mockrig")
	r.implProgress = newImplementationProgress("wf-empty", "implementation", "mockrig")
	if got := r.formatImplementationProgressBlock(); got != "" {
		t.Fatalf("want empty block, got %q", got)
	}
}
