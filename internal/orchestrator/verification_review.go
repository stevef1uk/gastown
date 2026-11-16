package orchestrator

import (
	"encoding/json"
	"fmt"
	"strings"
)

// QAFailureReport names repair targets explicitly. IDs appearing in command
// output, inventories, and prose are diagnostics, not repair instructions.
type QAFailureReport struct {
	Summary       string   `json:"summary"`
	RepairBeadIDs []string `json:"repair_bead_ids"`
	Evidence      string   `json:"evidence"`
}

func ParseQAFailureReport(summary string) (QAFailureReport, bool) {
	var report QAFailureReport
	if json.Unmarshal([]byte(summary), &report) != nil || strings.TrimSpace(report.Summary) == "" {
		return report, false
	}
	return report, true
}

func qaFailureSummary(summary string) string {
	if report, ok := ParseQAFailureReport(summary); ok {
		return report.Summary
	}
	return summary
}

// normalizeVerificationReviewResult supports older agents reporting shell errors
// as failure, without interpreting incidental shell noise as an app defect.
func normalizeVerificationReviewResult(townRoot, rig string, v WorkflowValidation, outcome, summary string) (string, string) {
	if IsVerificationErrorOutcome(outcome) || IsTimeoutOutcome(outcome) {
		return "verification_error", summary
	}
	report, structured := ParseQAFailureReport(summary)
	if (IsFailureOutcome(outcome) || IsArchitectureReworkOutcome(outcome)) &&
		IsQAAgentShellError(qaFailureSummary(summary)) && (!structured || len(report.RepairBeadIDs) == 0) {
		return "verification_error", qaFailureSummary(summary)
	}
	if !IsFailureOutcome(outcome) {
		return outcome, summary
	}
	invalid := func(reason string) (string, string) {
		return "verification_error", "QA repair report is incomplete: " + reason + ". Retry QA with explicit repair_bead_ids and evidence for actual defects. Original finding: " + qaFailureSummary(summary)
	}
	if !structured || len(report.RepairBeadIDs) == 0 || strings.TrimSpace(report.Evidence) == "" {
		return invalid("a failure needs explicit repair targets and evidence; closed-bead listings are not repair targets")
	}
	known := map[string]bool{}
	activePhaseIndex := v.ActivePhaseIndex()
	for _, status := range []string{"closed", "open", "in_progress"} {
		beads, err := listImplementBeadsForGuard(townRoot, rig, v, status)
		if err != nil {
			return invalid("cannot resolve repair ownership: " + err.Error())
		}
		for _, bead := range beads {
			path := NormalizePlannerBeadPath(ExtractPathFromBeadTitle(bead.Title, v.BeadTitleContains), v.LayoutRoot, rig)
			phaseIndex := v.FindDeliveryPhaseForFile(path)
			inScope := pathMatchesRequiredForProfile(path, v.ForActivePhase().RequiredFiles, v)
			if v.HasPhasedDelivery() {
				inScope = activePhaseIndex >= 0 && phaseIndex >= 0 && phaseIndex <= activePhaseIndex
			}
			if inScope {
				known[bead.ID] = true
			}
		}
	}
	for _, id := range report.RepairBeadIDs {
		if !known[id] {
			return invalid(fmt.Sprintf("%q is not a canonical current-or-completed-phase implementation bead", id))
		}
	}
	return outcome, summary
}

func (inst *WorkflowInstance) recordVerificationError(state, outcome string, limit int) {
	if limit <= 0 {
		return
	}
	if !IsVerificationErrorOutcome(outcome) {
		delete(inst.VerificationErrors, state)
		return
	}
	if inst.VerificationErrors == nil {
		inst.VerificationErrors = map[string]int{}
	}
	inst.VerificationErrors[state]++
	if inst.VerificationErrors[state] >= limit {
		inst.Status = "paused"
		inst.PauseReason = fmt.Sprintf("QA verification paused after %d execution/reporting errors; correct the command or environment before resuming", inst.VerificationErrors[state])
		if inst.PendingRework != nil {
			inst.PauseReason += ": " + truncateWorkflowText(inst.PendingRework.Summary, maxWorkflowReworkSummary)
			inst.PendingRework.Feedback += fmt.Sprintf("\n\nWorkflow paused after %d QA verification execution/reporting errors. Correct the command or environment before resuming. Preserve implementation files and closed beads.", inst.VerificationErrors[state])
		}
	}
}

func verificationRetryFeedback(v WorkflowValidation, feedback string) string {
	return truncateWorkflowText("Verification did not establish an application defect. Retry QA, not implementation. From mayor/rig run the configured phase verification directly: "+v.ActivePhaseQAVerifyCommand()+"\nDo not prepend broad pkill/killall/fuser cleanup or reopen beads. If verification passes, complete the normal SPEC review before approving.\n\n"+feedback, maxWorkflowReworkFeedback)
}
