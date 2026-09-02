package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Catches `import python3 -m pytest` pasted into .py files — not legitimate `import pytest`.
var invalidPythonLineRE = regexp.MustCompile(`(?i)^\s*import\s+python3?\b`)

// CheckPythonSourceValid rejects common LLM mistakes (shell commands pasted as Python).
func CheckPythonSourceValid(data []byte, displayRel string) error {
	ext := strings.ToLower(filepath.Ext(displayRel))
	if ext != ".py" {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if invalidPythonLineRE.MatchString(line) {
			return fmt.Errorf("%s line %d looks like a shell command pasted into Python (%q)", displayRel, i+1, trimmed)
		}
		if strings.Contains(strings.ToLower(trimmed), "python3 -m") {
			return fmt.Errorf("%s line %d contains shell invocation %q", displayRel, i+1, trimmed)
		}
	}
	return nil
}

// PythonFileCorrupted reports whether a .py file has syntax errors and should be replaced wholesale.
func PythonFileCorrupted(townRoot, rig, relPath, layoutRoot string) bool {
	path := filepath.Join(townRoot, rig, "mayor", "rig", filepath.FromSlash(relPath))
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return CheckPythonSourceValid(data, relPath) != nil
}

// ValidateLayoutPythonSources scans .py files under layout_root in the rig worktree.
func ValidateLayoutPythonSources(rigDir string, v WorkflowValidation) error {
	layout := strings.TrimSpace(v.LayoutRoot)
	if layout == "" {
		return nil
	}
	root := filepath.Join(rigDir, filepath.FromSlash(layout))
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "__pycache__" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.ToLower(filepath.Ext(path)) != ".py" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(rigDir, path)
		if err != nil {
			rel = path
		}
		return CheckPythonSourceValid(data, filepath.ToSlash(rel))
	})
}
