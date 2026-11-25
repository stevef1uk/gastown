package orchestrator

import (
	"encoding/json"
	"fmt"
	"strings"
)

type QAFinding struct {
	Summary       string   `json:"summary"`
	RepairBeadIDs []string `json:"repair_bead_ids,omitempty"`
	Evidence      string   `json:"evidence,omitempty"`
	Paths         []string `json:"paths,omitempty"`
}

// QAFailureReport names repair targets explicitly. IDs appearing in command
// output, inventories, and prose are diagnostics, not repair instructions.
type QAFailureReport struct {
	Summary       string      `json:"summary"`
	RepairBeadIDs []string    `json:"repair_bead_ids,omitempty"`
	Evidence      string      `json:"evidence,omitempty"`
	Findings      []QAFinding `json:"findings,omitempty"`
}

func ParseQAFailureReport(summary string) (QAFailureReport, bool) {
	var report QAFailureReport
	if json.Unmarshal([]byte(summary), &report) != nil || strings.TrimSpace(report.Summary) == "" {
		return report, false
	}
	return report, true
}

func qaFailureFindings(report QAFailureReport) []QAFinding {
	if len(report.Findings) == 0 {
		return nil
	}
	out := make([]QAFinding, 0, len(report.Findings))
	for _, finding := range report.Findings {
		finding.Summary = strings.TrimSpace(finding.Summary)
		finding.Evidence = strings.TrimSpace(finding.Evidence)
		if len(finding.RepairBeadIDs) == 0 {
			finding.RepairBeadIDs = report.RepairBeadIDs
		}
		if finding.Evidence == "" {
			finding.Evidence = report.Evidence
		}
		out = append(out, finding)
	}
	return out
}

func qaFailureRepairBeadIDs(report QAFailureReport) []string {
	ids := append([]string(nil), report.RepairBeadIDs...)
	for _, finding := range report.Findings {
		ids = append(ids, finding.RepairBeadIDs...)
	}
	for i, id := range ids {
		ids[i] = strings.TrimSpace(id)
	}
	return dedupeStrings(ids)
}

func QAFailureRepairBeadIDs(report QAFailureReport) []string {
	return qaFailureRepairBeadIDs(report)
}

// ValidateQAFailureReportFields checks report completeness, not bead ownership.
func ValidateQAFailureReportFields(summary string) error {
	report, structured := ParseQAFailureReport(summary)
	if !structured {
		return fmt.Errorf("a failure needs a nonempty summary, explicit repair_bead_ids and evidence")
	}
	findings := qaFailureFindings(report)
	if len(findings) == 0 {
		if len(report.RepairBeadIDs) == 0 {
			return fmt.Errorf("repair_bead_ids must list the actual defect-owner beads")
		}
		for _, id := range report.RepairBeadIDs {
			if strings.TrimSpace(id) == "" {
				return fmt.Errorf("repair_bead_ids must not contain empty IDs")
			}
		}
		if strings.TrimSpace(report.Evidence) == "" {
			return fmt.Errorf("evidence must be a separate, nonempty field describing the observed defect")
		}
		return nil
	}
	for i, finding := range findings {
		if finding.Summary == "" {
			return fmt.Errorf("findings[%d].summary must describe the defect", i)
		}
		if len(finding.RepairBeadIDs) == 0 {
			return fmt.Errorf("findings[%d].repair_bead_ids must list the actual defect-owner beads", i)
		}
		for _, id := range finding.RepairBeadIDs {
			if strings.TrimSpace(id) == "" {
				return fmt.Errorf("findings[%d].repair_bead_ids must not contain empty IDs", i)
			}
		}
		if finding.Evidence == "" {
			return fmt.Errorf("findings[%d].evidence must be a separate, nonempty field describing the observed defect", i)
		}
	}
	return nil
}

func qaFailureSummary(summary string) string {
	report, ok := ParseQAFailureReport(summary)
	if !ok {
		return summary
	}
	findings := qaFailureFindings(report)
	if len(findings) < 2 {
		return report.Summary
	}
	parts := make([]string, 0, len(findings))
	for i, finding := range findings {
		parts = append(parts, fmt.Sprintf("%d) %s", i+1, finding.Summary))
	}
	return report.Summary + " | " + strings.Join(parts, " | ")
}

