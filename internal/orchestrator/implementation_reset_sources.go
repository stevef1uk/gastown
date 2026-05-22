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
