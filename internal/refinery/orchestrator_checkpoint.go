package refinery

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/git"
)

// hasBinaryFiles returns true if there are binary (non-text) files among
// the uncommitted changes in the git repository at workDir.
func hasBinaryFiles(workDir string) (bool, error) {
	run := func(args ...string) (string, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = workDir
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	lines, err := run("diff", "--cached", "--numstat", "-z")
	if err != nil && !strings.Contains(err.Error(), "empty") {
		return false, err
	}
	if lines == "" {
		lines, err = run("diff", "--numstat", "-z")
		if err != nil && !strings.Contains(err.Error(), "empty") {
			return false, err
		}
	}
	if lines == "" {
		lines, err = run("ls-files", "--others", "--exclude-standard", "-z")
		if err != nil && !strings.Contains(err.Error(), "empty") {
			return false, err
		}
	}

	if strings.Contains(lines, "\t-\t") {
		return true, nil
	}

	if lines != "" {
		parts := strings.Split(lines, "\x00")
		for _, path := range parts {
			if path != "" {
				fullPath := filepath.Join(workDir, path)
				data, rerr := os.ReadFile(fullPath)
				if rerr != nil {
					continue
				}
				for i := 0; i < len(data) && i < 8192; i++ {
					if data[i] == 0 {
						return true, nil
					}
				}
			}
		}
	}
	return false, nil
}

// CommitMayorRigOrchestratorCheckpoint stages and commits all tracked and untracked
// changes in mayor/rig after an orchestrator rig-flow FSM transition. This mirrors
// legacy Gas Town refinery ownership of rig git history (canonical commits on the
// worktree agents use).
//
// When the workflow reaches state "completed", it also pushes the current branch
// to origin (git push -u origin <branch>) so checkpoint commits land on the upstream
// remote. Opt out of push with GT_WORKFLOW_SKIP_PUSH=1 (commits still run unless
// GT_SKIP_WORKFLOW_GIT_COMMIT=1).
//
// Skipped when: GT_SKIP_WORKFLOW_GIT_COMMIT is set, template is not rig-flow, rig is
// unknown, fromState == toState (no real edge), worktree is missing or not a repo.
// Push is skipped for detached HEAD or when GT_WORKFLOW_SKIP_PUSH is set.
func CommitMayorRigOrchestratorCheckpoint(townRoot, rigName, workflowID, templateID, fromState, toState, outcome string) error {
	if os.Getenv("GT_SKIP_WORKFLOW_GIT_COMMIT") != "" {
		return maybePushMayorRigOnCompleted(townRoot, rigName, templateID, fromState, toState)
	}
	if templateID != "rig-flow" || rigName == "" {
		return nil
	}
	if fromState == toState {
		return nil
	}

	fmt.Printf("[Orchestrator] rig %s mayor/rig: rig-flow transition %s -> %s\n", rigName, fromState, toState)

	rigPath := filepath.Join(townRoot, rigName)
	mayorRig := constants.RigMayorPath(rigPath)
	if st, err := os.Stat(mayorRig); err != nil || !st.IsDir() {
		return nil
	}

	g := git.NewGit(mayorRig)
	if !g.IsRepo() {
		return nil
	}

	dirty, err := g.HasUncommittedChanges()
	if err != nil {
		return fmt.Errorf("mayor/rig status: %w", err)
	}
	if dirty {
		if hasBin, berr := hasBinaryFiles(mayorRig); berr != nil {
			fmt.Printf("[Orchestrator] rig %s: warning: could not check for binary files: %v\n", rigName, berr)
		} else if hasBin {
			fmt.Printf("[Orchestrator] rig %s: SKIP checkpoint commit — binary files detected in worktree\n", rigName)
			return maybePushMayorRigOnCompleted(townRoot, rigName, templateID, fromState, toState)
		}
		if err := g.Add("-A"); err != nil {
			return fmt.Errorf("git add: %w", err)
		}

		msg := buildOrchestratorTransitionMessage(workflowID, fromState, toState, outcome)
		if err := g.Commit(msg); err != nil {
			if strings.Contains(err.Error(), "nothing to commit") {
				// Index clean after add (e.g. skip-worktree); still may need push below.
			} else {
				return fmt.Errorf("git commit: %w", err)
			}
		} else {
			fmt.Printf("[Orchestrator] rig %s mayor/rig: checkpoint commit (%s -> %s)\n", rigName, fromState, toState)
		}
	} else if isQAApprovedTransition(templateID, fromState, toState) {
		msg := buildQAApprovedMessage(workflowID)
		if err := g.CommitAllowEmpty(msg); err != nil {
			return fmt.Errorf("git commit qa marker: %w", err)
		}
		fmt.Printf("[Orchestrator] rig %s mayor/rig: QA-approved marker commit\n", rigName)
	} else {
		fmt.Printf("[Orchestrator] rig %s mayor/rig: worktree clean at %s -> %s (no checkpoint commit)\n", rigName, fromState, toState)
	}

	return maybePushMayorRigOnCompleted(townRoot, rigName, templateID, fromState, toState)
}

// SyncMayorRigUpstream commits any dirty mayor/rig worktree and pushes the current branch
// to origin. Use after a rig-flow run when the orchestrator was not restarted with
// checkpoint/push support, or to publish local commits manually.
func SyncMayorRigUpstream(townRoot, rigName string) error {
	if rigName == "" {
		return fmt.Errorf("rig name required")
	}
	rigPath := filepath.Join(townRoot, rigName)
	mayorRig := constants.RigMayorPath(rigPath)
	if st, err := os.Stat(mayorRig); err != nil || !st.IsDir() {
		return fmt.Errorf("mayor/rig not found at %s", mayorRig)
	}
	g := git.NewGit(mayorRig)
	if !g.IsRepo() {
		return fmt.Errorf("%s is not a git repository", mayorRig)
	}
	dirty, err := g.HasUncommittedChanges()
	if err != nil {
		return fmt.Errorf("status: %w", err)
	}
	if dirty {
		if hasBin, berr := hasBinaryFiles(mayorRig); berr != nil {
			fmt.Printf("[Orchestrator] rig %s: warning: could not check for binary files: %v\n", rigName, berr)
		} else if hasBin {
			fmt.Printf("[Orchestrator] rig %s: SKIP sync commit — binary files detected in worktree\n", rigName)
			return maybePushMayorRigOnCompleted(townRoot, rigName, "rig-flow", "sync", "completed")
		}
		if err := g.Add("-A"); err != nil {
			return fmt.Errorf("git add: %w", err)
		}
		msg := "chore(orchestrator): sync upstream (manual)"
		if err := g.Commit(msg); err != nil && !strings.Contains(err.Error(), "nothing to commit") {
			return fmt.Errorf("git commit: %w", err)
		}
		fmt.Printf("[Orchestrator] rig %s mayor/rig: committed uncommitted changes\n", rigName)
	}
	return maybePushMayorRigOnCompleted(townRoot, rigName, "rig-flow", "sync", "completed")
}

func maybePushMayorRigOnCompleted(townRoot, rigName, templateID, fromState, toState string) error {
	if toState != "completed" || templateID != "rig-flow" || rigName == "" {
		return nil
	}
	if os.Getenv("GT_WORKFLOW_SKIP_PUSH") != "" {
		return nil
	}
	if fromState == toState {
		return nil
	}

	rigPath := filepath.Join(townRoot, rigName)
	mayorRig := constants.RigMayorPath(rigPath)
	if st, err := os.Stat(mayorRig); err != nil || !st.IsDir() {
		return nil
	}

	g := git.NewGit(mayorRig)
	if !g.IsRepo() {
		return nil
	}

	branch, err := g.CurrentBranch()
	if err != nil || branch == "" || branch == "HEAD" {
		return fmt.Errorf("mayor/rig push skipped: not on a named branch (detached HEAD?)")
	}

	if _, err := g.RemoteURL("origin"); err != nil {
		return fmt.Errorf("mayor/rig push: no origin remote: %w", err)
	}

	if err := g.PushSetUpstream("origin", branch); err != nil {
		return fmt.Errorf("mayor/rig push to origin/%s: %w", branch, err)
	}
	fmt.Printf("[Orchestrator] rig %s mayor/rig: pushed %s to origin (rig-flow completed)\n", rigName, branch)
	return nil
}

func buildOrchestratorTransitionMessage(workflowID, fromState, toState, outcome string) string {
	if isQAApprovedTransition("rig-flow", fromState, toState) {
		return buildQAApprovedMessage(workflowID)
	}
	return buildOrchestratorCheckpointMessage(workflowID, fromState, toState, outcome)
}

func buildOrchestratorCheckpointMessage(workflowID, fromState, toState, outcome string) string {
	const prefix = "chore(orchestrator): checkpoint"
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s -> %s", prefix, fromState, toState)
	if outcome != "" {
		fmt.Fprintf(&b, " (%s)", outcome)
	}
	if workflowID != "" {
		fmt.Fprintf(&b, " [%s]", workflowID)
	}
	return b.String()
}

func buildQAApprovedMessage(workflowID string) string {
	msg := "chore(orchestrator): qa approved"
	if workflowID != "" {
		msg += fmt.Sprintf(" [%s]", workflowID)
	}
	return msg
}

func isQAApprovedTransition(templateID, fromState, toState string) bool {
	return templateID == "rig-flow" && fromState == "qa_review" && toState == "completed"
}
