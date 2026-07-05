package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ForeignTestFilesForActiveBead lists layout-relative *_test.go paths in the active bead's
// package that belong to a different required_files production bead (e.g. store_test.go while on schema.go).
func ForeignTestFilesForActiveBead(beadPath string, v WorkflowValidation, mayorRigDir string) []string {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	if beadPath == "" || IsTestImplementPath(beadPath) {
		return nil
	}
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	pkgRel := GoBuildRelPackage(v.LayoutRoot, beadPath)
	if pkgRel == "" {
		return nil
	}
	dir := filepath.Join(mayorRigDir, layout, filepath.FromSlash(pkgRel))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	myTest := CorrelatedTestPathForSource(beadPath, v)
	var out []string
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
				out = append(out, testRel)
				break
			}
		}
	}
	return out
}

// IsForeignBeadTestFileForActive reports whether testPath is a *_test.go file in the active
// bead's package that belongs to another implement bead's production file.
func IsForeignBeadTestFileForActive(activeBeadPath, testPath string, v WorkflowValidation, mayorRigDir string) bool {
	testPath = filepath.ToSlash(strings.TrimSpace(testPath))
	if testPath == "" {
		return false
	}
	for _, foreign := range ForeignTestFilesForActiveBead(activeBeadPath, v, mayorRigDir) {
		if PathMatchesImplementWrite(testPath, foreign, v.RequiredFiles, v) {
			return true
		}
	}
	return false
}

// ImplementBeadIDForForeignTestFile returns the open/in_progress implement bead that owns testPath's
// production source, or "" if none.
func ImplementBeadIDForForeignTestFile(townRoot, rig, activeBeadPath, testPath string, v WorkflowValidation) (beadID, prodPath string, ok bool) {
	testPath = filepath.ToSlash(strings.TrimSpace(testPath))
	src := SourcePathForCorrelatedTest(testPath, v.LayoutRoot)
	if src == "" {
		return "", "", false
	}
	if id, p, open := OpenImplementBeadForPath(townRoot, rig, src, v); open {
		return id, p, true
	}
	if id, p, open := OpenImplementBeadForPath(townRoot, rig, testPath, v); open {
		return id, p, true
	}
	return "", "", false
}

// AllowForeignOpenBeadCompileFixForVerifyFailure allows editing another bead's *_test.go when
// package verify fails to compile that file while a production bead is active (e.g. broken
// store_test.go blocking schema.go verify).
func AllowForeignOpenBeadCompileFixForVerifyFailure(townRoot, rig, activeBeadPath, writtenPath, verifyOutput string, v WorkflowValidation) bool {
	activeBeadPath = filepath.ToSlash(strings.TrimSpace(activeBeadPath))
	writtenPath = filepath.ToSlash(strings.TrimSpace(writtenPath))
	if activeBeadPath == "" || writtenPath == "" || !WorkflowUsesGo(v) || IsTestImplementPath(activeBeadPath) {
		return false
	}
	if !strings.HasSuffix(writtenPath, "_test.go") {
		return false
	}
	mayorRigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	if !IsForeignBeadTestFileForActive(activeBeadPath, writtenPath, v, mayorRigDir) {
		return false
	}
	out := strings.TrimSpace(verifyOutput)
	if out == "" {
		return false
	}
	if !GoCompileErrorsOnlyInTestFiles(out, v.LayoutRoot) && !strings.Contains(out, "[build failed]") {
		return false
	}
	if !GoCompileOutputCitesFile(out, writtenPath, v.LayoutRoot) {
		return false
	}
	return true
}

// AllowForeignOpenBeadProductionCompileFixForVerifyFailure allows the polecat to edit a production file
// from another open bead within the same package when a compile error blocks the active bead's verify.
// Handles both Go (.go) and Python (.py). Two files in the same package (e.g. schema.go and store.go in
// internal/store/, or models.py and views.py in backend/app/) can belong to different beads, and a
// compile/syntax error in the later bead's file prevents the package from compiling — blocking the
// earlier bead from being verified and closed.
func AllowForeignOpenBeadProductionCompileFixForVerifyFailure(townRoot, rig, activeBeadPath, writtenPath, verifyOutput string, v WorkflowValidation) bool {
	activeBeadPath = filepath.ToSlash(strings.TrimSpace(activeBeadPath))
	writtenPath = filepath.ToSlash(strings.TrimSpace(writtenPath))
	if activeBeadPath == "" || writtenPath == "" {
		return false
	}
	if !WorkflowUsesGo(v) && !WorkflowUsesPython(v) {
		return false
	}
	isGo := WorkflowUsesGo(v)
	if IsTestImplementPath(activeBeadPath) {
		return false
	}
	if IsTestImplementPath(writtenPath) {
		return false
	}
	if isGo {
		if !strings.HasSuffix(activeBeadPath, ".go") || !strings.HasSuffix(writtenPath, ".go") {
			return false
		}
	} else {
		if !strings.HasSuffix(activeBeadPath, ".py") || !strings.HasSuffix(writtenPath, ".py") {
			return false
		}
	}
	if !samePackagePath(activeBeadPath, writtenPath, v.LayoutRoot) {
		return false
	}
	if !pathMatchesRequired(writtenPath, v.RequiredFiles) {
		return false
	}
	closedOnly, err := ImplementPathHasOnlyClosedBeads(townRoot, rig, writtenPath, v)
	if err != nil || closedOnly {
		return false
	}
	out := strings.TrimSpace(verifyOutput)
	if out == "" {
		return false
	}
	if isGo {
		if !strings.Contains(out, "[build failed]") && !GoCompileErrorsOnlyInTestFiles(out, v.LayoutRoot) {
			return false
		}
		if !GoCompileOutputCitesFile(out, writtenPath, v.LayoutRoot) {
			return false
		}
	} else {
		if !strings.Contains(out, "[build failed]") && !pythonOutputHasCompileError(out) {
			return false
		}
		if !compileOutputCitesFilename(out, writtenPath) {
			return false
		}
	}
	return true
}

