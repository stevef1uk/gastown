package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var planBeadsDebug bool

func init() {
	for _, v := range strings.Split(os.Getenv("GT_DEBUG"), ",") {
		if strings.TrimSpace(v) == "plan_beads" {
			planBeadsDebug = true
			break
		}
	}
}

func pbDebug(format string, args ...interface{}) {
	if planBeadsDebug {
		fmt.Fprintf(os.Stderr, "[plan_beads] "+format+"\n", args...)
	}
}

// PlanBead is a minimal open-task view for plan-bead validation.
type PlanBead struct {
	ID    string
	Title string
}

// NormalizePlannerBeadPath canonicalizes paths from bead titles for flat mayor/rig worktrees.
// Models often prefix paths with the rig name (e.g. finally/Dockerfile) even when layout_root is ".".
// NormalizePlannerBeadPath normalizes a bead path for planning and matching.
// It strips the rig-name prefix when it's not the layout root (hallucinated prefix),
// but preserves it when layout_root matches the rig name (legitimate project directory).
func NormalizePlannerBeadPath(path, layoutRoot, rig string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	layoutRoot = strings.Trim(filepath.ToSlash(strings.TrimSpace(layoutRoot)), "/")
	rig = strings.TrimSpace(rig)
	// Strip leading ./ (bead titles like "Implement FinAlly/./backend/llm/client.py")
	for strings.HasPrefix(path, "./") {
		path = path[2:]
	}
	// Strip hallucinated rig-name prefix only when it's not the layout root.
	// When layout_root == rig, the rig name is a legitimate directory in the project.
	if rig != "" && !strings.EqualFold(rig, layoutRoot) {
		// Case-insensitive rig prefix stripping: bead titles may use mixed case
		// (e.g. "FinAlly/") while rig name is lowercase ("finally").
		for {
			idx := strings.Index(path, "/")
			if idx <= 0 {
				break
			}
			firstSeg := path[:idx]
			if !strings.EqualFold(firstSeg, rig) {
				break
			}
			path = path[idx+1:]
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
		ok := implementBeadTitlePathOK(title, v)
		pbDebug("MatchesImplementBeadTitle (branch1) title=%q pfx=%q => %v", title, pfx, ok)
		return ok
	}
	// Fallback when profile prefix is layout-specific (e.g. "Implement linkshelf/") but the
	// planner emitted canonical "Implement go.mod per architecture".
	if strings.HasPrefix(lowerTitle, "implement ") &&
		(strings.Contains(lowerTitle, " per architecture") || strings.Contains(lowerTitle, " per arch")) {
		ok := implementBeadTitlePathOK(title, v)
		pbDebug("MatchesImplementBeadTitle (branch2 fallback) title=%q pfx=%q => %v", title, pfx, ok)
		return ok
	}
	pbDebug("MatchesImplementBeadTitle false (no branch): title=%q pfx=%q lowerTitle=%q lowerPfx=%q", title, pfx, lowerTitle, strings.ToLower(pfx))
	return false
}

// implementBeadTitlePathOK validates the file path embedded in an implement-like title.
func implementBeadTitlePathOK(title string, v WorkflowValidation) bool {
	path := ExtractPathFromBeadTitle(title, v.BeadTitleContains)
	layout := effectiveLayoutRootForBeadTitle(v)
	path = NormalizeBeadPathForLayout(path, layout)
	if layout != "" {
		path = fixDoubledLayoutPath(path, layout)
	}
	if path == "" || !IsValidImplementBeadPath(path) {
		pbDebug("implementBeadTitlePathOK false: title=%q BeadTitleContains=%q LayoutRoot=%q path=%q IsValid=%v layout=%q", title, v.BeadTitleContains, v.LayoutRoot, path, IsValidImplementBeadPath(path), layout)
		return false
	}
	if len(v.RequiredFiles) == 0 {
		pfx := strings.TrimSpace(v.BeadTitleContains)
		if pfx == "" {
			return true
		}
		return strings.HasPrefix(strings.ToLower(strings.TrimSpace(title)), strings.ToLower(pfx))
	}
	// For nested/exact profiles, only accept bead titles whose embedded path matches
	// required_files (prevents queue-matching flattened handlers/main.go titles).
	if RequiresExactImplementPaths(v) {
		result := pathMatchesRequiredForProfile(path, requiredFilesWithCorrelatedTests(v.RequiredFiles, v), v)
		pbDebug("implementBeadTitlePathOK exact: title=%q path=%q result=%v v.RequiredFiles=%v", title, path, result, v.RequiredFiles)
		return result
	}

	// For flat/non-exact profiles, accept any valid implement path.
	// ValidatePlanBeads will later classify mismatches as "extra open bead(s)".
	layoutRoot := effectiveLayoutRootForBeadTitle(v)
	if layoutRoot != "" {
		result := path == layoutRoot || strings.HasPrefix(path, layoutRoot+"/")
		pbDebug("implementBeadTitlePathOK layoutRoot: title=%q path=%q layoutRoot=%q result=%v", title, path, layoutRoot, result)
		return result
	}
	// layoutRoot is empty — accepted
	return true
}

// effectiveLayoutRootForBeadTitle returns LayoutRoot or infers it from BeadTitleContains (e.g. "Implement finally/").
func effectiveLayoutRootForBeadTitle(v WorkflowValidation) string {
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "." {
		// layout_root "." means no real layout root — do NOT infer from prefix.
		// The prefix may contain a rig-name (e.g. "Implement FinAlly/") which is NOT
		// a real directory and should be stripped by NormalizeBeadPathForLayout.
		return ""
	}
	if layout != "" {
		return layout
	}
	pfx := strings.TrimSpace(v.BeadTitleContains)
	if !strings.HasSuffix(pfx, "/") {
		return ""
	}
	parts := strings.Split(strings.TrimSuffix(pfx, "/"), " ")
	if len(parts) == 0 {
		return ""
	}
	candidate := parts[len(parts)-1]
	if strings.EqualFold(candidate, "implement") {
		return ""
	}
	return candidate
}

// ExtractPathFromBeadTitle returns a repo-relative file path from an implementation bead title.
func ExtractPathFromBeadTitle(title, titlePrefix string) string {
	title = strings.TrimSpace(title)
	if strings.TrimSpace(titlePrefix) == "" {
		if strings.HasPrefix(strings.ToLower(title), "implement ") {
			title = strings.TrimSpace(title[len("Implement"):])
		}
	}
	prefix := titlePrefix
	if strings.TrimSpace(prefix) != "" {
		lowerTitle := strings.ToLower(title)
		lowerPfx := strings.ToLower(prefix)
		if idx := strings.Index(lowerTitle, lowerPfx); idx >= 0 {
			title = strings.TrimSpace(title[idx+len(prefix):])
			// If the prefix included a layout root (e.g. "Implement pingapp/"), restore it
			if strings.HasSuffix(prefix, "/") {
				parts := strings.Split(strings.TrimSuffix(prefix, "/"), " ")
				if len(parts) > 0 {
					layout := parts[len(parts)-1]
					if layout != "" && !strings.EqualFold(layout, "implement") {
						title = layout + "/" + title
					}
				}
			}
		} else if strings.HasPrefix(lowerTitle, "implement") && strings.HasPrefix(lowerPfx, "implement") {
			// Glued typo: ImplementDockerfile, Implement.env.example
			title = strings.TrimSpace(title[len("Implement"):])
		} else if strings.HasPrefix(lowerTitle, "implement ") {
			// Legacy planner titles when profile prefix differs (e.g. "Link Shelf /" vs "Implement linkshelf/").
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
	beadPath = strings.TrimLeft(filepath.ToSlash(strings.TrimSpace(beadPath)), "/")
	layoutRoot = strings.Trim(strings.TrimSpace(layoutRoot), "/")
	if beadPath == "" {
		return beadPath
	}
	if layoutRoot == "" || layoutRoot == "." {
		// With no layout root, strip rig-name prefixes that are not known layout directories.
		// This handles paths like "finally/Dockerfile", "FinAlly/backend/main.py", etc.
		// Walk from the left, stripping segments that are not known layout prefixes.
		for {
			idx := strings.Index(beadPath, "/")
			if idx <= 0 {
				break
			}
			firstSeg := beadPath[:idx]
			if knownLayoutPrefix(firstSeg + "/") {
				break
			}
			beadPath = beadPath[idx+1:]
		}
		return beadPath
	}
	if strings.HasPrefix(beadPath, layoutRoot+"/") || beadPath == layoutRoot {
		return beadPath
	}
	// Strip hallucinated rig-name prefix before the layout root
	// (e.g. bead title "Implement finally/backend/tests/test_market.py" when layoutRoot is "backend").
	// The rig name is not a directory component in the project.
	if idx := strings.Index(beadPath, layoutRoot+"/"); idx > 0 {
		beadPath = beadPath[idx:]
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
		strings.HasPrefix(beadPath, "web/"),
		beadPath == "main.go",
		beadPath == "main_test.go":
		return layoutRoot + "/" + beadPath
	}
	if !strings.Contains(beadPath, "/") {
		return layoutRoot + "/" + beadPath
	}
	return beadPath
}

// knownLayoutPrefix reports whether path starts with a Go module-relative prefix
// that should never be stripped as a hallucinated rig-name segment.
func knownLayoutPrefix(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	switch {
	case strings.HasPrefix(path, "internal/"),
		strings.HasPrefix(path, "cmd/"),
		strings.HasPrefix(path, "pkg/"),
		strings.HasPrefix(path, "api/"),
		strings.HasPrefix(path, "web/"),
		strings.HasPrefix(path, "backend/"),
		strings.HasPrefix(path, "frontend/"),
		strings.HasPrefix(path, "tests/"),
		strings.HasPrefix(path, "migrations/"),
		strings.HasPrefix(path, "scripts/"),
		strings.HasPrefix(path, "docker/"):
		return true
	}
	return false
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
	var nonRequired []string
	for _, b := range beads {
		if b.ID == "" {
			continue
		}
		if RequiresExactImplementPaths(v) && looksLikeOpenImplementBeadTitle(b.Title, v) && !MatchesImplementBeadTitle(b.Title, v) {
			nonRequired = append(nonRequired, fmt.Sprintf("%s (%q)", b.ID, b.Title))
			continue
		}
		if !MatchesImplementBeadTitle(b.Title, v) {
			continue
		}
		impl = append(impl, b)
	}
	if len(nonRequired) > 0 {
		return fmt.Errorf("open implement bead(s) with non-required path(s): %s", strings.Join(dedupeStrings(nonRequired), "; "))
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

	exact := RequiresExactImplementPaths(v)
	for _, want := range expected {
		lookupWant := NormalizePlannerBeadPath(want, v.LayoutRoot, rig)
		ids := beadIDsForPathProfile(pathToIDs, lookupWant, v)
		if len(ids) == 0 {
			missing = append(missing, want)
			continue
		}
		if len(ids) > 1 {
			dupes = append(dupes, fmt.Sprintf("%s (%s)", want, strings.Join(ids, ", ")))
		}
		if exact {
			for _, id := range ids {
				for _, b := range impl {
					if b.ID != id {
						continue
					}
					p := NormalizePlannerBeadPath(ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains), v.LayoutRoot, rig)
					if p != lookupWant {
						dupes = append(dupes, fmt.Sprintf("%s bead %s path %q (want exact %q)", want, id, p, lookupWant))
					}
				}
			}
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
					if exact {
						if want != p {
							continue
						}
					} else if want != p && filepath.Base(want) != filepath.Base(p) {
						continue
					}
					lookupWant := NormalizePlannerBeadPath(want, v.LayoutRoot, rig)
					if len(beadIDsForPathProfile(pathToIDs, lookupWant, v)) == 0 {
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
	augmented := requiredFilesWithCorrelatedTests(v.RequiredFiles, v)
	var extra []string
	for p, ids := range pathToIDs {
		matched := false
		for _, a := range augmented {
			if pathMatchesRequiredForProfile(p, []string{a}, v) {
				matched = true
				break
			}
			if n := NormalizePlannerBeadPath(a, v.LayoutRoot, rig); n != "" && pathMatchesRequiredForProfile(p, []string{n}, v) {
				matched = true
				break
			}
		}
		if !matched || !IsValidImplementBeadPath(p) {
			extra = append(extra, fmt.Sprintf("%s (%s)", p, strings.Join(ids, ", ")))
		}
	}
	if len(extra) > 0 {
		return fmt.Errorf("extra open bead(s) not in required_files (bd delete --force): %s", strings.Join(dedupeStrings(extra), "; "))
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
	if strings.Contains(p, " ") || p == "/" || p == "." || p == "./" {
		return false
	}
	if strings.HasPrefix(p, "/") {
		return false
	}

	if strings.HasSuffix(p, "/") || strings.HasSuffix(p, "\\") {
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
