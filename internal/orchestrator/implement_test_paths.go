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
	if strings.Contains(lower, "/tests/") && strings.HasSuffix(lower, ".py") {
		return true
	}
	// TypeScript/JavaScript test files
	if strings.HasSuffix(lower, ".test.ts") || strings.HasSuffix(lower, ".test.tsx") ||
		strings.HasSuffix(lower, ".spec.ts") || strings.HasSuffix(lower, ".spec.tsx") {
		return true
	}
	return false
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
		testPath := ""
		if dir == "." {
			testPath = base + "_test.go"
		} else {
			testPath = dir + "/" + base + "_test.go"
		}
		if layout != "" {
			testPath = layout + "/" + testPath
		}
		return testPath
	}
	if strings.HasSuffix(rel, ".py") && !strings.HasPrefix(filepath.Base(rel), "test_") {
		base := strings.TrimSuffix(filepath.Base(rel), ".py")
		testNames := []string{"test_" + base + ".py", base + "_test.py"}

		// Use the project-specific test path from required_files when available.
		// Matching by basename avoids hardcoding tests/ vs app/ layout conventions.
		for _, req := range v.RequiredFiles {
			req = filepath.ToSlash(strings.TrimSpace(req))
			reqBase := filepath.Base(req)
			for _, tn := range testNames {
				if reqBase == tn {
					return req
				}
			}
		}

		// Fallback to conventional locations.
		dir := filepath.ToSlash(filepath.Dir(rel))
		candidates := []string{
			filepath.ToSlash(filepath.Join(dir, testNames[0])),
		}
		if dir != "." {
			candidates = append(candidates, filepath.ToSlash(filepath.Join(dir, "tests", testNames[0])))
		}
		candidates = append(candidates, filepath.ToSlash(filepath.Join("tests", testNames[0])))
		var formatted []string
		for _, c := range candidates {
			if layout != "" && !strings.HasPrefix(c, layout+"/") {
				c = layout + "/" + c
			}
			formatted = append(formatted, c)
		}
		for _, c := range formatted {
			for _, req := range v.RequiredFiles {
				req = filepath.ToSlash(strings.TrimSpace(req))
				if req == c {
					return c
				}
			}
		}
		// Return first conventional candidate when no required_file match,
		// consistent with the unconditional return for Go files above.
		if len(formatted) > 0 {
			return formatted[0]
		}
		return ""
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

// TestPathCoveredByOtherOpenBead reports whether the test file path belongs to a different
// open/in_progress implement bead (not the bead being closed). When true, the close guard
// defers the test check — the owning bead will verify and write the test file.
func TestPathCoveredByOtherOpenBead(townRoot, rig, closingBeadID, testPath string, v WorkflowValidation) bool {
	if townRoot == "" || rig == "" || closingBeadID == "" {
		return false
	}
	if !BeadsDatabaseReady(townRoot, rig) {
		return false
	}
	open, err := ListImplementBeadsOpenOrInProgress(townRoot, rig, v)
	if err != nil || len(open) == 0 {
		return false
	}
	for _, b := range open {
		if b.ID == closingBeadID {
			continue
		}
		beadPath := filepath.ToSlash(NormalizeBeadPathForLayout(
			ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains), v.LayoutRoot))
		if pathMatchesRequired(beadPath, []string{testPath}) {
			return true
		}
	}
	return false
}

// OpenBeadIDForPath returns the open/in_progress bead ID that owns the given path, or "".
func OpenBeadIDForPath(townRoot, rig, path string, v WorkflowValidation) string {
	if townRoot == "" || rig == "" || !BeadsDatabaseReady(townRoot, rig) {
		return ""
	}
	open, err := ListImplementBeadsOpenOrInProgress(townRoot, rig, v)
	if err != nil {
		return ""
	}
	for _, b := range open {
		beadPath := filepath.ToSlash(NormalizeBeadPathForLayout(
			ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains), v.LayoutRoot))
		if pathMatchesRequired(beadPath, []string{path}) {
			return b.ID
		}
	}
	return ""
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
			PathMatchesImplementWrite(writtenPath, src, v.RequiredFiles, v) {
			return true
		}
		return productionGoPathInRequiredFiles(writtenPath, v)
	}
	if !IsTestImplementPath(activePath) && IsTestImplementPath(writtenPath) {
		if test := CorrelatedTestPathForSource(activePath, v); test != "" &&
			PathMatchesImplementWrite(writtenPath, test, v.RequiredFiles, v) {
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