// samePackagePath reports whether two layout-relative paths are in the same directory (package/module).
func samePackagePath(pathA, pathB, layoutRoot string) bool {
	pathA = filepath.ToSlash(strings.TrimSpace(pathA))
	pathB = filepath.ToSlash(strings.TrimSpace(pathB))
	if pathA == "" || pathB == "" {
		return false
	}
	layout := strings.Trim(strings.TrimSpace(layoutRoot), "/")
	relA, relB := pathA, pathB
	if layout != "" {
		if strings.HasPrefix(relA, layout+"/") {
			relA = strings.TrimPrefix(relA, layout+"/")
		}
		if strings.HasPrefix(relB, layout+"/") {
			relB = strings.TrimPrefix(relB, layout+"/")
		}
	}
	return filepath.ToSlash(filepath.Dir(relA)) == filepath.ToSlash(filepath.Dir(relB))
}

// pythonOutputHasCompileError reports whether verify output contains Python compilation errors.
func pythonOutputHasCompileError(output string) bool {
	lower := strings.ToLower(output)
	signals := []string{
		"syntaxerror",
		"importerror",
		"modulenotfounderror",
		"indentationerror",
		"taberror",
		"collection failure",
		"error during collection",
		"failed: error",
	}
	for _, s := range signals {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

// compileOutputCitesFilename reports whether the output mentions the given relative file path.
func compileOutputCitesFilename(output, filePath string) bool {
	output = strings.TrimSpace(output)
	filePath = filepath.ToSlash(strings.TrimSpace(filePath))
	if output == "" || filePath == "" {
		return false
	}
	base := filepath.Base(filePath)
	return strings.Contains(output, base) || strings.Contains(output, filePath)
}

// FormatForeignOpenBeadTestCompileHint explains verify failures in another bead's *_test.go while
// a production bead is active, and how to fix or switch beads.
func FormatForeignOpenBeadTestCompileHint(townRoot, rig, activeBeadPath, cmdOutput string, v WorkflowValidation) string {
	activeBeadPath = filepath.ToSlash(strings.TrimSpace(activeBeadPath))
	if activeBeadPath == "" || IsTestImplementPath(activeBeadPath) {
		return ""
	}
	out := strings.TrimSpace(cmdOutput)
	if out == "" {
		return ""
	}
	if !GoCompileErrorsOnlyInTestFiles(out, v.LayoutRoot) && !strings.Contains(out, "[build failed]") {
		return ""
	}
	mayorRigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	foreign := ForeignTestFilesForActiveBead(activeBeadPath, v, mayorRigDir)
	if len(foreign) == 0 {
		return ""
	}
	cited := map[string]bool{}
	for _, testRel := range foreign {
		if GoCompileOutputCitesFile(out, testRel, v.LayoutRoot) {
			cited[testRel] = true
		}
	}
	if len(cited) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("### Foreign test file blocking package verify\n")
	b.WriteString("Go failed while compiling another implement bead's `*_test.go` in this package. ")
	b.WriteString("Your active bead is **`" + activeBeadPath + "`** — fix or defer that test file before `bd close`.\n\n")
	for testRel := range cited {
		id, prodPath, ok := ImplementBeadIDForForeignTestFile(townRoot, rig, activeBeadPath, testRel, v)
		if ok && id != "" {
			b.WriteString(fmt.Sprintf("- **`%s`** belongs to bead **%s** (`%s`). Either:\n", testRel, id, prodPath))
			b.WriteString(fmt.Sprintf("  - **EDIT:** **`%s`** now to fix compile errors cited in verify output (allowed while verify is red), then re-run **Verify**; or\n", testRel))
			b.WriteString(fmt.Sprintf("  - `CMD: export BEADS_DIR=$GT_ROOT/%s/.beads && cd %s/mayor/rig && bd update %s --status=in_progress` → fix **`%s`** / **`%s`** → Verify → `bd close %s`, then return to this bead.\n", rig, rig, id, prodPath, testRel, id))
		} else {
			b.WriteString(fmt.Sprintf("- Fix compile errors in **`%s`**, then re-run **Verify**.\n", testRel))
		}
	}
	b.WriteString("\nWhile this bead is active, post-write **Verify** uses **`go build`** on the production package so foreign `*_test.go` compile errors do not block **`" + activeBeadPath + "`** — run full **`go test`** before closing if you changed tests.\n")
	return strings.TrimSpace(b.String())
}

// foreignTestErrorsCiteOtherBeadTests reports whether cmdOutput cites *_test.go files that belong
// to implement beads other than the active production bead (schema vs store_test, etc.).
func foreignTestErrorsCiteOtherBeadTests(activeBeadPath, cmdOutput string, v WorkflowValidation, mayorRigDir string) bool {
	for _, testRel := range ForeignTestFilesForActiveBead(activeBeadPath, v, mayorRigDir) {
		if GoCompileOutputCitesFile(cmdOutput, testRel, v.LayoutRoot) {
			return true
		}
	}
	return false
}
