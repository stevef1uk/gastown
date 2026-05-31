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
func CorrelatedTestPathForSource(beadPath string, v WorkflowValidation) string {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	if beadPath == "" || IsTestImplementPath(beadPath) {
		return ""
	}
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
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
			filepath.ToSlash(filepath.Join(dir, testName)),
			filepath.ToSlash(filepath.Join(dir, "tests", testName)),
			filepath.ToSlash(filepath.Join("tests", testName)),
		}
		var formatted []string
		for _, c := range candidates {
			if layout != "" && !strings.HasPrefix(c, layout+"/") {
				c = layout + "/" + c
			}
			formatted = append(formatted, c)
		}
		if len(v.RequiredFiles) > 0 {
			for _, c := range formatted {
				for _, req := range v.RequiredFiles {
					if pathMatchesRequired(c, []string{req}) {
						return c
					}
				}
			}
		}
		return formatted[0]
	}
	return ""
}

// TestPathListedInRequired reports whether path or its correlated test is in required_files.
func TestPathListedInRequired(sourcePath string, v WorkflowValidation) bool {
	corr := CorrelatedTestPathForSource(sourcePath, v)
	if corr == "" {
		return false
	}
	for _, want := range v.RequiredFiles {
		if pathMatchesRequired(corr, []string{want}) {
			return true
		}
	}
	return false
}

// AllowedCorrelatedPackageImplementWrite allows editing the paired production or *_test.go
// file in the same Go package while a different implement bead is active (e.g. fix handlers.go
// while working the handlers_test.go bead so go test ./internal/api/... compiles).
func AllowedCorrelatedPackageImplementWrite(activePath, writtenPath string, v WorkflowValidation) bool {
	if !WorkflowUsesGo(v) {
		return false
	}
	activePath = filepath.ToSlash(strings.TrimSpace(activePath))
	writtenPath = filepath.ToSlash(strings.TrimSpace(writtenPath))
	if activePath == "" || writtenPath == "" || activePath == writtenPath {
		return false
	}
	if !sameGoImplementPackage(activePath, writtenPath, v.LayoutRoot) {
		return false
	}
	if IsTestImplementPath(activePath) && !IsTestImplementPath(writtenPath) {
		if src := SourcePathForCorrelatedTest(activePath, v.LayoutRoot); src != "" &&
			PathMatchesImplementWrite(writtenPath, src, v.RequiredFiles) {
			return true
		}
		return productionGoPathInRequiredFiles(writtenPath, v)
	}
	if !IsTestImplementPath(activePath) && IsTestImplementPath(writtenPath) {
		if test := CorrelatedTestPathForSource(activePath, v); test != "" &&
			PathMatchesImplementWrite(writtenPath, test, v.RequiredFiles) {
			return true
		}
	}
	return false
}

func productionGoPathInRequiredFiles(path string, v WorkflowValidation) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" || IsTestImplementPath(path) || !strings.HasSuffix(path, ".go") {
		return false
	}
	for _, want := range v.RequiredFiles {
		if pathMatchesRequired(path, []string{want}) {
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
