package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
)

// PlanExcerptForBead returns the ### bead section from plan.md for the given path, if present.
func PlanExcerptForBead(townRoot, rig, beadPath string) string {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	if beadPath == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(townRoot, rig, "mayor", "rig", "plan.md"))
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	var section []string
	inSection := false
	for _, line := range lines {
		if strings.HasPrefix(line, "### ") {
			if inSection {
				break
			}
			rest := strings.TrimPrefix(line, "### ")
			if idx := strings.Index(rest, ": "); idx >= 0 {
				pathPart := strings.TrimSpace(rest[idx+2:])
				if pathPart == beadPath || strings.HasSuffix(pathPart, "/"+filepath.Base(beadPath)) {
					inSection = true
					section = append(section, line)
					continue
				}
			}
			continue
		}
		if inSection {
			if strings.HasPrefix(line, "## ") && !strings.HasPrefix(line, "### ") {
				break
			}
			section = append(section, line)
		}
	}
	if len(section) == 0 {
		return ""
	}
	const maxPlanExcerpt = 1200
	out := strings.Join(section, "\n")
	if len(out) > maxPlanExcerpt {
		return out[:maxPlanExcerpt] + "\n…"
	}
	return out
}
