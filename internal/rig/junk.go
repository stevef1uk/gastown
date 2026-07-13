package rig

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// mayorRigJunkFiles are agent placeholder artifacts that must never live at mayor/rig root.
var mayorRigJunkFiles = map[string]bool{
	"package.json":           true,
	"package-lock.json":      true,
	"test-execution-command": true,
	"tests_skipped.txt":      true,
	"spec_file_path.txt":     true,
	"run-tests":              true,
	"run-tests.sh":           true,
	"run-tests.sh_backup":    true,
	"fizzbuzz.py":            true,
	"main.py":                true,
	"test_fizzbuzz.py":       true,
	"dummy.py":               true,
	"plan_complete.js":       true,
}

// mayorRigJunkDirs are directories agents sometimes create at mayor/rig root by mistake.
// Only Gastown-internal directories belong here. Project directories like backend, frontend,
// tests, node_modules, env, etc. are legitimate project content.
var mayorRigJunkDirs = map[string]bool{
	"polecat": true,
}

// mayorRigRootAllowedFiles may exist at mayor/rig root (workflow + spec artifacts).
var mayorRigRootAllowedFiles = map[string]bool{
	"SPEC.md":         true,
	"architecture.md": true,
	"plan.md":         true,
	"codeindex.json":  true,
	".gitignore":      true,
	"CLAUDE.md":       true,
	"AGENTS.md":       true,
	"Dockerfile":      true,
	"Makefile":        true,
	"README.md":       true,
	"README":          true,
	"LICENSE":         true,
	"Containerfile":   true,
}

// IsKnownMayorRigJunkRel reports junk paths relative to mayor/rig (not under layout_root).
func IsKnownMayorRigJunkRel(rel string) bool {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if rel == "" || strings.Contains(rel, "..") {
		return false
	}
	if mayorRigRootAllowedFiles[rel] {
		return false
	}
	if strings.HasPrefix(rel, ".gastown/") || strings.HasPrefix(rel, ".beads/") {
		return false
	}
	base := filepath.Base(rel)
	if mayorRigJunkFiles[base] || mayorRigJunkFiles[rel] {
		return true
	}
	parts := strings.Split(rel, "/")
	if len(parts) == 1 {
		return mayorRigJunkDirs[parts[0]]
	}
	return mayorRigJunkDirs[parts[0]]
}

// RemoveMayorRigAgentJunk deletes known agent junk under mayor/rig root (not layout_root).
func RemoveMayorRigAgentJunk(mayorRigDir string) ([]string, error) {
	mayorRigDir = filepath.Clean(mayorRigDir)
	var removed []string
	for name := range mayorRigJunkFiles {
		path := filepath.Join(mayorRigDir, name)
		if err := os.Remove(path); err != nil {
			if !os.IsNotExist(err) {
				return removed, err
			}
			continue
		}
		removed = append(removed, name)
	}
	for name := range mayorRigJunkDirs {
		path := filepath.Join(mayorRigDir, name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return removed, err
		}
		removed = append(removed, name+"/")
	}
	return removed, nil
}

// RemoveMayorRigAgentJunkLog returns a hook log line when junk was removed.
func RemoveMayorRigAgentJunkLog(mayorRigDir string) (string, error) {
	removed, err := RemoveMayorRigAgentJunk(mayorRigDir)
	if err != nil {
		return "", err
	}
	if len(removed) == 0 {
		return "", nil
	}
	return fmt.Sprintf("removed rig-root junk: %s", strings.Join(removed, ", ")), nil
}

