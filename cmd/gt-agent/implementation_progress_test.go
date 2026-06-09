package main

import (
	"os"
	"os/exec"
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

func TestInitImplementationProgress_doesNotRestoreVerifyOKWithoutGreenRun(t *testing.T) {
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
	if runner.track.verifyOK {
		t.Fatal("verifyOK must not be true until Verify passes in this session")
	}
	if !strings.Contains(block, "te-store") || !strings.Contains(block, "Verify passed") {
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
	runner.implProgress.mark(implVerifyKey("te-main"))
	runner.track.verifyOK = true
	got := loadImplementationProgress(dir, rig, wf, "implementation")
	if got == nil || len(got.LastVerifyFailPaths) == 0 {
		t.Fatalf("progress = %+v", got)
	}
	if got.done(implVerifyKey("te-main")) {
		t.Fatal("verify milestone should be cleared on failure")
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

func TestClearStaleImplementationVerifyFailureOnResume(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	dir := t.TempDir()
	rig := "mockrig"
	wf := "wf-stale"
	mayor := filepath.Join(dir, rig, "mayor", "rig")
	storeDir := filepath.Join(mayor, "linkshelf", "internal", "store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mayor, "linkshelf/go.mod"), []byte("module linkshelf\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "store.go"), []byte("package store\n"), 0644); err != nil {
		t.Fatal(err)
	}
	p := newImplementationProgress(wf, "implementation", rig)
	p.ActiveBead = "te-store"
	p.ActiveBeadPath = "linkshelf/internal/store/store.go"
	p.LastVerifyFailBead = "te-store"
	p.LastVerifyFailPath = "linkshelf/internal/store/store.go"
	p.LastVerifyFailPaths = []string{"linkshelf/internal/store/schema.go"}
	if err := saveImplementationProgress(dir, rig, p); err != nil {
		t.Fatal(err)
	}
	runner := newStateRunner(&orchestrator.Task{
		WorkflowID: wf,
		State:      "implementation",
		Hooks:      orchestrator.StateHooks{Track: "implementation"},
	}, dir, rig)
	runner.v = orchestrator.WorkflowValidation{
		LayoutRoot:        "linkshelf",
		QAVerifyCommand:   "cd linkshelf && go test ./...",
		RequiredFiles:     []string{"linkshelf/internal/store/store.go"},
		BeadTitleContains: "Implement ",
	}
	runner.track.activeBead = "te-store"
	runner.track.activeBeadPath = "linkshelf/internal/store/store.go"
	runner.implProgress = loadImplementationProgress(dir, rig, wf, "implementation")
	runner.clearStaleImplementationVerifyFailureOnResume()
	got := loadImplementationProgress(dir, rig, wf, "implementation")
	if got == nil || got.LastVerifyFailBead != "" || len(got.LastVerifyFailPaths) > 0 {
		t.Fatalf("expected cleared verify-fail, got %+v", got)
	}
	if !runner.track.verifyOK {
		t.Fatal("expected verifyOK after green resume check")
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

func TestRejectRedundantImplementEditAfterVerify_allowsEditWhenOnlyPersistedMilestone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "mockrig"
	mayor := filepath.Join(dir, rig, "mayor", "rig")
	storeDir := filepath.Join(mayor, "linkshelf", "internal", "store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mayor, "linkshelf", "go.mod"), []byte("module linkshelf\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "schema.go"), []byte("package store\n\nfunc Schema() string { return \"ok\" }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "schema_test.go"), []byte("package store\n\nfunc TestSchema(t *testing.T) {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runner := newStateRunner(&orchestrator.Task{
		WorkflowID: "wf-1",
		State:      "implementation",
		Hooks:      orchestrator.StateHooks{Track: "implementation"},
	}, dir, rig)
	runner.v = orchestrator.WorkflowValidation{
		LayoutRoot:                 "linkshelf",
		TestRunner:                 "go",
		MinImplementationFileBytes: 1,
		MinSubstantiveLines:        1,
		RequiredFiles:              []string{"linkshelf/internal/store/schema.go"},
	}
	runner.implProgress = newImplementationProgress("wf-1", "implementation", rig)
	runner.implProgress.mark(implVerifyKey("te-g7q"))
	runner.track.activeBead = "te-g7q"
	runner.track.activeBeadPath = "linkshelf/internal/store/schema.go"
	runner.track.verifyOK = false
	if err := runner.rejectRedundantImplementEditAfterVerify("linkshelf/internal/store/schema_test.go"); err != nil {
		t.Fatalf("edit should be allowed without session verifyOK: %v", err)
	}
	runner.track.verifyOK = true
	if err := runner.rejectRedundantImplementEditAfterVerify("linkshelf/internal/store/schema_test.go"); err == nil {
		t.Fatal("edit should be blocked when verifyOK is true this session")
	}
}

func TestClearStaleImplementationVerifyMilestoneOnResume(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	dir := t.TempDir()
	rig := "mockrig"
	wf := "wf-stale-milestone"
	mayor := filepath.Join(dir, rig, "mayor", "rig")
	storeDir := filepath.Join(mayor, "linkshelf", "internal", "store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mayor, "linkshelf/go.mod"), []byte("module linkshelf\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "schema.go"), []byte("package store\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Broken test — verify must fail on resume.
	if err := os.WriteFile(filepath.Join(storeDir, "schema_test.go"), []byte("package store\n\nfunc TestSchema(t *testing.T) { t.Fatal(\"fail\") }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	p := newImplementationProgress(wf, "implementation", rig)
	p.mark(implVerifyKey("te-g7q"))
	p.ActiveBead = "te-g7q"
	p.ActiveBeadPath = "linkshelf/internal/store/schema.go"
	if err := saveImplementationProgress(dir, rig, p); err != nil {
		t.Fatal(err)
	}
	runner := newStateRunner(&orchestrator.Task{
		WorkflowID: wf,
		State:      "implementation",
		Hooks:      orchestrator.StateHooks{Track: "implementation"},
	}, dir, rig)
	runner.v = orchestrator.WorkflowValidation{
		LayoutRoot:      "linkshelf",
		TestRunner:      "go",
		QAVerifyCommand: "cd linkshelf && go test ./...",
		RequiredFiles:   []string{"linkshelf/internal/store/schema.go", "linkshelf/internal/store/schema_test.go"},
	}
	runner.track.activeBead = "te-g7q"
	runner.track.activeBeadPath = "linkshelf/internal/store/schema.go"
	runner.implProgress = loadImplementationProgress(dir, rig, wf, "implementation")
	runner.clearStaleImplementationVerifyMilestoneOnResume()
	got := loadImplementationProgress(dir, rig, wf, "implementation")
	if got == nil || got.done(implVerifyKey("te-g7q")) {
		t.Fatalf("stale verify milestone should be cleared, got %+v", got)
	}
	if runner.track.verifyOK {
		t.Fatal("verifyOK must stay false when verify is red on resume")
	}
}

func TestImplementationTrack_clearsPersistedVerifyOnFailedVerifyCmd(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	wf := "wf-fail"
	mayor := filepath.Join(dir, rig, "mayor", "rig")
	storeDir := filepath.Join(mayor, "linkshelf", "internal", "store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mayor, "linkshelf/go.mod"), []byte("module linkshelf\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "schema.go"), []byte("package store\n\nfunc Schema() string { return \"ok\" }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "schema_test.go"), []byte("package store\n\nfunc TestSchema(t *testing.T) { t.Fatal(\"fail\") }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	p := newImplementationProgress(wf, "implementation", rig)
	p.mark(implVerifyKey("te-g7q"))
	p.ActiveBead = "te-g7q"
	p.ActiveBeadPath = "linkshelf/internal/store/schema.go"
	if err := saveImplementationProgress(dir, rig, p); err != nil {
		t.Fatal(err)
	}
	task := &orchestrator.Task{
		WorkflowID: wf,
		State:      "implementation",
		Hooks:      orchestrator.StateHooks{Track: "implementation"},
	}
	r := newStateRunner(task, dir, rig)
	r.v = orchestrator.WorkflowValidation{
		LayoutRoot:      "linkshelf",
		TestRunner:      "go",
		QAVerifyCommand: "cd linkshelf && go test ./...",
		RequiredFiles:   []string{"linkshelf/internal/store/schema.go", "linkshelf/internal/store/schema_test.go"},
	}
	r.implProgress = loadImplementationProgress(dir, rig, wf, "implementation")
	r.track.activeBead = "te-g7q"
	r.track.activeBeadPath = "linkshelf/internal/store/schema.go"
	r.track.verifyOK = false
	trackHandlers["implementation"](r, "cd mockrig/mayor/rig/linkshelf && go test -count=1 ./internal/store/...", errFake{})
	if r.track.verifyOK {
		t.Fatal("verifyOK should stay false after failed verify command")
	}
	got := loadImplementationProgress(dir, rig, wf, "implementation")
	if got == nil || got.done(implVerifyKey("te-g7q")) {
		t.Fatalf("persisted verify milestone should clear on failed verify, got %+v", got)
	}
}