// normalizeVerificationReviewResult supports older agents reporting shell errors
// as failure, without interpreting incidental shell noise as an app defect.
func normalizeVerificationReviewResult(townRoot, rig string, v WorkflowValidation, outcome, summary string) (string, string) {
	if IsVerificationErrorOutcome(outcome) || IsTimeoutOutcome(outcome) {
		return "verification_error", summary
	}
	report, structured := ParseQAFailureReport(summary)
	if (IsFailureOutcome(outcome) || IsArchitectureReworkOutcome(outcome)) &&
		IsQAAgentShellError(qaFailureSummary(summary)) && (!structured || len(qaFailureRepairBeadIDs(report)) == 0) {
		return "verification_error", qaFailureSummary(summary)
	}
	if !IsFailureOutcome(outcome) {
		return outcome, summary
	}
	invalid := func(reason string) (string, string) {
		return "verification_error", "QA repair report is incomplete: " + reason + ". Retry QA with explicit repair_bead_ids and evidence for actual defects. Original finding: " + qaFailureSummary(summary)
	}
	if err := ValidateQAFailureReportFields(summary); err != nil {
		return invalid(err.Error())
	}
	known := map[string]bool{}
	pathToID := map[string]string{}
	titleToID := map[string]string{}
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
				titleToID[bead.Title] = bead.ID
				titleToID[strings.TrimSpace(bead.Title)] = bead.ID
				if path != "" {
					pathToID[path] = bead.ID
					titleToID["Implement "+path] = bead.ID
					titleToID[path] = bead.ID
				}
			}
		}
	}
	resolveBeadID := func(raw string) string {
		raw = strings.TrimSpace(raw)
		if known[raw] {
			return raw
		}
		if id, ok := titleToID[raw]; ok {
			return id
		}
		if id, ok := pathToID[raw]; ok {
			return id
		}
		if p := ExtractPathFromBeadTitle(raw, v.BeadTitleContains); p != "" {
			p = NormalizePlannerBeadPath(p, v.LayoutRoot, rig)
			if id, ok := pathToID[p]; ok {
				return id
			}
		}
		return ""
	}
	if len(report.Findings) > 0 {
		var inScopeFindings []QAFinding
		for _, f := range report.Findings {
			var validIDs []string
			for _, id := range f.RepairBeadIDs {
				if realID := resolveBeadID(id); realID != "" {
					validIDs = append(validIDs, realID)
				}
			}
			if len(validIDs) == 0 {
				for _, p := range f.Paths {
					p = NormalizePlannerBeadPath(p, v.LayoutRoot, rig)
					if realID, ok := pathToID[p]; ok {
						validIDs = append(validIDs, realID)
					}
				}
			}
			if len(validIDs) > 0 {
				f.RepairBeadIDs = validIDs
				inScopeFindings = append(inScopeFindings, f)
			}
		}
		if len(inScopeFindings) == 0 {
			return invalid("all finding repair targets are outside current or completed delivery phases")
		}
		report.Findings = inScopeFindings
		if len(report.RepairBeadIDs) > 0 {
			var validTopIDs []string
			for _, id := range report.RepairBeadIDs {
				if realID := resolveBeadID(id); realID != "" {
					validTopIDs = append(validTopIDs, realID)
				}
			}
			report.RepairBeadIDs = validTopIDs
		}
		cleanBytes, err := json.Marshal(report)
		if err == nil {
			summary = string(cleanBytes)
		}
	} else {
		var validTopIDs []string
		for _, id := range qaFailureRepairBeadIDs(report) {
			if realID := resolveBeadID(id); realID != "" {
				validTopIDs = append(validTopIDs, realID)
			} else {
				return invalid(fmt.Sprintf("%q is not a canonical current-or-completed-phase implementation bead", id))
			}
		}
		if len(validTopIDs) > 0 {
			report.RepairBeadIDs = validTopIDs
			cleanBytes, err := json.Marshal(report)
			if err == nil {
				summary = string(cleanBytes)
			}
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
