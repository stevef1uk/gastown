package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// PlanBead is a minimal open-task view for plan-bead validation.
type PlanBead struct {
	ID    string
	Title string
}

// NormalizePlannerBeadPath canonicalizes paths from bead titles for flat mayor/rig worktrees.
// Models often prefix paths with the rig name (e.g. finally/Dockerfile) even when layout_root is ".".
func NormalizePlannerBeadPath(path, layoutRoot, rig string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	layoutRoot = strings.Trim(filepath.ToSlash(strings.TrimSpace(layoutRoot)), "/")
	rig = strings.TrimSpace(rig)
	if layoutRoot == "" || layoutRoot == "." {
		if rig != "" && strings.HasPrefix(path, rig+"/") {
			path = strings.TrimPrefix(path, rig+"/")
		}
	}
	if layoutRoot != "" && layoutRoot != "." {
		path = fixDoubledLayoutPath(path, layoutRoot)
	}
	return path
}

// MatchesImplementBeadTitle reports whether a bd task title is an implementation bead
// (canonical "Implement <path>" or common planner typos like "ImplementDockerfile").
func MatchesImplementBeadTitle(title string, v WorkflowValidation) bool {
	title = strings.TrimSpace(title)
	if title == "" {
		return false
	}
	pfx := v.BeadTitleContains
	lowerTitle := strings.ToLower(title)
	if strings.TrimSpace(pfx) != "" && strings.HasPrefix(lowerTitle, strings.ToLower(pfx)) {
		return true
	}
	// Fallback when profile prefix is layout-specific (e.g. "Implement linkshelf/") but the
	// planner emitted canonical "Implement go.mod per architecture".
	if strings.HasPrefix(lowerTitle, "implement ") &&
		(strings.Contains(lowerTitle, " per architecture") || strings.Contains(lowerTitle, " per arch")) {
		path := ExtractPathFromBeadTitle(title, v.BeadTitleContains)
		if path == "" || !IsValidImplementBeadPath(path) {
			return false
		}
		if len(v.RequiredFiles) == 0 {
			return pfx == "" || strings.HasPrefix(lowerTitle, strings.ToLower(pfx))
		}
		return pathMatchesRequired(path, v.RequiredFiles)
	}
	return false
}

// ExtractPathFromBeadTitle returns a repo-relative file path from an implementation bead title.
func ExtractPathFromBeadTitle(title, titlePrefix string) string {
	title = strings.TrimSpace(title)
	prefix := titlePrefix
	if strings.TrimSpace(prefix) != "" {
		lowerTitle := strings.ToLower(title)
		lowerPfx := strings.ToLower(prefix)
		if idx := strings.Index(lowerTitle, lowerPfx); idx >= 0 {
			title = strings.TrimSpace(title[idx+len(prefix):])
		} else if strings.HasPrefix(lowerTitle, "implement") && strings.HasPrefix(lowerPfx, "implement") {
			// Glued typo: ImplementDockerfile, Implement.env.example
			title = strings.TrimSpace(title[len("Implement"):])
		}
	}
	if before, _, ok := strings.Cut(title, " per architecture"); ok {
		title = strings.TrimSpace(before)
	}
	if before, _, ok := strings.Cut(title, " per arch"); ok {
		title = strings.TrimSpace(before)
	}
	return filepath.ToSlash(strings.TrimSpace(title))
}

// NormalizeBeadPathForLayout prefixes layout_root when a bead title or go tool output
// omitted it (e.g. internal/store/store.go → linkshelf/internal/store/store.go).
func NormalizeBeadPathForLayout(beadPath, layoutRoot string) string {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	layoutRoot = strings.Trim(strings.TrimSpace(layoutRoot), "/")
	if beadPath == "" || layoutRoot == "" || layoutRoot == "." {
		return beadPath
	}
	if strings.HasPrefix(beadPath, layoutRoot+"/") || beadPath == layoutRoot {
		return beadPath
	}
	if strings.Contains(beadPath, "..") {
		return beadPath
	}
	if beadPath == "go.mod" || beadPath == "go.sum" {
		return layoutRoot + "/" + beadPath
	}
	// Module-relative paths from titles (Implement linkshelf/…) or `cd layout && go build`.
	switch {
	case strings.HasPrefix(beadPath, "internal/"),
		strings.HasPrefix(beadPath, "cmd/"),
		strings.HasPrefix(beadPath, "pkg/"),
		strings.HasPrefix(beadPath, "api/"),
		strings.HasPrefix(beadPath, "web/"):
		return layoutRoot + "/" + beadPath
	}
	return beadPath
}

