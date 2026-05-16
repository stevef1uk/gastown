package agentconsole

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentLogPaths_RigSingleton(t *testing.T) {
	town := "/home/stevef/gt"
	paths := agentLogPaths(town, "te-testgt2-qa", "testgt2", "qa", "te-testgt2-qa")
	want := filepath.Join(town, "logs", "sessions", "te-testgt2-qa.log")
	if len(paths) == 0 || paths[0] != want {
		t.Fatalf("expected sessions log first, got %v", paths)
	}
	if !strings.HasSuffix(paths[len(paths)-1], filepath.Join("testgt2", "qa", "typescript")) {
		t.Fatalf("expected typescript fallback, got %v", paths)
	}
}

func TestAgentLogPaths_Orchestrator(t *testing.T) {
	paths := agentLogPaths("/town", "orchestrator", "", "orchestrator", "")
	want := filepath.Join("/town", "logs", "orchestrator.log")
	if len(paths) != 1 || paths[0] != want {
		t.Fatalf("got %v want [%s]", paths, want)
	}
}

func TestAgentLogPaths_PolecatWorker(t *testing.T) {
	town := "/town"
	paths := agentLogPaths(town, "te-worker", "testgt2", "polecat", "worker")
	want := filepath.Join(town, "logs", "sessions", "te-worker.log")
	if len(paths) == 0 || paths[0] != want {
		t.Fatalf("expected sessions log first, got %v", paths)
	}
}

func TestProcEnvironMatches(t *testing.T) {
	env := "GT_ROOT=/gt\x00GT_SESSION=te-testgt2-architect\x00GT_ROLE=testgt2/architect\x00"
	if !procEnvironMatches(env, "GT_SESSION", "te-testgt2-architect") {
		t.Fatal("expected GT_SESSION match")
	}
	if !procEnvironMatches(env, "GT_ROLE", "testgt2/architect") {
		t.Fatal("expected GT_ROLE match")
	}
	if procEnvironMatches(env, "GT_SESSION", "other") {
		t.Fatal("unexpected match")
	}
}

func TestFriendlyRigAgentName(t *testing.T) {
	if got := friendlyRigAgentName("testgt2", "polecat", "te-testgt2-polecat"); got != "Polecat (pipeline)" {
		t.Fatalf("got %q", got)
	}
	if got := friendlyRigAgentName("testgt2", "architect", "te-testgt2-architect"); got != "Architect" {
		t.Fatalf("got %q", got)
	}
}

func TestTailFileLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree"), 0644); err != nil {
		t.Fatal(err)
	}
	lines := tailFileLines(path, 2)
	if len(lines) != 2 || lines[0] != "two" || lines[1] != "three" {
		t.Fatalf("got %v", lines)
	}
}

func TestRigFlowStateRole(t *testing.T) {
	if rigFlowStateRole["qa_review"] != "qa" {
		t.Fatalf("qa_review role: %q", rigFlowStateRole["qa_review"])
	}
	if rigFlowStateRole["implementation"] != "polecat" {
		t.Fatalf("implementation role: %q", rigFlowStateRole["implementation"])
	}
}

func TestEnrichAgentsWithWorkflows(t *testing.T) {
	agents := []Agent{
		{ID: "te-testgt2-qa", Rig: "testgt2", Role: "qa"},
		{ID: "te-testgt2-architect", Rig: "testgt2", Role: "architect"},
	}
	workflows := []WorkflowInfo{{
		ID: "wf-1", Rig: "testgt2", CurrentState: "qa_review",
		Status: "running", ActiveRole: "qa",
	}}
	enrichAgentsWithWorkflows(agents, workflows)
	if !agents[0].WorkflowActive || agents[0].WorkflowState != "qa_review" {
		t.Fatalf("qa agent: %+v", agents[0])
	}
	if agents[1].WorkflowActive {
		t.Fatalf("architect should not be active: %+v", agents[1])
	}
}
