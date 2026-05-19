package agentenv

import (
	"os"
	"path/filepath"
	"strings"
)

// RepairBrokenPythonInit fixes layout/__init__.py when it imports non-existent symbols (e.g. TaskStore).
func RepairBrokenPythonInit(workDir, layoutRoot string) (repaired bool, err error) {
	layout := strings.Trim(strings.TrimSpace(layoutRoot), "/")
	if layout == "" {
		return false, nil
	}
	path := filepath.Join(workDir, layout, "__init__.py")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	lower := strings.ToLower(string(data))
	if !strings.Contains(lower, "taskstore") {
		return false, nil
	}
	content := "# " + layout + " package\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return false, err
	}
	return true, nil
}
