package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
)

// StripMayorRigPathPrefix removes erroneous rig/mayor/rig/ prefixes from model paths.
func StripMayorRigPathPrefix(relPath string) string {
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	if i := strings.Index(relPath, "mayor/rig/"); i >= 0 {
		return relPath[i+len("mayor/rig/"):]
	}
	return relPath
}

// SanitizeNativeEditRelPath strips markdown/backtick junk from READ/EDIT/WRITE paths.
func SanitizeNativeEditRelPath(path string) string {
	path = StripMayorRigPathPrefix(filepath.ToSlash(strings.TrimSpace(path)))
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
		
		// Strip JSON noise from hallucinated {"cmd": "READ: path"} lines.
		for _, suffix := range []string{`"}`, `"`, `'`, `}`, `,`} {
			if strings.HasSuffix(strings.TrimSpace(path), suffix) {
				path = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(path), suffix))
				changed = true
			}
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
	if err != nil {
		return removed, err
	}
	// Remove spurious nested rig-path directories under the layout root.
	// E.g. linkshelf/testgt3/mayor/rig/linkshelf/ created by commands with
	// full path prefixes.
	if dirs, dirErr := removeNestedRigLayoutDirs(root, rigDir, v); dirErr != nil {
		return removed, dirErr
	} else {
		removed = append(removed, dirs...)
	}
	return removed, nil
}

// removeNestedRigLayoutDirs removes empty subdirectories under layoutRoot that
// form a spurious nested rig path (e.g. linkshelf/testgt3/mayor/rig/linkshelf/...).
func removeNestedRigLayoutDirs(root, rigDir string, v WorkflowValidation) ([]string, error) {
	rigBase := filepath.Base(rigDir) // "rig"
	if rigBase == "" || rigBase == "." {
		return nil, nil
	}
	var removed []string
	// Walk in reverse (bottom-up) to handle empty child directories first.
	var dirs []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() || path == root {
			return nil
		}
		// Collect only directories that look like rig-path components.
		name := info.Name()
		if name == rigBase || name == "mayor" {
			dirs = append(dirs, path)
		}
		return nil
	})
	// Remove deepest first.
	for i := len(dirs) - 1; i >= 0; i-- {
		if isEmptyDir(dirs[i]) {
			if err := os.Remove(dirs[i]); err == nil {
				rel, _ := filepath.Rel(root, dirs[i])
				if rel != "" && rel != "." {
					removed = append(removed, filepath.ToSlash(rel)+"/")
				}
			}
		}
	}
	return removed, nil
}

func isEmptyDir(path string) bool {
	entries, err := os.ReadDir(path)
	return err == nil && len(entries) == 0
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
