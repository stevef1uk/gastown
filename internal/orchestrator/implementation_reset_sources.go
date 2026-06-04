package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
)

// layoutSetupBasenames are never deleted during implementation hard reset.
var layoutSetupBasenames = map[string]bool{
	"go.mod": true, "go.sum": true,
	"requirements.txt": true, "pyproject.toml": true,
}

// RemoveLayoutSourceCodeFiles deletes every .go and .py file under layout_root, keeping
// dependency/setup manifests (go.mod, go.sum, requirements.txt, pyproject.toml).
func RemoveLayoutSourceCodeFiles(rigDir string, v WorkflowValidation) ([]string, error) {
	v = v.ForActivePhase()
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	if layout == "" {
		return nil, nil
	}
	root := filepath.Join(rigDir, layout)
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	rigDir = filepath.Clean(rigDir)
	var removed []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		base := strings.ToLower(info.Name())
		if layoutSetupBasenames[base] {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".go" && ext != ".py" {
			return nil
		}
		rel, err := filepath.Rel(rigDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if IsProjectSetupArtifactPath(rel, v) {
			return nil
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		removed = append(removed, rel)
		return nil
	})
	return removed, err
}

func closedImplementPathSet(townRoot, rig string, v WorkflowValidation) (map[string]bool, error) {
	v = v.ForActivePhase()
	closed, err := listImplementBeadsForGuard(townRoot, rig, v, "closed")
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, b := range closed {
		if !MatchesImplementBeadTitle(b.Title, v) {
			continue
		}
		p := NormalizePlannerBeadPath(ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains), v.LayoutRoot, rig)
		if p != "" {
			out[p] = true
		}
	}
	return out, nil
}

// implementArtifactPathsForActiveBeads returns rig-relative paths for open and in_progress implement beads only.
// Paths already covered by a closed implement bead are omitted so hard reset does not delete finished work.
func implementArtifactPathsForActiveBeads(townRoot, rig string, v WorkflowValidation) ([]string, error) {
	v = v.ForActivePhase()
	closedPaths, err := closedImplementPathSet(townRoot, rig, v)
	if err != nil {
		return nil, err
	}
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		p = NormalizeBeadPathForLayout(filepath.ToSlash(strings.TrimSpace(p)), v.LayoutRoot)
		if p == "" || IsProjectSetupArtifactPath(p, v) {
			return
		}
		if closedPaths[p] {
			return
		}
		if layoutSetupBasenames[strings.ToLower(filepath.Base(p))] {
			return
		}
		if seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, status := range []string{"open", "in_progress"} {
		beads, err := listImplementBeadsForGuard(townRoot, rig, v, status)
		if err != nil {
			return nil, err
		}
		for _, b := range beads {
			if !MatchesImplementBeadTitle(b.Title, v) {
				continue
			}
			add(ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains))
		}
	}
	return out, nil
}

// RemoveImplementBeadArtifactFiles deletes on-disk files for the given rig-relative paths (any extension).
func RemoveImplementBeadArtifactFiles(rigDir string, relPaths []string) ([]string, error) {
	rigDir = filepath.Clean(rigDir)
	var removed []string
	for _, rel := range relPaths {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" {
			continue
		}
		base := strings.ToLower(filepath.Base(rel))
		if layoutSetupBasenames[base] {
			continue
		}
		abs := filepath.Join(rigDir, filepath.FromSlash(rel))
		if _, err := os.Stat(abs); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return removed, err
		}
		if err := os.Remove(abs); err != nil {
			return removed, err
		}
		removed = append(removed, rel)
	}
	return removed, nil
}
