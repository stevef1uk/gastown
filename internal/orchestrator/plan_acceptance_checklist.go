package orchestrator

import (
	"path/filepath"
	"strings"
)

// FormatPlanAcceptanceChecklist returns plan.md acceptance bullets plus profile defaults.
func FormatPlanAcceptanceChecklist(townRoot, rig, beadPath string, v WorkflowValidation) string {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	if beadPath == "" {
		return ""
	}
	var bullets []string
	if excerpt := PlanExcerptForBead(townRoot, rig, beadPath); excerpt != "" {
		bullets = append(bullets, acceptanceBulletsFromPlanExcerpt(excerpt)...)
	}
	for _, b := range planAcceptanceBullets(beadPath, v) {
		bullets = appendUniqueString(bullets, b)
	}
	if len(bullets) == 0 {
		return ""
	}
	var out strings.Builder
	out.WriteString("### Acceptance checklist\n")
	for _, b := range bullets {
		out.WriteString("- ")
		out.WriteString(b)
		out.WriteString("\n")
	}
	return strings.TrimSpace(out.String())
}

func acceptanceBulletsFromPlanExcerpt(excerpt string) []string {
	var out []string
	for _, line := range strings.Split(excerpt, "\n") {
		trim := strings.TrimSpace(line)
		if !strings.HasPrefix(trim, "- ") {
			continue
		}
		body := strings.TrimSpace(strings.TrimPrefix(trim, "- "))
		if body == "" {
			continue
		}
		lower := strings.ToLower(body)
		if strings.HasPrefix(lower, "scope:") {
			continue
		}
		if strings.HasPrefix(lower, "acceptance:") {
			body = strings.TrimSpace(body[len("acceptance:"):])
		}
		if body != "" {
			out = append(out, body)
		}
	}
	return out
}

func appendUniqueString(ss []string, s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ss
	}
	for _, existing := range ss {
		if existing == s {
			return ss
		}
	}
	return append(ss, s)
}