// RejectMayorRigRootShellCommand blocks npm/jest/python-venv scaffolding at mayor/rig root.
func RejectMayorRigRootShellCommand(cmd, layoutRoot string) error {
	lower := strings.ToLower(strings.TrimSpace(cmd))
	if lower == "" {
		return nil
	}
	for _, blocked := range []string{
		"npm init", "npm install", "npm ci", "npx jest", "yarn init", "yarn add", "pnpm init", "pnpm add",
	} {
		if !strings.Contains(lower, blocked) {
			continue
		}
		// Allow npm install in subdirectories (e.g. cd frontend && npm install).
		// Only block when running at the rig root.
		if layoutRoot != "" && layoutRoot != "." {
			// Check if there's a "cd <subdir>" before the npm command — that's OK.
			cleaned := strings.ReplaceAll(lower, "&&", ";")
			parts := strings.Split(cleaned, ";")
			runsAtRoot := true
			for _, part := range parts {
				part = strings.TrimSpace(part)
				if strings.HasPrefix(part, "cd ") {
					target := strings.TrimPrefix(part, "cd ")
					target = strings.TrimSpace(target)
					// If cd target is the rig root or parent, it's still root-level.
					if target == "." || target == "" || strings.HasSuffix(target, "/mayor/rig") || target == strings.Trim(layoutRoot, "/") {
						// Root-level cd — stay blocked.
					} else {
						runsAtRoot = false
					}
				}
			}
			if !runsAtRoot {
				continue
			}
		}
		return fmt.Errorf("do not run %q at mayor/rig root — use go test under %s/", strings.TrimSpace(blocked), strings.Trim(layoutRoot, "/"))
	}
	if strings.Contains(lower, "python3 -m venv") || strings.Contains(lower, "python -m venv") {
		layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(layoutRoot)), "/")
		venvOK := layout != "" && (strings.Contains(lower, layout+"/") || strings.Contains(lower, "/"+layout+"/"))
		if !venvOK && !strings.Contains(lower, ".venv") {
			return fmt.Errorf("do not create Python venv at mayor/rig root — use go mod under %s/ for this rig", layout)
		}
	}
	if strings.Contains(lower, ">") || strings.Contains(lower, "touch ") || strings.Contains(lower, "touch\t") {
		for _, f := range strings.Fields(lower) {
			f = strings.TrimSpace(f)
			if f == "" {
				continue
			}
			if strings.HasPrefix(f, "./") {
				f = f[2:]
			}
			if strings.Contains(f, "/") {
				continue
			}
			if mayorRigJunkFiles[f] {
				return fmt.Errorf("do not create placeholder file %q at mayor/rig root", f)
			}
		}
	}
	if (strings.Contains(lower, "rm ") || strings.Contains(lower, "rm\t")) &&
		(strings.Contains(lower, ".venv/") || strings.Contains(lower, "venv/")) {
		return fmt.Errorf("do not delete .venv/ files — the Python virtual environment is managed by the pipeline")
	}
	if (strings.Contains(lower, "chmod ") || strings.Contains(lower, "ln -")) &&
		(strings.Contains(lower, ".venv/") || strings.Contains(lower, "venv/")) {
		return fmt.Errorf("do not modify .venv/ permissions or symlinks — the Python virtual environment is managed by the pipeline")
	}
	return nil
}

// RejectDisallowedMayorRigWrite rejects native WRITE/EDIT paths outside layout and workflow artifacts.
func RejectDisallowedMayorRigWrite(relPath, layoutRoot string, requiredFiles []string) error {
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	if relPath == "" {
		return fmt.Errorf("empty path")
	}
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(layoutRoot)), "/")
	if layout != "" && (relPath == layout || strings.HasPrefix(relPath, layout+"/")) {
		return nil
	}
	if mayorRigRootAllowedFiles[relPath] {
		return nil
	}
	if strings.HasPrefix(relPath, ".gastown/") {
		return nil
	}
	for _, rf := range requiredFiles {
		if relPath == filepath.ToSlash(strings.TrimSpace(rf)) {
			return nil
		}
	}
	if IsKnownMayorRigJunkRel(relPath) {
		return fmt.Errorf("path %q is rig-root junk — write under %s/ only", relPath, layout)
	}
	if !strings.Contains(relPath, "/") {
		return fmt.Errorf("do not write %q at mayor/rig root — use paths under %s/", relPath, layout)
	}
	parts := strings.Split(relPath, "/")
	if len(parts) > 0 && mayorRigJunkDirs[parts[0]] {
		return fmt.Errorf("do not write under %q at mayor/rig root", parts[0])
	}
	return nil
}
