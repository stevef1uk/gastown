package orchestrator

import (
	"strings"
)

// PrepareWorkflowReworkFeedback stores concise cross-step context for the next agent.
// Raw attempt logs often contain grep noise and generic hints; planners need summary + bd list.
func PrepareWorkflowReworkFeedback(fromState, nextState, summary, rawFeedback string) string {
	summary = strings.TrimSpace(summary)
	rawFeedback = strings.TrimSpace(rawFeedback)
	if fromState == "plan_review" && nextState == "planning" {
		return truncateWorkflowText(preparePlanReviewToPlannerFeedback(summary, rawFeedback), maxWorkflowReworkFeedback)
	}
	cleaned := sanitizeAttemptFeedback(rawFeedback)
	if summary != "" && cleaned != "" {
		return truncateWorkflowText("Summary: "+summary+"\n\n"+cleaned, maxWorkflowReworkFeedback)
	}
	if summary != "" {
		return truncateWorkflowText(summary, maxWorkflowReworkFeedback)
	}
	return truncateWorkflowText(cleaned, maxWorkflowReworkFeedback)
}

func preparePlanReviewToPlannerFeedback(summary, raw string) string {
	var b strings.Builder
	if summary != "" {
		b.WriteString("QA summary: ")
		b.WriteString(summary)
		b.WriteString("\n")
	}
	if PlanReviewSummarySaysPlanOK(summary) {
		b.WriteString("\nplan.md size is acceptable per QA — do not pad plan.md unless the summary says it is too small.\n")
		b.WriteString("Focus on duplicate/missing implementation beads only.\n")
	}
	if list := extractLastBdListOutput(raw); list != "" {
		b.WriteString("\nLast open bead list from QA step:\n")
		b.WriteString(list)
		b.WriteString("\n")
	}
	out := strings.TrimSpace(b.String())
	if out != "" {
		return out
	}
	return sanitizeAttemptFeedback(raw)
}

// PlanReviewSummarySaysPlanOK reports whether QA explicitly accepted plan.md size.
func PlanReviewSummarySaysPlanOK(summary string) bool {
	lower := strings.ToLower(summary)
	if !strings.Contains(lower, "plan.md") {
		return false
	}
	return strings.Contains(lower, " ok") ||
		strings.Contains(lower, "adequate") ||
		strings.Contains(lower, "sufficient")
}

func sanitizeAttemptFeedback(raw string) string {
	if raw == "" {
		return ""
	}
	var kept []string
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "grep:") {
			continue
		}
		if strings.HasPrefix(trimmed, "Use CMD:") {
			continue
		}
		if strings.Contains(trimmed, "When done, reply with JSON only") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// extractLastBdListOutput returns the last bd list --status=open block in command output.
func extractLastBdListOutput(raw string) string {
	lines := strings.Split(raw, "\n")
	var cur, best []string
	flush := func() {
		if len(cur) == 0 {
			return
		}
		if countBeadListLines(cur) >= 1 {
			best = append([]string(nil), cur...)
		}
		cur = nil
	}
	for _, line := range lines {
		if looksLikeBdListLine(line) {
			if len(cur) == 0 {
				cur = []string{line}
			} else {
				cur = append(cur, line)
			}
			continue
		}
		if len(cur) > 0 {
			if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "Command:") {
				cur = append(cur, line)
				continue
			}
			flush()
		}
	}
	flush()
	return strings.TrimSpace(strings.Join(best, "\n"))
}

func looksLikeBdListLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "○") || strings.HasPrefix(trimmed, "◯")
}

func countBeadListLines(lines []string) int {
	n := 0
	for _, line := range lines {
		if looksLikeBdListLine(line) {
			n++
		}
	}
	return n
}
