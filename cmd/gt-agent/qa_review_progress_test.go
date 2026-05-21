package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestQAReviewProgress_saveLoadAndClear(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	p := newQAReviewProgress("wf-9", "qa_review", rig)
	p.mark(qaMilestoneClosedBeads)
	p.mark(qaMilestoneSpecRead)
	if err := saveQAReviewProgress(dir, rig, p); err != nil {
		t.Fatal(err)
	}
	path := qaReviewProgressPath(dir, rig)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("progress file: %v", err)
	}
	got := loadQAReviewProgress(dir, rig, "wf-9", "qa_review")
	if got == nil || !got.done(qaMilestoneClosedBeads) || !got.done(qaMilestoneSpecRead) {
		t.Fatalf("load: %+v", got)
	}
	if loadQAReviewProgress(dir, rig, "wf-other", "qa_review") != nil {
		t.Fatal("wrong workflow should not load")
	}
	clearQAReviewProgress(dir, rig)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected removed, stat err=%v", err)
	}
}

func TestQAReviewProgress_applyToTrack(t *testing.T) {
	task := &orchestrator.Task{
		WorkflowID: "wf-1",
		State:      "qa_review",
		Hooks:      orchestrator.StateHooks{Track: "qa"},
	}
	runner := newStateRunner(task, t.TempDir(), "mockrig")
	runner.qaProgress = newQAReviewProgress("wf-1", "qa_review", "mockrig")
	runner.qaProgress.mark(qaMilestoneUnittest)
	runner.qaProgress.mark(qaMilestoneRuntimeSmoke)
	runner.applyQAProgressToTrack()
	if !runner.track.unittestOK || !runner.track.qaSmokeOK {
		t.Fatalf("track=%+v", runner.track)
	}
}

func TestFormatQAReviewProgressBlock_listsDoneAndTodo(t *testing.T) {
	p := newQAReviewProgress("wf-1", "qa_review", "testgt3")
	p.mark(qaMilestoneClosedBeads)
	p.mark(qaMilestoneSpecRead)
	block := formatQAReviewProgressBlock(p, "testgt3", "go test ./...")
	if !strings.Contains(block, "do not repeat") {
		t.Fatal("want skip guidance")
	}
	if !strings.Contains(block, "closed") || !strings.Contains(block, "SPEC.md") {
		t.Fatalf("got %q", block)
	}
	if !strings.Contains(block, "runtime smoke") {
		t.Fatal("want remaining smoke in todo")
	}
}

func TestInitQAReviewProgress_injectsAfterLoad(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	p := newQAReviewProgress("wf-1", "qa_review", rig)
	p.mark(qaMilestoneArchRead)
	if err := saveQAReviewProgress(dir, rig, p); err != nil {
		t.Fatal(err)
	}
	task := &orchestrator.Task{
		WorkflowID: "wf-1",
		State:      "qa_review",
		Hooks:      orchestrator.StateHooks{Track: "qa", Artifacts: "qa"},
	}
	runner := newStateRunner(task, dir, rig)
	block := runner.initQAReviewProgress()
	if block == "" || !strings.Contains(block, "architecture.md") {
		t.Fatalf("block=%q", block)
	}
}

func TestQAMilestonesFromReadCommand(t *testing.T) {
	keys := qaMilestonesFromReadCommand("cat mockrig/mayor/rig/SPEC.md")
	if len(keys) != 1 || keys[0] != qaMilestoneSpecRead {
		t.Fatalf("got %v", keys)
	}
	keys = qaMilestonesFromReadCommand(`find mockrig/mayor/rig/linkshelf -type f -exec wc -c {} +`)
	if len(keys) != 1 || keys[0] != qaMilestoneFileAudit {
		t.Fatalf("got %v", keys)
	}
}

func TestClearQAReviewProgressIfLeaving_onlyOnExit(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	p := newQAReviewProgress("wf-1", "qa_review", rig)
	p.mark(qaMilestoneClosedBeads)
	if err := saveQAReviewProgress(dir, rig, p); err != nil {
		t.Fatal(err)
	}
	clearQAReviewProgressIfLeaving(dir, rig, "qa_review", "qa_review")
	if _, err := os.Stat(qaReviewProgressPath(dir, rig)); err != nil {
		t.Fatal("should keep file when state unchanged")
	}
	clearQAReviewProgressIfLeaving(dir, rig, "qa_review", "implementation")
	if _, err := os.Stat(qaReviewProgressPath(dir, rig)); !os.IsNotExist(err) {
		t.Fatalf("should remove file, err=%v", err)
	}
}

func qaReviewTask(workflowID, rig string, v orchestrator.WorkflowValidation) *orchestrator.Task {
	return &orchestrator.Task{
		WorkflowID: workflowID,
		State:      "qa_review",
		Hooks:      orchestrator.StateHooks{Track: "qa", Artifacts: "qa"},
		Validation: v,
	}
}

