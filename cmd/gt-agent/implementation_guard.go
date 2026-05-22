package main

import (
	"fmt"
	"strings"
)

// isRetriableLLMError reports API quota/rate-limit failures where completing the
// workflow step would cause a no-op loop (hands-off delivery should backoff and retry).
func isRetriableLLMError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "403") ||
		strings.Contains(s, "429") ||
		strings.Contains(s, "limit exceeded") ||
		strings.Contains(s, "rate limit") ||
		strings.Contains(s, "quota")
}

// noteImplementationFixAttempt marks that this task attempt did real fix work (not read-only inspection).
func (r *stateRunner) noteImplementationFixAttempt(cmd string, hadSuccessfulNative bool) {
	if r == nil || r.task == nil || r.task.State != "implementation" {
		return
	}
	if hadSuccessfulNative {
		r.attemptFixWork = true
		return
	}
	lower := strings.ToLower(strings.TrimSpace(cmd))
	if strings.Contains(lower, "bd update") || strings.Contains(lower, "bd close") {
		r.attemptFixWork = true
		return
	}
	if strings.Contains(lower, "sed -i") || strings.Contains(lower, " patch ") || strings.HasPrefix(lower, "patch ") {
		r.attemptFixWork = true
		return
	}
	if isImplementationVerifyCommandOK(cmd, r.townRoot, r.rig, r.track.activeBead, r.v) {
		r.attemptFixWork = true
	}
}

// rejectImplementationNoOpFailure blocks failure JSON when the polecat did not attempt a fix
// while open implement beads or QA rework still require hands-on work (prevents complete_task loops).
func (r *stateRunner) rejectImplementationNoOpFailure(outcome string) (string, bool) {
	if r == nil || r.task == nil || r.task.State != "implementation" {
		return "", false
	}
	if !isOrchestratedFailureOutcome(outcome) {
		return "", false
	}
	openImpl := openImplementBeadCount(r)
	if r.attemptEditSearchMiss && !r.attemptFixWork && (openImpl > 0 || r.hasQAPendingRework()) {
		return r.implementationEditSearchMissNudge(openImpl), true
	}
	if r.attemptFixWork {
		return "", false
	}
	if openImpl == 0 && !r.hasQAPendingRework() {
		return "", false
	}
	return r.implementationNoOpFailureNudge(openImpl), true
}

func openImplementBeadCount(r *stateRunner) int {
	if r == nil {
		return 0
	}
	title := strings.TrimSpace(r.v.BeadTitleContains)
	if title == "" {
		return 0
	}
	n, err := countOpenMatchingBeads(r.townRoot, r.rig, title)
	if err != nil {
		return 0
	}
	return n
}

func (r *stateRunner) implementationEditSearchMissNudge(openImpl int) string {
	var b strings.Builder
	b.WriteString("**Rejected:** EDIT failed because SEARCH did not match the file. Auto-READ output is in the feedback above — do not send failure JSON yet.\n\n")
	b.WriteString("Copy exact lines from **Auto-READ** (or ### Current file on disk) into a new **EDIT:** `<<<<<<< SEARCH` / `=======` / `>>>>>>> REPLACE` block, then run Verify.\n")
	if openImpl > 0 {
		b.WriteString(fmt.Sprintf("\n%d open implement bead(s) remain.\n", openImpl))
	}
	return b.String()
}

func (r *stateRunner) implementationNoOpFailureNudge(openImpl int) string {
	example := beadIDExample(r.townRoot, r.rig)
	var b strings.Builder
	b.WriteString("**Rejected:** You cannot send `{\"outcome\":\"failure\"}` without doing fix work in this session.\n\n")
	if r.hasQAPendingRework() {
		b.WriteString("QA returned this rig for implementation rework — read **Prior step failed** above and fix routes/code.\n")
	}
	if openImpl > 0 {
		b.WriteString(fmt.Sprintf("%d open implement bead(s) remain — use **Next bead** in the prompt.\n", openImpl))
	}
	b.WriteString("Required this turn: `CMD: bd update " + example + " --status=in_progress` then **EDIT:** / **WRITE:** or verify CMD, then `bd close`.\n")
	b.WriteString("Only send failure JSON after you ran verify/edit commands and they still block progress (name the bead ID from `bd list`).\n")
	if h := r.implementationArtifactFailureExtra(fmt.Errorf("open implement beads remain")); h != "" {
		b.WriteString("\n")
		b.WriteString(h)
	}
	return b.String()
}
