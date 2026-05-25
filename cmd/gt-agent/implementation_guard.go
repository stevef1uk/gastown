package main

import (
	"fmt"
	"strings"

	"github.com/steveyegge/gastown/internal/orchestrator"
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

// rejectImplementationPrematureSuccess blocks success JSON while verify/compile still fails
// (e.g. unused import in handlers_test.go while the active bead is handlers.go).
func (r *stateRunner) rejectImplementationPrematureSuccess(outcome string) (string, bool) {
	if r == nil || r.task == nil || r.task.State != "implementation" {
		return "", false
	}
	if !isOrchestratedSuccessOutcome(outcome) {
		return "", false
	}
	openImpl := openImplementBeadCount(r)
	if openImpl == 0 && orchestrator.WorkflowUsesGo(r.v) && orchestrator.WorkflowNeedsRuntimeSmoke(r.v) {
		if err := orchestrator.ImplementationPhaseVerifyOK(r.townRoot, r.rig, r.v); err != nil {
			if orchestrator.ImplementationVerifyNeedsRuntimeRework(err) {
				reopened, _ := orchestrator.ReopenImplementationBeadsAfterSmokeFailure(r.townRoot, r.rig, r.v, err)
				block := orchestrator.FormatImplementationSmokeFailureBlock(r.townRoot, r.rig, r.v, err, reopened)
				if block != "" {
					return "**Rejected:** success JSON while runtime smoke still fails.\n\n" + block, true
				}
			}
			return "**Rejected:** success JSON while phase verify still fails: " + err.Error() + "\n\nFix compile/smoke, reopen affected beads, then verify before success.", true
		}
		return "", false
	}
	if openImpl == 0 {
		return "", false
	}
	out := strings.TrimSpace(r.track.lastVerifyOutput)
	if out == "" {
		return "", false
	}
	if orchestrator.GoCompileOutputHasUnusedImport(out) {
		return r.implementationPrematureSuccessNudge(openImpl), true
	}
	if !r.track.verifyOK && r.track.hadCmdFailure && implementationCompileStillBlocked(out) {
		return r.implementationPrematureSuccessNudge(openImpl), true
	}
	return "", false
}

func implementationCompileStillBlocked(verifyOutput string) bool {
	lower := strings.ToLower(verifyOutput)
	if strings.Contains(lower, "[build failed]") || strings.Contains(lower, "build failed") {
		return true
	}
	if strings.Contains(verifyOutput, ".go:") &&
		(strings.Contains(lower, "fail") || strings.Contains(lower, "error") || strings.Contains(lower, "undefined:")) {
		return true
	}
	return false
}

func (r *stateRunner) implementationPrematureSuccessNudge(openImpl int) string {
	var b strings.Builder
	b.WriteString("**Rejected:** success JSON while verify/compile still fails (see command output above).\n\n")
	b.WriteString("Fix compile errors first (often `goimports` on the **test file** in the same package), run **Verify** from Next bead, then `bd close`.\n")
	b.WriteString(fmt.Sprintf("%d open implement bead(s) remain.\n", openImpl))
	if h := orchestrator.FormatUnusedImportCompileHint(r.track.lastVerifyOutput); h != "" {
		b.WriteString("\n")
		b.WriteString(h)
		b.WriteString("\n")
	}
	return b.String()
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
