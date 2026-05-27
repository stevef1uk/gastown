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

// rejectImplementationSuccessWithoutDisk blocks success JSON when the active/next bead file is absent.
func (r *stateRunner) rejectImplementationSuccessWithoutDisk(outcome string) (string, bool) {
	if r == nil || r.task == nil || r.task.State != "implementation" {
		return "", false
	}
	if !isOrchestratedSuccessOutcome(outcome) {
		return "", false
	}
	beadPath := strings.TrimSpace(r.activeImplementBeadPath())
	beadID := strings.TrimSpace(r.track.activeBead)
	if beadPath == "" {
		next, err := orchestrator.NextOpenImplementBead(r.townRoot, r.rig, r.v)
		if err != nil || next == nil {
			return "", false
		}
		beadID = next.ID
		beadPath = orchestrator.NormalizeBeadPathForLayout(
			orchestrator.ExtractPathFromBeadTitle(next.Title, r.v.BeadTitleContains), r.v.LayoutRoot)
	}
	if beadPath == "" {
		return "", false
	}
	rigDir := rigMayorRigDir(r.townRoot, r.rig)
	artifactErr := orchestrator.ValidateBeadArtifactOnDisk(rigDir, beadPath, r.v)
	if artifactErr == nil {
		return "", false
	}
	cleaned, cleanErr := r.cleanupCorruptedOpenImplementBeadFiles()
	if cleanErr != nil {
		orchestratedFprintfStderr("[gt-agent] corrupted-bead cleanup: %v\n", cleanErr)
	}
	var b strings.Builder
	b.WriteString("**Rejected:** success JSON does not write files — `")
	b.WriteString(beadPath)
	b.WriteString("` is missing on disk (")
	b.WriteString(artifactErr.Error())
	b.WriteString(").\n\n")
	if len(cleaned) > 0 {
		b.WriteString("Auto-cleanup removed corrupted open-bead artifacts so they can be rewritten:\n")
		for _, rel := range cleaned {
			b.WriteString("- `")
			b.WriteString(rel)
			b.WriteString("`\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("Use **WRITE:** or `CMD:` heredoc for this bead in **this** session, run **Verify**, then `bd close ")
	if beadID != "" {
		b.WriteString(beadID)
	} else {
		b.WriteString("<id-from-bd-list>")
	}
	b.WriteString("`. Do not claim the file is written until it exists on disk.")
	return b.String(), true
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
	if openImpl == 0 {
		if msg, blocked := r.implementationNoOpenBeadsButWorkRemainsNudge(); blocked {
			return "**Rejected:** success JSON while phase verify still blocked.\n\n" + msg, true
		}
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

// rejectImplementationFalseBeadInfraFailure blocks hallucinated "beads corrupted / reset .beads" failure JSON.
func (r *stateRunner) rejectImplementationFalseBeadInfraFailure(outcome, summary string) (string, bool) {
	if r == nil || r.task == nil || r.task.State != "implementation" {
		return "", false
	}
	if !isOrchestratedFailureOutcome(outcome) || !summaryClaimsFalseBeadInfraFailure(summary) {
		return "", false
	}
	if r.track != nil && r.track.bdInfraFailed {
		return "", false
	}
	if r.townRoot == "" || r.rig == "" || !orchestrator.BeadsDatabaseReady(r.townRoot, r.rig) {
		return "", false
	}
	example := beadIDExample(r.townRoot, r.rig)
	var b strings.Builder
	b.WriteString("**Rejected:** the beads database is working — do **not** reset or reinitialize `.beads`.\n\n")
	b.WriteString("Plain `bd list` only shows **open** issues (often role beads like witness/qa). Implement beads may be **closed**.\n")
	b.WriteString("Use:\n")
	b.WriteString("`CMD: export BEADS_DIR=$GT_ROOT/" + r.rig + "/.beads && cd " + r.rig + "/mayor/rig && bd list --status=closed --flat --limit=0`\n")
	b.WriteString("Pick a closed implement bead ID from that output, then `bd update " + example + " --status=open`, fix code, Verify, `bd close " + example + "`.\n")
	b.WriteString("Do not send failure JSON about bead corruption.")
	return b.String(), true
}

func summaryClaimsFalseBeadInfraFailure(summary string) bool {
	lower := strings.ToLower(strings.TrimSpace(summary))
	if lower == "" {
		return false
	}
	for _, needle := range []string{
		"bead system corrupt", "beads corrupt", "bead corruption", "corrupted bead",
		"reset .beads", "reinitialize bead", "re-init", "restore bead state",
		"bd list shows help", "bd list output to identify",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
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
		if msg, blocked := r.implementationNoOpenBeadsButWorkRemainsNudge(); blocked {
			return msg, true
		}
		return "", false
	}
	return r.implementationNoOpFailureNudge(openImpl), true
}

// implementationNoOpenBeadsButWorkRemainsNudge blocks failure/success hand-waving when the queue is empty
// but phase verify or QA rework still requires code changes.
func (r *stateRunner) implementationNoOpenBeadsButWorkRemainsNudge() (string, bool) {
	if r == nil {
		return "", false
	}
	if r.track != nil && r.track.bdInfraFailed {
		return "", false
	}
	if r.townRoot != "" && r.rig != "" && !orchestrator.BeadsDatabaseReady(r.townRoot, r.rig) {
		return "", false
	}
	if r.hasQAPendingRework() {
		return r.implementationNoOpFailureNudge(0), true
	}
	if orchestrator.WorkflowNeedsRuntimeSmoke(r.townRoot, r.rig, r.v) {
		if err := orchestrator.ImplementationPhaseVerifyOK(r.townRoot, r.rig, r.v); err != nil {
			var b strings.Builder
			b.WriteString("**Rejected:** all implement beads are closed but **phase verify** (compile + runtime smoke) still fails — implementation is not done.\n\n")
			if orchestrator.ImplementationVerifyNeedsRuntimeRework(err) {
				reopened, _ := orchestrator.ReopenImplementationBeadsAfterSmokeFailure(r.townRoot, r.rig, r.v, err)
				if block := orchestrator.FormatImplementationSmokeFailureBlock(r.townRoot, r.rig, r.v, err, reopened); block != "" {
					b.WriteString(block)
					b.WriteString("\n\n")
				}
			} else {
				b.WriteString(err.Error())
				b.WriteString("\n\n")
			}
			example := beadIDExample(r.townRoot, r.rig)
			b.WriteString("Reopen the affected bead (`bd list --status=closed`), `bd update " + example + " --status=open`, fix code, Verify, `bd close`. Do not send failure JSON.")
			return b.String(), true
		}
	}
	return "", false
}

func (r *stateRunner) formatActiveBeadCompileFailureForNudge() string {
	if r == nil || !orchestrator.WorkflowUsesGo(r.v) {
		return ""
	}
	beadPath := strings.TrimSpace(r.activeImplementBeadPath())
	if beadPath == "" {
		next, err := orchestrator.NextOpenImplementBead(r.townRoot, r.rig, r.v)
		if err != nil || next == nil {
			return ""
		}
		beadPath = orchestrator.NormalizeBeadPathForLayout(
			orchestrator.ExtractPathFromBeadTitle(next.Title, r.v.BeadTitleContains), r.v.LayoutRoot)
	}
	if beadPath == "" {
		return ""
	}
	return orchestrator.FormatImplementBeadCompileFailureBlock(rigMayorRigDir(r.townRoot, r.rig), beadPath, r.v)
}

// appendBdListImplementationHintIfNeeded clarifies when bd list omits closed implement beads.
func appendBdListImplementationHintIfNeeded(r *stateRunner, cmd, output string, combined *strings.Builder) {
	if r == nil || r.task == nil || r.task.State != "implementation" {
		return
	}
	lower := strings.ToLower(cmd)
	if !strings.Contains(lower, "bd list") || strings.Contains(lower, "grep -fi") {
		return
	}
	title := strings.TrimSpace(r.v.BeadTitleContains)
	if title == "" {
		return
	}
	outLower := strings.ToLower(output)
	if strings.Contains(outLower, strings.ToLower(title)) {
		return
	}
	if strings.Contains(outLower, "usage:") && strings.Contains(outLower, "bd [command]") {
		combined.WriteString("\n**Note:** bd printed CLI help — check command flags. Use `bd list --status=open,in_progress --flat --limit=0` or `--status=closed`.\n")
		return
	}
	combined.WriteString("\n**Note:** no implement beads in this output (titles containing ")
	combined.WriteString(title)
	combined.WriteString("). Closed implement beads are hidden unless you use `bd list --status=closed`. Phase verify may still require reopening them.\n")
}

func openImplementBeadCount(r *stateRunner) int {
	if r == nil {
		return 0
	}
	if strings.TrimSpace(r.v.BeadTitleContains) == "" && len(r.v.RequiredFiles) == 0 {
		return 0
	}
	n, err := countOpenMatchingBeads(r.townRoot, r.rig, r.v)
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
	if block := r.formatActiveBeadCompileFailureForNudge(); block != "" {
		b.WriteString(block)
		b.WriteString("\n\n")
	}
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
