package rig

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var specLayoutDirLine = regexp.MustCompile(`(?m)^([a-zA-Z][a-zA-Z0-9_-]*)/\s*$`)

var layoutSetupKeep = map[string]bool{
	"go.mod": true, "go.sum": true,
	"requirements.txt": true, "pyproject.toml": true,
}

var layoutBuildArtifactNames = map[string]bool{
	"server":         true,
	"codeindex.json": true,
	"block:":         true,
}

// InferLayoutRootFromMayorRig reads SPEC.md tree header or finds a child directory with go.mod.
func InferLayoutRootFromMayorRig(mayorRigDir string) string {
	specPath := filepath.Join(mayorRigDir, "SPEC.md")
	if data, err := os.ReadFile(specPath); err == nil {
		if m := specLayoutDirLine.FindStringSubmatch(string(data)); len(m) == 2 {
			return m[1]
		}
	}
	entries, err := os.ReadDir(mayorRigDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if _, err := os.Stat(filepath.Join(mayorRigDir, e.Name(), "go.mod")); err == nil {
			return e.Name()
		}
	}
	return ""
}

// ResetLayoutPreImplementation removes stale implementation from a cloned or prior run under layout_root.
// Keeps dependency manifests (go.mod, go.sum, requirements.txt, pyproject.toml) only.
func ResetLayoutPreImplementation(mayorRigDir, layoutRoot string) ([]string, error) {
	layoutRoot = strings.Trim(filepath.ToSlash(strings.TrimSpace(layoutRoot)), "/")
	if layoutRoot == "" {
		return nil, nil
	}
	root := filepath.Join(mayorRigDir, layoutRoot)
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	mayorRigDir = filepath.Clean(mayorRigDir)
	var removed []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		base := info.Name()
		lowerBase := strings.ToLower(base)
		if layoutSetupKeep[lowerBase] {
			return nil
		}
		if layoutBuildArtifactNames[base] || layoutBuildArtifactNames[lowerBase] {
			return removeLayoutRel(mayorRigDir, path, &removed)
		}
		if strings.HasSuffix(lowerBase, ".db") {
			return removeLayoutRel(mayorRigDir, path, &removed)
		}
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".go", ".py", ".html", ".js", ".css", ".ts", ".tsx":
			return removeLayoutRel(mayorRigDir, path, &removed)
		}
		if info.Mode()&0o111 != 0 && ext == "" {
			return removeLayoutRel(mayorRigDir, path, &removed)
		}
		return nil
	})
	return removed, err
}

func removeLayoutRel(mayorRigDir, path string, removed *[]string) error {
	rel, err := filepath.Rel(mayorRigDir, path)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	*removed = append(*removed, filepath.ToSlash(rel))
	return nil
}

// ResetMayorRigCloneState removes rig-root agent junk and stale layout implementation from git clone.
func ResetMayorRigCloneState(mayorRigDir string) ([]string, error) {
	var all []string
	junk, err := RemoveMayorRigAgentJunk(mayorRigDir)
	if err != nil {
		return all, err
	}
	all = append(all, junk...)
	layout := InferLayoutRootFromMayorRig(mayorRigDir)
	if layout == "" {
		return all, nil
	}
	stale, err := ResetLayoutPreImplementation(mayorRigDir, layout)
	if err != nil {
		return all, err
	}
	all = append(all, stale...)
	return all, nil
}

// ResetMayorRigCloneStateLog returns a hook-friendly log line.
func ResetMayorRigCloneStateLog(mayorRigDir string) (string, error) {
	removed, err := ResetMayorRigCloneState(mayorRigDir)
	if err != nil {
		return "", err
	}
	if len(removed) == 0 {
		return "", nil
	}
	if len(removed) > 8 {
		return fmt.Sprintf("reset clone state: removed %d stale files under mayor/rig", len(removed)), nil
	}
	return fmt.Sprintf("reset clone state: removed %s", strings.Join(removed, ", ")), nil
}
