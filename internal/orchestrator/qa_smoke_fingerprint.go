package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// QASmokeSourceFingerprint fingerprints handler + web files that affect HTTP smoke.
// Used to invalidate stale qa-review-progress runtime_smoke milestones (GT-VERIFY-006).
func QASmokeSourceFingerprint(townRoot, rig string, v WorkflowValidation) string {
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	var parts []string
	for _, rel := range qaSmokeFingerprintPaths(v) {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" {
			continue
		}
		abs := filepath.Join(rigDir, filepath.FromSlash(rel))
		info, err := os.Stat(abs)
		if err != nil {
			parts = append(parts, rel+":missing")
			continue
		}
		parts = append(parts, fmt.Sprintf("%s:%d:%d", rel, info.Size(), info.ModTime().UnixNano()))
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}

func qaSmokeFingerprintPaths(v WorkflowValidation) []string {
	seen := map[string]bool{}
	var out []string
	for _, rel := range append(append([]string(nil), v.RequiredFiles...), v.UnionRequiredFiles()...) {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" || seen[rel] {
			continue
		}
		lower := strings.ToLower(rel)
		if strings.Contains(lower, "/api/handlers") || strings.Contains(lower, "/web/") {
			seen[rel] = true
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out
}
