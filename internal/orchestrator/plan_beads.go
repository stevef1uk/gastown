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

// ExtractPathFromBeadTitle returns a repo-relative file path from an implementation bead title.
func ExtractPathFromBeadTitle(title, titlePrefix string) string {
	title = strings.TrimSpace(title)
	prefix := strings.TrimSpace(titlePrefix)
	if prefix != "" {
		if idx := strings.Index(strings.ToLower(title), strings.ToLower(prefix)); idx >= 0 {
			title = strings.TrimSpace(title[idx+len(prefix):])
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
func ValidatePlanBeads(beads []PlanBead, archPath string, v WorkflowValidation) error {
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
		if !strings.Contains(strings.ToLower(b.Title), strings.ToLower(strings.TrimSpace(v.BeadTitleContains))) {
			continue
		}
		impl = append(impl, b)
	}
	if len(impl) == 0 {
		return fmt.Errorf("no open beads matching %q", v.BeadTitleContains)
	}

	pathToIDs := map[string][]string{}
	for _, b := range impl {
		p := ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains)
		if p == "" {
			return fmt.Errorf("bead %s title has no file path: %q", b.ID, b.Title)
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

var archPathRe = regexp.MustCompile("`([^`]+\\.(?:js|py|html|css|tsx?|jsx?))`")

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
	for _, m := range archPathRe.FindAllStringSubmatch(archText, -1) {
		p := filepath.ToSlash(strings.TrimSpace(m[1]))
		if p == "" || seen[p] || !isLikelyRepoFilePath(p, layoutRoot) {
			continue
		}
		seen[p] = true
		out = append(out, p)
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
	return strings.Contains(p, "/")
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
