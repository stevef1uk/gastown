package refinery

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/constants"
)

func TestCommitMayorRigOrchestratorCheckpoint_skips(t *testing.T) {
	t.Parallel()
	if err := CommitMayorRigOrchestratorCheckpoint("/nonexistent/town", "r", "wf-1", "rig-flow", "kickoff", "design", "success"); err != nil {
		t.Fatal(err)
	}
	if err := CommitMayorRigOrchestratorCheckpoint("/nonexistent/town", "r", "wf-1", "other-flow", "a", "b", "success"); err != nil {
		t.Fatal(err)
	}
	if err := CommitMayorRigOrchestratorCheckpoint("/nonexistent/town", "", "wf-1", "rig-flow", "a", "b", "success"); err != nil {
		t.Fatal(err)
	}
	if err := CommitMayorRigOrchestratorCheckpoint("/nonexistent/town", "r", "wf-1", "rig-flow", "design", "design", "failure"); err != nil {
		t.Fatal(err)
	}
}

func TestCommitMayorRigOrchestratorCheckpoint_commit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	tmp := t.TempDir()
	townRoot := filepath.Join(tmp, "town")
	rigName := "myrig"
	rigPath := filepath.Join(townRoot, rigName)
	mayorRig := constants.RigMayorPath(rigPath)
	if err := os.MkdirAll(mayorRig, 0755); err != nil {
		t.Fatal(err)
	}

	gitSeq := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = mayorRig
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@local",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@local",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	gitSeq("init")
	if err := os.WriteFile(filepath.Join(mayorRig, "README.md"), []byte("hi\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitSeq("add", "README.md")
	gitSeq("commit", "-m", "init")

	if err := os.WriteFile(filepath.Join(mayorRig, "architecture.md"), []byte("# arch\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := CommitMayorRigOrchestratorCheckpoint(townRoot, rigName, "wf-abc", "rig-flow", "kickoff", "design", "success"); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("git", "-C", mayorRig, "log", "-1", "--format=%s%n%b")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "chore(orchestrator): checkpoint kickoff -> design") {
		t.Fatalf("unexpected log message: %q", string(out))
	}
	if !strings.Contains(string(out), "wf-abc") {
		t.Fatalf("expected workflow id in message: %q", string(out))
	}
}

func TestCommitMayorRigOrchestratorCheckpoint_cleanQACompletedCreatesMarker(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("GT_WORKFLOW_SKIP_PUSH", "1")

	tmp := t.TempDir()
	townRoot := filepath.Join(tmp, "town")
	rigName := "myrig"
	rigPath := filepath.Join(townRoot, rigName)
	mayorRig := constants.RigMayorPath(rigPath)
	if err := os.MkdirAll(mayorRig, 0755); err != nil {
		t.Fatal(err)
	}

	gitSeq := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = mayorRig
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@local",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@local",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	gitSeq("init")
	if err := os.WriteFile(filepath.Join(mayorRig, "README.md"), []byte("hi\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gitSeq("add", "README.md")
	gitSeq("commit", "-m", "init")

	if err := CommitMayorRigOrchestratorCheckpoint(townRoot, rigName, "wf-qa", "rig-flow", "qa_review", "completed", "all_passed"); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("git", "-C", mayorRig, "log", "-1", "--format=%s")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != "chore(orchestrator): qa approved [wf-qa]" {
		t.Fatalf("unexpected marker commit message: %q", got)
	}

	cmd = exec.Command("git", "-C", mayorRig, "rev-list", "--count", "HEAD")
	out, err = cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != "2" {
		t.Fatalf("expected marker commit to advance HEAD, count=%s", got)
	}
}
