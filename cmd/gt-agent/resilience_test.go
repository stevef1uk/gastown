package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestBeadAttemptsSurviveRestartWithoutDeletingShortArtifact(t *testing.T) {
	for _, body := range []string{"package sample\n", strings.Repeat("x", 500)} {
		dir := t.TempDir()
		rig := "renamed-project"
		path := filepath.Join(dir, rig, "mayor", "rig", "init.py")
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		r := &stateRunner{townRoot: dir, rig: rig, track: &cmdTracker{activeBead: "b1", activeBeadPath: "init.py"}, implProgress: newImplementationProgress("wf-1", "implementation", rig)}
		r.implProgress.BeadTurnsLimit = 4
		for i := 0; i < 4; i++ {
			if r.addBeadTurn("b1") {
				t.Fatal("early limit")
			}
		}
		r.implProgress = loadImplementationProgress(dir, rig, "wf-1", "implementation")
		if !r.addBeadTurn("b1") {
			t.Fatal("restart reset attempt counter")
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != body {
			t.Fatalf("turn limit modified artifact: %q %v", got, err)
		}
	}
}

func TestExecutionBatchKeepsMilestoneAndClosedCheckpoints(t *testing.T) {
	dir := t.TempDir()
	rig := "renamed"
	p := newImplementationProgress("wf-2", "implementation", rig)
	r := &stateRunner{implProgress: p, hooks: orchestrator.StateHooks{ExecutionBatchSize: 8}}
	sessions := 1
	baseline := 0
	for i := 0; i < 33; i++ {
		p.mark(implClosedKey(string(rune('A' + i))))
		if r.executionBatchReady(baseline) {
			if err := saveImplementationProgress(dir, rig, p); err != nil {
				t.Fatal(err)
			}
			p = loadImplementationProgress(dir, rig, "wf-2", "implementation")
			r.implProgress = p
			baseline = r.closedCheckpointCount()
			sessions++
		}
	}
	if sessions != 5 || r.closedCheckpointCount() != 33 {
		t.Fatalf("sessions=%d checkpoints=%d", sessions, r.closedCheckpointCount())
	}
}

func TestResourceErrorsArePermanent(t *testing.T) {
	for _, msg := range []string{"LLM API error (status 401)", "LLM API error (status 402): credits", "LLM API error (status 403)", "execution budget exhausted"} {
		err := errors.New(msg)
		if !isPermanentLLMError(err) || isRetriableLLMError(err) {
			t.Fatalf("must stop dispatch: %s", msg)
		}
	}
	if isPermanentLLMError(errors.New("LLM API error (status 429): rate limit")) {
		t.Fatal("temporary rate limit classified permanent")
	}
}