// ValidatePlanBeads checks open implementation beads against architecture and profile.
// rig is the Gas Town rig name (used to strip erroneous finally/… prefixes on flat worktrees).
func ValidatePlanBeads(beads []PlanBead, archPath string, v WorkflowValidation, rig string) error {
	v = v.ForActivePhase()
	if len(v.RequiredFiles) == 0 {
		return nil
	}
	expected := make([]string, 0, len(v.RequiredFiles))
	for _, f := range v.RequiredFiles {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if f != "" {
			expected = append(expected, f)
		}
	}
	if len(expected) == 0 {
		return nil
	}

	var impl []PlanBead
	for _, b := range beads {
		if b.ID == "" {
			continue
		}
		if !MatchesImplementBeadTitle(b.Title, v) {
			continue
		}
		impl = append(impl, b)
	}
	if len(impl) == 0 {
		return fmt.Errorf("no open beads matching %q", v.BeadTitleContains)
	}

	pathToIDs := map[string][]string{}
	for _, b := range impl {
		p := NormalizePlannerBeadPath(ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains), v.LayoutRoot, rig)
		if p == "" {
			return fmt.Errorf("bead %s title has no file path: %q", b.ID, b.Title)
		}
		if DoubledLayoutPath(p, v.LayoutRoot) {
			return fmt.Errorf("bead %s duplicates layout_root in path %q — use %s/Dockerfile not %s/%s/…",
				b.ID, p, v.LayoutRoot, v.LayoutRoot, v.LayoutRoot)
		}
		pathToIDs[p] = append(pathToIDs[p], b.ID)
	}

	var missing []string
	var dupes []string

	for _, want := range expected {
		ids := beadIDsForPath(pathToIDs, want)
		if len(ids) == 0 {
			missing = append(missing, want)
			continue
		}
		if len(ids) > 1 {
			dupes = append(dupes, fmt.Sprintf("%s (%s)", want, strings.Join(ids, ", ")))
		}
	}

	for p, ids := range pathToIDs {
		if len(ids) > 1 {
			dupes = append(dupes, fmt.Sprintf("%s (%s)", p, strings.Join(ids, ", ")))
		}
	}

	// Paths listed in architecture.md (backtick paths) must be covered when they match required_files.
	if archPath != "" {
		if data, err := os.ReadFile(archPath); err == nil {
			for _, p := range extractArchPaths(string(data), v.LayoutRoot) {
				for _, want := range expected {
					if want != p && filepath.Base(want) != filepath.Base(p) {
						continue
					}
					if len(beadIDsForPath(pathToIDs, want)) == 0 {
						missing = append(missing, want)
					}
					break
				}
			}
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing open bead(s) for architecture path(s): %s", strings.Join(dedupeStrings(missing), ", "))
	}
	if len(dupes) > 0 {
		return fmt.Errorf("duplicate open bead(s) for the same path: %s", strings.Join(dedupeStrings(dupes), "; "))
	}
	var extra []string
	for p, ids := range pathToIDs {
		if !pathMatchesRequired(p, expected) || !IsValidImplementBeadPath(p) {
			extra = append(extra, fmt.Sprintf("%s (%s)", p, strings.Join(ids, ", ")))
		}
	}
	if len(extra) > 0 {
		return fmt.Errorf("extra open bead(s) not in required_files (bd delete --force): %s", strings.Join(dedupeStrings(extra), "; "))
	}
	if len(impl) != len(expected) {
		return fmt.Errorf("open implementation beads (%d) must equal required_files (%d) — one bead per path, no extras", len(impl), len(expected))
	}
	return nil
}

var (
	archPathBoldRe = regexp.MustCompile("(?:\\*\\*)`([^`]+)`(?:\\*\\*)")
	archPathRe     = regexp.MustCompile("`([^`]+(?:\\.[a-zA-Z0-9]+)?)`")
)

// beadIDsForPath returns bead IDs covering a required/architecture path (exact or basename match).
func beadIDsForPath(pathToIDs map[string][]string, want string) []string {
	want = filepath.ToSlash(strings.TrimSpace(want))
	if want == "" {
		return nil
	}
	if list, ok := pathToIDs[want]; ok {
		return list
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

func extractArchPaths(archText, layoutRoot string) []string {
	seen := map[string]bool{}
	var out []string
	for _, re := range []*regexp.Regexp{archPathBoldRe, archPathRe} {
		for _, m := range re.FindAllStringSubmatch(archText, -1) {
			p := filepath.ToSlash(strings.TrimSpace(m[1]))
			if p == "" || seen[p] || !isLikelyRepoFilePath(p, layoutRoot) {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

func isLikelyRepoFilePath(p, layoutRoot string) bool {
	if strings.Contains(p, " ") {
		return false
	}
	lower := strings.ToLower(p)
	for _, prefix := range []string{"python3 ", "python ", "node ", "npm ", "npx ", "cd ", "export "} {
		if strings.HasPrefix(lower, prefix) {
			return false
		}
	}
	if layoutRoot != "" {
		return strings.HasPrefix(p, layoutRoot+"/") || strings.Contains(p, "/")
	}
	if strings.Contains(p, "/") {
		return true
	}
	return p == "Dockerfile" || strings.Contains(lower, "docker-compose") ||
		strings.HasSuffix(lower, ".env") || strings.HasSuffix(lower, ".example")
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
