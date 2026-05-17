package orchestrator

import (
	"strings"
)

// PrepareWorkflowReworkFeedback stores concise cross-step context for the next agent.
// Raw attempt logs often contain grep noise and generic hints; planners need summary + bd list.
// Profile fields (required_files, qa_verify_command, layout_root) drive recovery text — never hardcode rig paths.
func PrepareWorkflowReworkFeedback(fromState, nextState, summary, rawFeedback string, v WorkflowValidation) string {
	summary = strings.TrimSpace(summary)
	rawFeedback = strings.TrimSpace(rawFeedback)
	if fromState == "plan_review" && nextState == "planning" {
		return truncateWorkflowText(preparePlanReviewToPlannerFeedback(summary, rawFeedback), maxWorkflowReworkFeedback)
	}
	if fromState == "qa_review" && nextState == "implementation" {
		return truncateWorkflowText(prepareQAReviewToImplementationFeedback(summary, rawFeedback, v), maxWorkflowReworkFeedback)
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

func prepareQAReviewToImplementationFeedback(summary, raw string, v WorkflowValidation) string {
	var b strings.Builder
	if summary != "" {
		b.WriteString("QA summary: ")
		b.WriteString(summary)
		b.WriteString("\n")
	}
	if paths := extractStubPathsFromFeedback(summary, raw); len(paths) > 0 {
		b.WriteString("\nFiles to fix (stubs or broken Python):\n")
		for _, p := range paths {
			b.WriteString("- ")
			b.WriteString(p)
			b.WriteString("\n")
		}
	}
	if err := extractLastTestFailure(raw); err != "" {
		b.WriteString("\nLast test failure:\n")
		b.WriteString(err)
		b.WriteString("\n")
	}
	if list := extractLastBdListOutput(raw); list != "" {
		b.WriteString("\nBead list from QA step:\n")
		b.WriteString(list)
		b.WriteString("\n")
	} else if list := extractLastBdListClosedOutput(raw); list != "" {
		b.WriteString("\nClosed implement beads from QA:\n")
		b.WriteString(list)
		b.WriteString("\n")
	}
	b.WriteString("\nRecovery steps (automated rig-flow):\n")
	b.WriteString("1. Use only bead IDs from bd list (rig prefix — copy exactly).\n")
	if req := v.RequirementsFilePath(); req != "" {
		b.WriteString("2. Install deps: `python3 -m pip install -r ")
		b.WriteString(req)
		b.WriteString("` before tests.\n")
	} else {
		b.WriteString("2. Install test dependencies if the workflow profile lists a requirements file.\n")
	}
	verify := strings.TrimSpace(v.UnittestCommandHint())
	if verify == "" {
		verify = "profile qa_verify_command"
	}
	b.WriteString("3. Run verification: `")
	b.WriteString(verify)
	b.WriteString("` — never paste shell into .py files.\n")
	layout := v.LayoutRootDir()
	b.WriteString("4. Replace stub/broken files under `")
	b.WriteString(layout)
	b.WriteString("/`; run tests until green; `bd close` each bead.\n")
	out := strings.TrimSpace(b.String())
	if out != "" {
		return out
	}
	return sanitizeAttemptFeedback(raw)
}

func extractStubPathsFromFeedback(summary, raw string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range []string{summary, raw} {
		for _, m := range strings.Split(s, " ") {
			m = strings.Trim(m, ".,;:\"'")
			if strings.Contains(m, "/") && (strings.HasSuffix(m, ".py") || strings.HasSuffix(m, ".js") || strings.HasSuffix(m, ".html")) {
				if !seen[m] {
					seen[m] = true
					out = append(out, m)
				}
			}
		}
	}
	return out
}

func extractLastTestFailure(raw string) string {
	lines := strings.Split(raw, "\n")
	var block []string
	capture := false
	for _, line := range lines {
		if strings.Contains(line, "pytest") || strings.Contains(line, "unittest") || strings.Contains(line, "SyntaxError") ||
			strings.Contains(line, "ModuleNotFoundError") || strings.Contains(line, "command failed") {
			capture = true
		}
		if capture {
			block = append(block, line)
			if len(block) > 40 {
				block = block[1:]
			}
		}
	}
	return strings.TrimSpace(strings.Join(block, "\n"))
}

func extractLastBdListClosedOutput(raw string) string {
	lines := strings.Split(raw, "\n")
	var cur, best []string
	flush := func() {
		if len(cur) == 0 {
			return
		}
		if countClosedBeadListLines(cur) >= 1 {
			best = append([]string(nil), cur...)
		}
		cur = nil
	}
	for _, line := range lines {
		if looksLikeClosedBdListLine(line) {
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

func looksLikeClosedBdListLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "✓") || strings.HasPrefix(trimmed, "✔")
}

func countClosedBeadListLines(lines []string) int {
	n := 0
	for _, line := range lines {
		if looksLikeClosedBdListLine(line) || looksLikeBdListLine(line) {
			n++
		}
	}
	return n
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
