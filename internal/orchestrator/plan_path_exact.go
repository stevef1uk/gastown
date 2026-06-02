package orchestrator

import (
	"fmt"
	"path/filepath"
	"strings"
)

// RequiresExactImplementPaths reports whether implement beads and plan.md must use full
// repo-relative paths from required_files (no basename-only matching). Enabled when the
// active phase lists nested module paths (internal/, cmd/, etc.) under layout_root.
func RequiresExactImplementPaths(v WorkflowValidation) bool {
	v = v.ForActivePhase()
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	nested := []string{"/internal/", "/cmd/", "/pkg/", "/api/", "/web/"}
	for _, f := range v.UnionRequiredFiles() {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if f == "" {
			continue
		}
		for _, pre := range nested {
			if strings.Contains(f, pre) {
				return true
			}
		}
		if layout != "" && layout != "." && strings.HasPrefix(f, layout+"/") {
			rest := strings.TrimPrefix(f, layout+"/")
			if strings.Count(rest, "/") >= 2 {
				return true
			}
		}
	}
	for _, f := range v.RequiredFiles {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if f == "" {
			continue
		}
		for _, pre := range nested {
			if strings.Contains(f, pre) {
				return true
			}
		}
		if layout != "" && layout != "." && strings.HasPrefix(f, layout+"/") {
			rest := strings.TrimPrefix(f, layout+"/")
			if strings.Count(rest, "/") >= 2 {
				return true
			}
		}
	}
	return false
}

// pathMatchesRequiredForProfile matches path against required_files using exact paths when
// RequiresExactImplementPaths; otherwise basename matching is allowed (flat rigs).
func pathMatchesRequiredForProfile(path string, required []string, v WorkflowValidation) bool {
	return pathMatchesRequiredMode(path, required, RequiresExactImplementPaths(v))
}

func pathMatchesRequiredMode(path string, required []string, exactOnly bool) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	for _, want := range required {
		want = filepath.ToSlash(strings.TrimSpace(want))
		if want == "" {
			continue
		}
		if path == want {
			return true
		}
		if !exactOnly && filepath.Base(path) == filepath.Base(want) {
			return true
		}
	}
	return false
}

// beadIDsForPathProfile returns bead IDs for want, using exact paths when the profile requires it.
func beadIDsForPathProfile(pathToIDs map[string][]string, want string, v WorkflowValidation) []string {
	return beadIDsForPathMode(pathToIDs, want, RequiresExactImplementPaths(v))
}

func beadIDsForPathMode(pathToIDs map[string][]string, want string, exactOnly bool) []string {
	want = filepath.ToSlash(strings.TrimSpace(want))
	if want == "" {
		return nil
	}
	if list, ok := pathToIDs[want]; ok {
		return list
	}
	if exactOnly {
		return nil
	}
	base := filepath.Base(want)
	var ids []string
	for p, idList := range pathToIDs {
		if filepath.Base(p) == base {
			ids = append(ids, idList...)
		}
	}
	return ids
}

// PathMatchesImplementFileForProfile matches a written file to an active bead path.
func PathMatchesImplementFileForProfile(written, beadPath string, v WorkflowValidation) bool {
	if RequiresExactImplementPaths(v) {
		written = filepath.ToSlash(strings.TrimSpace(written))
		beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
		if written == "" || beadPath == "" {
			return false
		}
		return written == beadPath
	}
	return PathMatchesImplementFile(written, beadPath)
}

// ValidatePlanBeadPathsExact ensures each open implement bead path equals a required_files entry
// when the profile uses nested layout paths. Complements ValidatePlanBeads basename matching legacy.
func ValidatePlanBeadPathsExact(beads []PlanBead, v WorkflowValidation, rig string) error {
	if !RequiresExactImplementPaths(v) {
		return nil
	}
	v = v.ForActivePhase()
	expected := make(map[string]bool)
	for _, f := range v.RequiredFiles {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if f != "" {
			expected[f] = true
		}
	}
	var bad []string
	for _, b := range beads {
		if !MatchesImplementBeadTitle(b.Title, v) {
			continue
		}
		p := NormalizePlannerBeadPath(ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains), v.LayoutRoot, rig)
		if p == "" {
			continue
		}
		if !expected[p] {
			bad = append(bad, fmt.Sprintf("%s (%q)", b.ID, p))
		}
	}
	if len(bad) == 0 {
		return nil
	}
	return fmt.Errorf("implement bead path must match required_files exactly (not basename only): %s", strings.Join(bad, ", "))
}
