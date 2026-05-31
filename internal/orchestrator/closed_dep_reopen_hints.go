package orchestrator

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var goCompileFileLineRE = regexp.MustCompile(`(?m)(?:^|\s|\])([a-zA-Z0-9_./-]+\.go):(\d+)`)

// goAllFuncsRE matches any func declaration (exported or unexported) in Go source.
var goAllFuncsRE = regexp.MustCompile(`(?m)^func\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)

// goUndefinedSymbolRE extracts the symbol name from "undefined: SymbolName" compile diagnostics.
var goUndefinedSymbolRE = regexp.MustCompile(`undefined:\s+(\w+)`)

// SameImplementGoPackage reports whether two layout-relative paths are in the same Go package directory.
func SameImplementGoPackage(pathA, pathB, layoutRoot string) bool {
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

// GoCompileOutputCitesFile reports whether cmdOutput contains a file:line diagnostic for filePath.
func GoCompileOutputCitesFile(cmdOutput, filePath, layoutRoot string) bool {
	cmdOutput = strings.TrimSpace(cmdOutput)
	filePath = filepath.ToSlash(strings.TrimSpace(filePath))
	if cmdOutput == "" || filePath == "" {
		return false
	}
	base := filepath.Base(filePath)
	rel := filePath
	layout := strings.Trim(strings.TrimSpace(layoutRoot), "/")
	if layout != "" && strings.HasPrefix(rel, layout+"/") {
		rel = strings.TrimPrefix(rel, layout+"/")
	}
	for _, m := range goCompileFileLineRE.FindAllStringSubmatch(cmdOutput, -1) {
		if len(m) < 2 {
			continue
		}
		cited := filepath.ToSlash(strings.TrimSpace(m[1]))
		if cited == filePath || cited == rel || filepath.Base(cited) == base {
			return true
		}
		if layout != "" && (cited == layout+"/"+rel || strings.HasSuffix(cited, "/"+rel)) {
			return true
		}
	}
	return false
}

// GoCompileErrorsOnlyInTestFiles reports whether every file:line diagnostic in cmdOutput is in *_test.go.
func GoCompileErrorsOnlyInTestFiles(cmdOutput, layoutRoot string) bool {
	cmdOutput = strings.TrimSpace(cmdOutput)
	if cmdOutput == "" {
		return false
	}
	seen := false
	for _, m := range goCompileFileLineRE.FindAllStringSubmatch(cmdOutput, -1) {
		if len(m) < 2 {
			continue
		}
		seen = true
		cited := filepath.ToSlash(strings.TrimSpace(m[1]))
		if layoutRoot != "" {
			cited = NormalizeBeadPathForLayout(cited, layoutRoot)
		}
		if !strings.HasSuffix(cited, "_test.go") {
			return false
		}
	}
	return seen
}

// ProductionGoPathsFromRequired returns production .go paths from profile required_files (build order).
func ProductionGoPathsFromRequired(required []string) []string {
	var out []string
	for _, p := range OrderRequiredFilesForImplementation(required) {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if p == "" || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			continue
		}
		out = append(out, p)
	}
	return out
}

// ShouldSuggestReopenClosedDep decides whether to tell the polecat to reopen a closed earlier bead.
// Softens false positives when verify fails only in *_test.go in the same package as the active production bead.
func ShouldSuggestReopenClosedDep(activeBeadPath, depPath, cmdOutput string, v WorkflowValidation) bool {
	activeBeadPath = filepath.ToSlash(strings.TrimSpace(activeBeadPath))
	depPath = filepath.ToSlash(strings.TrimSpace(depPath))
	if depPath == "" || activeBeadPath == depPath {
		return false
	}
	if cmdOutput == "" {
		return true
	}
	if SameImplementGoPackage(activeBeadPath, depPath, v.LayoutRoot) &&
		!IsTestImplementPath(activeBeadPath) &&
		GoCompileErrorsOnlyInTestFiles(cmdOutput, v.LayoutRoot) {
		return false
	}
	if GoCompileOutputCitesFile(cmdOutput, depPath, v.LayoutRoot) {
		return true
	}
	if compileOutputSuggestsCrossPackage(cmdOutput) &&
		!SameImplementGoPackage(activeBeadPath, depPath, v.LayoutRoot) {
		return outputMentionsPath(cmdOutput, depPath, v.LayoutRoot)
	}
	return false
}

// FormatSamePackageTestAPIHint guides fixing *_test.go to match on-disk production API in the active bead.
// When the production file was rewritten with new function names, it lists the specific mismatched symbols.
// mayorRigDir is {townRoot}/{rig}/mayor/rig (needed to read the production file from disk).
func FormatSamePackageTestAPIHint(activeBeadPath, mayorRigDir, cmdOutput string, v WorkflowValidation) string {
	activeBeadPath = filepath.ToSlash(strings.TrimSpace(activeBeadPath))
	if activeBeadPath == "" || IsTestImplementPath(activeBeadPath) || !GoCompileErrorsOnlyInTestFiles(cmdOutput, v.LayoutRoot) {
		return ""
	}
	corr := CorrelatedTestPathForSource(activeBeadPath, v)

	// Extract undefined symbols referenced by tests but missing from production.
	var missing []string
	seen := map[string]bool{}
	for _, m := range goUndefinedSymbolRE.FindAllStringSubmatch(cmdOutput, -1) {
		if len(m) >= 2 && !seen[m[1]] {
			seen[m[1]] = true
			missing = append(missing, m[1])
		}
	}

	// Read the production file and list available function names.
	var available []string
	if mayorRigDir != "" {
		activeAbs := filepath.Join(mayorRigDir, filepath.FromSlash(activeBeadPath))
		if data, err := os.ReadFile(activeAbs); err == nil {
			fseen := map[string]bool{}
			for _, m := range goAllFuncsRE.FindAllStringSubmatch(string(data), -1) {
				if len(m) >= 2 && !fseen[m[1]] {
					fseen[m[1]] = true
					available = append(available, m[1])
				}
			}
		}
	}

	var b strings.Builder
	b.WriteString("### Fix tests vs production API (same package)\n")
	b.WriteString("Go reported errors only in `*_test.go`, not in closed production files for this package. ")
	b.WriteString("Do **not** reopen other closed beads in this package unless go output cites `path.go:line` for that file.\n")
	if len(missing) > 0 {
		b.WriteString("\n**Tests call these functions that don't exist on disk:** `")
		b.WriteString(strings.Join(missing, "`, `"))
		b.WriteString("`\n")
	}
	if len(available) > 0 {
		b.WriteString("**Production file `" + activeBeadPath + "` provides:** `")
		b.WriteString(strings.Join(available, "`, `"))
		b.WriteString("`\n\n")
	}
	if len(missing) > 0 && len(available) > 0 {
		b.WriteString("**Choose one approach:**\n")
		b.WriteString("- **EDIT `" + activeBeadPath + "`** to rename your functions back to what the tests expect (add `" + strings.Join(missing, "`, `") + "`), OR\n")
		if corr != "" {
			b.WriteString("- **EDIT `" + corr + "`** to call the new function names (`" + strings.Join(available, "`, `") + "`) instead — you **may** edit the test file while this production bead is active.\n")
		}
		b.WriteString("- Do **not** rewrite the whole file again — use targeted **EDIT:** with SEARCH/REPLACE for specific function renames or test updates.\n")
	} else if corr != "" {
		b.WriteString("- Align **`" + corr + "`** with the symbols and signatures already in **`" + activeBeadPath + "`** (see Source context above).\n")
	} else {
		b.WriteString("- Fix tests to call the API implemented in **`" + activeBeadPath + "`** (see Source context above).\n")
	}
	b.WriteString("- Duplicate `func Test…` names are auto-deduped on **EDIT:**/**WRITE:** (first wins); prefer **EDIT:** on the test bead path, not a full **WRITE:** rewrite.\n")
	b.WriteString("- Re-run **Verify** from the Next bead line.\n")
	return strings.TrimSpace(b.String())
}
