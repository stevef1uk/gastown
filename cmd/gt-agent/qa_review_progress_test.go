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