// TestQAReviewProgress_restartSession simulates gt-agent dying mid-qa_review and a new
// process resuming: progress file + tracker flags + skip prompt must carry over.
func TestQAReviewProgress_restartSession(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	wf := "wf-restart-qa"
	v := linkshelfWebProfile()

	task := qaReviewTask(wf, rig, v)
	sessionA := newStateRunner(task, dir, rig)
	if block := sessionA.initQAReviewProgress(); block != "" {
		t.Fatalf("fresh session should not inject progress block, got %q", block)
	}

	closedCmd := "export BEADS_DIR=$GT_ROOT/" + rig + "/.beads && cd " + rig + "/mayor/rig && bd list --status=closed"
	openCmd := "export BEADS_DIR=$GT_ROOT/" + rig + "/.beads && cd " + rig + "/mayor/rig && bd list --status=open,in_progress"
	specCmd := "cat " + rig + "/mayor/rig/SPEC.md"
	archCmd := "cat " + rig + "/mayor/rig/architecture.md"
	auditCmd := "find " + rig + "/mayor/rig/linkshelf -type f -name '*.go' -exec wc -c {} +"
	testCmd := "cd " + rig + "/mayor/rig && cd linkshelf && go test ./..."
	smokeCmd := "cd " + rig + "/mayor/rig/linkshelf && go run ./cmd/server & curl -sf -X POST -d '{\"title\":\"qa\",\"url\":\"https://example.com\"}' http://127.0.0.1:8080/api/bookmarks && curl -sf http://127.0.0.1:8080/api/bookmarks"

	for _, cmd := range []string{closedCmd, openCmd, specCmd, archCmd, auditCmd, testCmd, smokeCmd} {
		var combined strings.Builder
		sessionA.afterCommand(cmd, nil, dir, "te-mockrig-qa", nil, &combined)
	}

	for _, key := range []string{
		qaMilestoneClosedBeads, qaMilestoneOpenBeads, qaMilestoneSpecRead, qaMilestoneArchRead,
		qaMilestoneFileAudit, qaMilestoneUnittest, qaMilestoneRuntimeSmoke,
	} {
		if !sessionA.qaProgress.done(key) {
			t.Fatalf("session A missing milestone %q: %+v", key, sessionA.qaProgress.Completed)
		}
	}
	if !sessionA.track.bdListClosedOK || !sessionA.track.listOpenOK || !sessionA.track.unittestOK || !sessionA.track.qaSmokeOK {
		t.Fatalf("session A track=%+v", sessionA.track)
	}
	onDisk := loadQAReviewProgress(dir, rig, wf, "qa_review")
	if onDisk == nil || len(onDisk.Completed) != 7 {
		t.Fatalf("on disk: %+v", onDisk)
	}

	// Session B: new gt-agent process, same workflow still in qa_review.
	sessionB := newStateRunner(task, dir, rig)
	block := sessionB.initQAReviewProgress()
	if block == "" {
		t.Fatal("restarted session must inject progress block")
	}
	if !strings.Contains(block, "do not repeat") {
		t.Fatalf("block missing skip guidance: %q", block)
	}
	if strings.Contains(block, "Still required") {
		t.Fatalf("all milestones done — should not list remaining work: %q", block)
	}
	if !strings.Contains(block, "runtime smoke") {
		t.Fatal("block should list completed runtime smoke check")
	}
	if !sessionB.track.bdListClosedOK || !sessionB.track.qaSmokeOK {
		t.Fatalf("session B track not restored: %+v", sessionB.track)
	}
	// Artifact validator should accept restored smoke/unittest flags without re-running CMDs.
	if err := sessionB.validateArtifacts("failure"); err != nil {
		if strings.Contains(err.Error(), "bd list --status=closed") {
			t.Fatalf("should not require re-listing closed beads: %v", err)
		}
		if strings.Contains(err.Error(), "live smoke") {
			t.Fatalf("should not require re-smoke when milestone restored: %v", err)
		}
	}

	// Leaving qa_review clears the file for the next cycle.
	clearQAReviewProgressIfLeaving(dir, rig, "qa_review", "implementation")
	if _, err := os.Stat(qaReviewProgressPath(dir, rig)); !os.IsNotExist(err) {
		t.Fatalf("progress file should be cleared after transition, err=%v", err)
	}
}

func TestPersistQAReviewProgress_writesFile(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	task := &orchestrator.Task{
		WorkflowID: "wf-2",
		State:      "qa_review",
		Hooks:      orchestrator.StateHooks{Track: "qa"},
	}
	runner := newStateRunner(task, dir, rig)
	runner.initQAReviewProgress()
	runner.track.bdListClosedOK = true
	runner.persistQAReviewProgress(`export BEADS_DIR=x && bd list --status=closed`)
	got := loadQAReviewProgress(dir, rig, "wf-2", "qa_review")
	if got == nil || !got.done(qaMilestoneClosedBeads) {
		t.Fatalf("got %+v", got)
	}
	if filepath.Base(qaReviewProgressPath(dir, rig)) != "qa-review-progress.json" {
		t.Fatal("unexpected path")
	}
}
