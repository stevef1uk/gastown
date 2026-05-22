package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
)

// SanitizeNativeEditRelPath strips markdown/backtick junk from READ/EDIT/WRITE paths.
func SanitizeNativeEditRelPath(path string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	for {
		trimmed := strings.TrimSpace(path)
		changed := false
		if len(trimmed) >= 2 && strings.HasPrefix(trimmed, "`") && strings.HasSuffix(trimmed, "`") {
			path = strings.TrimSpace(trimmed[1 : len(trimmed)-1])
			changed = true
		} else if strings.HasPrefix(trimmed, "`") {
			path = strings.TrimSpace(trimmed[1:])
			changed = true
		} else if strings.HasSuffix(trimmed, "`") {
			path = strings.TrimSpace(trimmed[:len(trimmed)-1])
			changed = true
		}
		if strings.HasPrefix(strings.TrimSpace(path), "**") {
			path = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(path), "**"))
			changed = true
		}
		if strings.HasSuffix(strings.TrimSpace(path), "**") {
			path = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(path), "**"))
			changed = true
		}
		if !changed {
			break
		}
	}
	return strings.TrimSpace(path)
}

// RemoveMalformedLayoutArtifactFiles deletes junk files under layout_root (prose mistaken as paths).
func RemoveMalformedLayoutArtifactFiles(rigDir string, v WorkflowValidation) ([]string, error) {
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
		if layoutSetupBasenames[strings.ToLower(info.Name())] {
			return nil
		}
		rel, err := filepath.Rel(rigDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !IsMalformedLayoutArtifact(rel) {
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

// IsMalformedLayoutArtifact reports paths written by mistake (prose/backticks, not real files).
func IsMalformedLayoutArtifact(rel string) bool {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if rel == "" {
		return false
	}
	base := filepath.Base(rel)
	if strings.ContainsAny(base, "`") || strings.HasPrefix(base, "**") {
		return true
	}
	lower := strings.ToLower(base)
	if strings.Contains(lower, "command to create") || strings.Contains(lower, "per architecture") {
		return true
	}
	return !IsValidImplementBeadPath(rel)
}
