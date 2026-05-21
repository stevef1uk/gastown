package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
)

// IsTestImplementPath reports whether the implement bead path is a unit-test file.
func IsTestImplementPath(beadPath string) bool {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	if beadPath == "" {
		return false
	}
	base := filepath.Base(beadPath)
	lower := strings.ToLower(beadPath)
	if strings.HasSuffix(lower, "_test.go") {
		return true
	}
	if strings.HasPrefix(base, "test_") && strings.HasSuffix(lower, ".py") {
		return true
	}
	return strings.Contains(lower, "/tests/") && strings.HasSuffix(lower, ".py")
}

// CorrelatedTestPathForSource returns the conventional unit-test path for a source bead, or "".
func CorrelatedTestPathForSource(beadPath, layoutRoot string) string {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	if beadPath == "" || IsTestImplementPath(beadPath) {
		return ""
	}
	layout := strings.Trim(strings.TrimSpace(layoutRoot), "/")
	rel := beadPath
	if layout != "" && strings.HasPrefix(rel, layout+"/") {
		rel = strings.TrimPrefix(rel, layout+"/")
	}
	if strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, "_test.go") {
		dir := filepath.ToSlash(filepath.Dir(rel))
		base := strings.TrimSuffix(filepath.Base(rel), ".go")
		if dir == "." {
			return layout + "/" + base + "_test.go"
		}
		if layout != "" {
			return layout + "/" + dir + "/" + base + "_test.go"
		}
		return dir + "/" + base + "_test.go"
	}
	if strings.HasSuffix(rel, ".py") && !strings.HasPrefix(filepath.Base(rel), "test_") {
		base := strings.TrimSuffix(filepath.Base(rel), ".py")
		testName := "test_" + base + ".py"
		dir := filepath.ToSlash(filepath.Dir(rel))
		candidates := []string{
			filepath.ToSlash(filepath.Join("tests", testName)),
			filepath.ToSlash(filepath.Join(dir, "tests", testName)),
		}
		for _, c := range candidates {
			if layout != "" && !strings.HasPrefix(c, layout+"/") {
				c = layout + "/" + c
			}
			return c
		}
	}
	return ""
}

// TestPathListedInRequired reports whether path or its correlated test is in required_files.
func TestPathListedInRequired(sourcePath string, required []string, layoutRoot string) bool {
	corr := CorrelatedTestPathForSource(sourcePath, layoutRoot)
	if corr == "" {
		return false
	}
	for _, want := range required {
		if pathMatchesRequired(corr, []string{want}) {
			return true
		}
	}
	return false
}

func mayorRigTestFileExists(townRoot, rig, relPath string) bool {
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	if relPath == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(townRoot, rig, "mayor", "rig", relPath))
	return err == nil
}
