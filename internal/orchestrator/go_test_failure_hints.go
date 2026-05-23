package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	goTestFailLineRE = regexp.MustCompile(`(?m)([a-zA-Z0-9_./-]+_test\.go):\d+`)
	goTestFuncRE     = regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]+)\(`)
)

// SourcePathForCorrelatedTest maps a *_test.go implement path to its production source bead path.
func SourcePathForCorrelatedTest(testPath, layoutRoot string) string {
	testPath = filepath.ToSlash(strings.TrimSpace(testPath))
	if testPath == "" || !IsTestImplementPath(testPath) || !strings.HasSuffix(testPath, "_test.go") {
		return ""
	}
	layout := strings.Trim(strings.TrimSpace(layoutRoot), "/")
	rel := testPath
	if layout != "" && strings.HasPrefix(rel, layout+"/") {
		rel = strings.TrimPrefix(rel, layout+"/")
	}
	srcRel := strings.TrimSuffix(rel, "_test.go") + ".go"
	if layout != "" {
		return layout + "/" + srcRel
	}
	return srcRel
}

// GoTestFailureProductionPaths returns production .go paths implicated by go test output
// (from *_test.go:line references in failure messages).
func GoTestFailureProductionPaths(cmdOutput, layoutRoot string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if p == "" || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	layout := strings.Trim(strings.TrimSpace(layoutRoot), "/")
	for _, m := range goTestFailLineRE.FindAllStringSubmatch(cmdOutput, -1) {
		if len(m) < 2 {
			continue
		}
		testFile := resolveGoTestFilePath(m[1], layout, cmdOutput)
		add(SourcePathForCorrelatedTest(testFile, layoutRoot))
	}
	return out
}

func goTestOutputSuggestsFailure(cmdOutput string) bool {
	return strings.Contains(cmdOutput, "--- FAIL:") ||
		strings.Contains(cmdOutput, "nil slice, want empty slice") ||
		(strings.Contains(cmdOutput, "FAIL\n") && strings.Contains(cmdOutput, "_test.go"))
}

// resolveGoTestFilePath turns a test file reference from go test output into a layout-relative path.
func resolveGoTestFilePath(testFile, layout, cmdOutput string) string {
	testFile = filepath.ToSlash(strings.TrimSpace(testFile))
	if testFile == "" {
		return ""
	}
	layout = strings.Trim(layout, "/")
	if layout != "" && strings.HasPrefix(testFile, layout+"/") {
		return testFile
	}
	if strings.Contains(testFile, "/") {
		if layout != "" {
			return layout + "/" + strings.TrimPrefix(testFile, "/")
		}
		return testFile
	}
	if layout == "" {
		return testFile
	}
	full := layout + `/[a-zA-Z0-9_./-]+/` + regexp.QuoteMeta(testFile)
	if m := regexp.MustCompile(full).FindString(cmdOutput); m != "" {
		return filepath.ToSlash(m)
	}
	// e.g. FAIL	linkshelf/internal/store	0.003s
	pkgRE := regexp.MustCompile(`(?m)FAIL\s+` + regexp.QuoteMeta(layout) + `/([a-zA-Z0-9_./-]+)\s`)
	if m := pkgRE.FindStringSubmatch(cmdOutput); len(m) >= 2 {
		return filepath.ToSlash(layout + "/" + m[1] + "/" + testFile)
	}
	return ""
}

func verifyOutputSuggestsCrossFile(cmdOutput string) bool {
	return compileOutputSuggestsCrossPackage(cmdOutput) || goTestOutputSuggestsFailure(cmdOutput)
}

// TestFuncNamesFromGoTestFile returns Test* function names declared in a Go test file.
func TestFuncNamesFromGoTestFile(data []byte) []string {
	var names []string
	for _, m := range goTestFuncRE.FindAllStringSubmatch(string(data), -1) {
		if len(m) >= 2 && m[1] != "" {
			names = append(names, m[1])
		}
	}
	return names
}

func packageNeedsScopedGoTest(beadPath string, v WorkflowValidation, mayorRigDir string) bool {
	return PackageHasForeignTestFiles(beadPath, v, mayorRigDir) ||
		PackageHasForeignProductionGoFiles(beadPath, v, mayorRigDir)
}

// PackageHasForeignProductionGoFiles reports whether the package contains other production
// .go files on disk that belong to a different required_files implement bead.
func PackageHasForeignProductionGoFiles(beadPath string, v WorkflowValidation, mayorRigDir string) bool {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	if beadPath == "" || IsTestImplementPath(beadPath) {
		return false
	}
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	pkgRel := GoBuildRelPackage(v.LayoutRoot, beadPath)
	if pkgRel == "" {
		return false
	}
	dir := filepath.Join(mayorRigDir, layout, filepath.FromSlash(pkgRel))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		prodRel := filepath.ToSlash(filepath.Join(layout, pkgRel, e.Name()))
		if prodRel == beadPath {
			continue
		}
		for _, want := range v.RequiredFiles {
			if pathMatchesRequired(prodRel, []string{want}) {
				return true
			}
		}
	}
	return false
}

// PackageHasForeignTestFiles reports whether the package for beadPath contains *_test.go
// files on disk that belong to a different required_files production bead.
func PackageHasForeignTestFiles(beadPath string, v WorkflowValidation, mayorRigDir string) bool {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	if beadPath == "" || IsTestImplementPath(beadPath) {
		return false
	}
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	pkgRel := GoBuildRelPackage(v.LayoutRoot, beadPath)
	if pkgRel == "" {
		return false
	}
	dir := filepath.Join(mayorRigDir, layout, filepath.FromSlash(pkgRel))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	myTest := CorrelatedTestPathForSource(beadPath, v.LayoutRoot)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		testRel := filepath.ToSlash(filepath.Join(layout, pkgRel, e.Name()))
		if testRel == myTest {
			continue
		}
		src := SourcePathForCorrelatedTest(testRel, v.LayoutRoot)
		if src == "" || src == beadPath {
			continue
		}
		for _, want := range v.RequiredFiles {
			if pathMatchesRequired(src, []string{want}) {
				return true
			}
		}
	}
	return false
}

// FormatGoTestFailureHints adds guidance for go test failures (closed production beads, Go idioms).
func FormatGoTestFailureHints(townRoot, rig, activeBeadPath, cmdOutput string, errorPaths []string, v WorkflowValidation) string {
	if !goTestOutputSuggestsFailure(cmdOutput) {
		return ""
	}
	prod := GoTestFailureProductionPaths(cmdOutput, v.LayoutRoot)
	merged := append(append([]string{}, errorPaths...), prod...)
	if hint := FormatClosedDependencyCompileHints(townRoot, rig, activeBeadPath, merged, v); hint != "" {
		return hint
	}
	var b strings.Builder
	if strings.Contains(cmdOutput, "nil slice, want empty slice") {
		b.WriteString("### Go test: empty slice vs nil\n")
		b.WriteString("Tests expect a **non-nil empty slice** (`[]T{}` or `make([]T, 0)`), not `nil`. ")
		b.WriteString("In the production method under test (often `List`), initialize `links := make([]Link, 0)` or return `[]Link{}` when there are no rows — then re-run **Verify**.\n")
	}
	if len(prod) > 0 && activeBeadPath != "" {
		for _, p := range prod {
			if p == activeBeadPath {
				continue
			}
			id, ok := ClosedImplementBeadForPath(townRoot, rig, p, v)
			if ok {
				b.WriteString(fmt.Sprintf("\nFailure references **`%s`** (closed bead **%s**) while active bead is **`%s`**. Reopen **%s**, fix with **EDIT:**, Verify, `bd close %s`, then continue the active bead.\n",
					p, id, activeBeadPath, id, id))
			}
		}
	}
	return strings.TrimSpace(b.String())
}
