package specprofile

import (
	"fmt"
	"strings"
)

const specSummaryMaxRunes = 450

// FormatOperatorWorkflowProfileNotice returns plain text for printing after a profile
// is written: what the file is for, key extracted fields, and how to fix mistakes.
func FormatOperatorWorkflowProfileNotice(absProfilePath, rigName string, p *ProfileFile) string {
	if p == nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Workflow profile (orchestrator)\n")
	fmt.Fprintf(&b, "  File: %s\n", absProfilePath)
	fmt.Fprintf(&b, "  This JSON is merged on top of the rig-flow template so gt-agent and QA use the\n")
	fmt.Fprintf(&b, "  right test command, bead title filter, and prompt hints from SPEC.md.\n")
	if strings.TrimSpace(p.Confidence) != "" {
		fmt.Fprintf(&b, "  Model confidence: %s\n", strings.TrimSpace(p.Confidence))
	}
	v := p.Validation
	fmt.Fprintf(&b, "  Extracted (review these):\n")
	add := func(label, val string) {
		val = strings.TrimSpace(val)
		if val == "" {
			val = "(empty)"
		}
		fmt.Fprintf(&b, "    - %s: %s\n", label, val)
	}
	add("bead_title_contains", v.BeadTitleContains)
	add("test_runner", v.TestRunner)
	add("unittest_module", v.UnittestModule)
	add("qa_verify_command", v.QAVerifyCommand)
	add("layout_root", v.LayoutRoot)
	if len(v.RequiredFiles) > 0 {
		max := 8
		if len(v.RequiredFiles) < max {
			max = len(v.RequiredFiles)
		}
		files := append([]string(nil), v.RequiredFiles[:max]...)
		list := strings.Join(files, ", ")
		if len(v.RequiredFiles) > max {
			list += fmt.Sprintf(", … (+%d more)", len(v.RequiredFiles)-max)
		}
		fmt.Fprintf(&b, "    - required_files: %s\n", list)
	} else {
		fmt.Fprintf(&b, "    - required_files: (empty)\n")
	}
	sum := strings.TrimSpace(v.SpecSummary)
	if sum != "" {
		runes := []rune(sum)
		if len(runes) > specSummaryMaxRunes {
			sum = string(runes[:specSummaryMaxRunes]) + "…"
		}
		indented := strings.ReplaceAll(sum, "\n", "\n      ")
		fmt.Fprintf(&b, "    - spec_summary:\n      %s\n", indented)
	}
	fmt.Fprintf(&b, "  If anything is wrong, edit that JSON by hand or run:\n")
	fmt.Fprintf(&b, "    gt rig spec-index %s --force\n", rigName)
	fmt.Fprintf(&b, "  Skip auto-index on future rig adds: export GT_SKIP_SPEC_INDEX=1\n")
	return strings.TrimRight(b.String(), "\n")
}
