package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GoBuildRelPackage returns the module-relative package directory for a bead .go path
// (e.g. linkshelf/internal/store/store.go → internal/store).
func GoBuildRelPackage(layoutRoot, beadPath string) string {
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(layoutRoot)), "/")
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	if layout != "" && strings.HasPrefix(beadPath, layout+"/") {
		beadPath = strings.TrimPrefix(beadPath, layout+"/")
	}
	if !strings.HasSuffix(beadPath, ".go") {
		return ""
	}
	return strings.Trim(filepath.ToSlash(filepath.Dir(beadPath)), "/")
}

// GoTestVerifyCommandForPackage runs unit tests for the package containing beadPath.
func GoTestVerifyCommandForPackage(v WorkflowValidation, mayorRigDir, beadPath string) string {
	pkg := GoBuildRelPackage(v.LayoutRoot, beadPath)
	if pkg == "" {
		return GoCompileOnlyVerifyCommand(v, mayorRigDir)
	}
	return fmt.Sprintf("%sgo mod tidy && go test -timeout 30s -count=1 %s", GoShellCDClause(mayorRigDir, v.LayoutRoot), goPackagePattern(pkg))
}

// GoCompileVerifyCommandForBead is verify scoped to the active implement file's package.
// Test beads always run go test. Production .go beads run go test when tests exist on disk,
// or when there is no separate *_test.go bead; otherwise go build until the test bead is implemented.
// When the package contains another bead's *_test.go, verify runs only this bead's Test* functions (-run).
func GoCompileVerifyCommandForBead(v WorkflowValidation, mayorRigDir, beadPath string) string {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	if IsFrontendImplementPath(beadPath) {
		return ""
	}
	var cmd string
	if strings.HasSuffix(beadPath, ".go") && !strings.HasSuffix(beadPath, "go.mod") && !IsServerMainImplementBead(beadPath) {
		if IsTestImplementPath(beadPath) {
			cmd = GoTestVerifyCommandForPackage(v, mayorRigDir, beadPath)
		} else {
			testPath := CorrelatedTestPathForSource(beadPath, v)
			if testPath != "" {
				testAbs := ResolveRequiredFileOnDisk(mayorRigDir, testPath, v.LayoutRoot)
				if _, err := os.Stat(testAbs); os.IsNotExist(err) {
					if TestPathListedInRequired(beadPath, v) || PackageHasForeignTestFiles(beadPath, v, mayorRigDir) {
						cmd = goBuildVerifyForPackage(v, mayorRigDir, beadPath)
					} else {
						cmd = GoTestVerifyCommandForPackage(v, mayorRigDir, beadPath)
					}
				} else if scoped := goTestVerifyScopedToBead(v, mayorRigDir, beadPath, testPath); scoped != "" {
					cmd = scoped
				} else {
					cmd = GoTestVerifyCommandForPackage(v, mayorRigDir, beadPath)
				}
			} else {
				cmd = GoTestVerifyCommandForPackage(v, mayorRigDir, beadPath)
			}
		}
	} else {
		cmd = goBuildVerifyForPackage(v, mayorRigDir, beadPath)
	}
	return AppendGoBuildCmdServerToVerify(cmd, mayorRigDir, beadPath, v)
}

// goTestVerifyScopedToBead runs go test -run for this bead's Test* funcs when sibling production
// .go files belong to other implement beads. Foreign *_test.go files from other beads always
// compile under go test (even with -run), so verify falls back to go build for production only.
func goTestVerifyScopedToBead(v WorkflowValidation, mayorRigDir, beadPath, correlatedTest string) string {
	if PackageHasForeignTestFiles(beadPath, v, mayorRigDir) {
		return goBuildVerifyForPackage(v, mayorRigDir, beadPath)
	}
	if !packageNeedsScopedGoTest(beadPath, v, mayorRigDir) {
		return ""
	}
	pkg := GoBuildRelPackage(v.LayoutRoot, beadPath)
	if pkg == "" {
		return ""
	}
	data, err := os.ReadFile(ResolveRequiredFileOnDisk(mayorRigDir, correlatedTest, v.LayoutRoot))
	if err != nil {
		return goBuildVerifyForPackage(v, mayorRigDir, beadPath)
	}
	names := TestFuncNamesFromGoTestFile(data)
	if len(names) == 0 {
		return goBuildVerifyForPackage(v, mayorRigDir, beadPath)
	}
	return fmt.Sprintf("%sgo mod tidy && go test -timeout 30s -count=1 ./%s/... -run '%s'",
		GoShellCDClause(mayorRigDir, v.LayoutRoot), pkg, strings.Join(names, "|"))
}

func goBuildVerifyForPackage(v WorkflowValidation, mayorRigDir, beadPath string) string {
	pkg := GoBuildRelPackage(v.LayoutRoot, beadPath)
	if pkg == "" {
		return GoCompileOnlyVerifyCommand(v, mayorRigDir)
	}
	return fmt.Sprintf("%sgo mod tidy && go build %s", GoShellCDClause(mayorRigDir, v.LayoutRoot), goPackagePattern(pkg))
}

// goPackagePattern renders the package selector for a module-relative package dir.
// "." means the module root, so `./...` (not `././...`).
func goPackagePattern(pkg string) string {
	if pkg == "" || pkg == "." {
		return "./..."
	}
	return "./" + pkg + "/..."
}

// managedSourceExtensions lists file extensions that the system owns and prunes.
// Files with these extensions that are NOT in required_files are removed as stale.
var managedSourceExtensions = []string{
	".go", ".py", ".ts", ".tsx", ".js", ".jsx",
	".css", ".html", ".sh", ".ps1",
	".yml", ".yaml", ".sql", ".toml",
}

// hasManagedExtension reports whether path has one of the managed source extensions.
func hasManagedExtension(path string) bool {
	lower := strings.ToLower(filepath.Ext(path))
	for _, ext := range managedSourceExtensions {
		if lower == ext {
			return true
		}
	}
	return false
}

// layoutRelPathsProtectedFromPrune returns layout-relative paths that must not be deleted
// (required_files plus correlated test files for each production source bead).
func layoutRelPathsProtectedFromPrune(v WorkflowValidation) map[string]bool {
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	if layout == "" {
		return nil
	}
	files := v.RequiredFiles
	if v.HasPhasedDelivery() {
		files = v.UnionRequiredFiles()
	}
	if len(files) == 0 {
		return nil
	}
	protected := map[string]bool{}
	add := func(fullPath string) {
		p := filepath.ToSlash(strings.TrimSpace(fullPath))
		p = strings.TrimPrefix(p, layout+"/")
		if p != "" && hasManagedExtension(p) {
			protected[p] = true
		}
	}
	for _, f := range files {
		add(f)
		if !IsTestImplementPath(f) {
			if testPath := CorrelatedTestPathForSource(f, v); testPath != "" {
				add(testPath)
			}
		}
	}
	// Protect common test config files that may be auto-generated or not explicitly listed
	commonTestConfigs := []string{
		"test/playwright.config.ts",
		"test/playwright.config.js",
		"tests/playwright.config.ts",
		"tests/playwright.config.js",
		"test/cypress.config.ts",
		"test/cypress.config.js",
		"tests/cypress.config.ts",
		"tests/cypress.config.js",
		"test/jest.config.ts",
		"test/jest.config.js",
		"tests/jest.config.ts",
		"tests/jest.config.js",
		"test/vitest.config.ts",
		"test/vitest.config.js",
		"tests/vitest.config.ts",
		"tests/vitest.config.js",
		"test/karma.conf.js",
		"test/mocha.opts",
		"tests/karma.conf.js",
		"tests/mocha.opts",
		"cypress.config.ts",
		"cypress.config.js",
		"playwright.config.ts",
		"playwright.config.js",
		"jest.config.ts",
		"jest.config.js",
		"vitest.config.ts",
		"vitest.config.js",
	}
	for _, cfg := range commonTestConfigs {
		protected[cfg] = true
	}
	return protected
}

// layoutGoBasenamesProtectedFromPrune guards flat Go layouts (pingapp/main.go) when beads or
// architecture drift to linkshelf-style paths (pingapp/cmd/main.go).
func layoutGoBasenamesProtectedFromPrune(v WorkflowValidation) map[string]bool {
	if len(v.RequiredFiles) == 0 {
		return nil
	}
	bases := map[string]bool{}
	add := func(fullPath string) {
		p := filepath.ToSlash(strings.TrimSpace(fullPath))
		if strings.HasSuffix(p, ".go") {
			bases[filepath.Base(p)] = true
		}
	}
	for _, f := range v.RequiredFiles {
		add(f)
		if WorkflowUsesGo(v) && !IsTestImplementPath(f) {
			if testPath := CorrelatedTestPathForSource(f, v); testPath != "" {
				add(testPath)
			}
		}
	}
	return bases
}

// PruneStaleLayoutFiles removes managed source files under layout_root that are not listed
// in required_files (full union when delivery phases are configured). Files from previous
// iterations that are no longer needed are removed even while implement beads are open.
// PruneStaleLayoutFiles removes managed source files under layout_root that are not listed
// in v.RequiredFiles. Files that cannot be removed (e.g. root-owned artifacts written by a
// docker test container) are collected as warnings and skipped instead of aborting the whole
// walk, so the workflow continues and the next prune can finish them once ownership is fixed.
func PruneStaleLayoutFiles(townRoot, rig string, v WorkflowValidation) (removed []string, warnings []string, err error) {
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	if layout == "" || len(v.RequiredFiles) == 0 {
		return nil, nil, nil
	}
	required := layoutRelPathsProtectedFromPrune(v)
	if len(required) == 0 {
		return nil, nil, nil
	}
	root := filepath.Join(townRoot, rig, "mayor", "rig", layout)
	basenameGo := layoutGoBasenamesProtectedFromPrune(v)
	if RequiresExactImplementPaths(v) {
		basenameGo = nil
	}
	err = filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		// Skip dependency directories that are not source files managed by the pipeline.
		base := filepath.Base(path)
		if info.IsDir() && (base == "node_modules" || base == v.PythonVenvRelDir()) {
			return filepath.SkipDir
		}
		if info.IsDir() || !hasManagedExtension(path) {
			return nil
		}
		if required[rel] {
			return nil
		}
		// Basename protection only applies to .go files (flat vs nested path drift)
		if strings.HasSuffix(rel, ".go") && basenameGo[filepath.Base(rel)] {
			return nil
		}
		if rmErr := os.Remove(path); rmErr != nil {
			warnings = append(warnings, filepath.ToSlash(filepath.Join(layout, rel))+": "+rmErr.Error())
			return nil
		}
		removed = append(removed, filepath.ToSlash(filepath.Join(layout, rel)))
		return nil
	})
	return removed, warnings, err
}

func PruneStaleLayoutFilesLog(townRoot, rig string, v WorkflowValidation) (string, error) {
	removed, warnings, err := PruneStaleLayoutFiles(townRoot, rig, v)
	if err != nil {
		return "", err
	}
	var parts []string
	if len(removed) > 0 {
		parts = append(parts, "removed stale files: "+joinStrings(removed, ", "))
	}
	if len(warnings) > 0 {
		parts = append(parts, "warning: could not remove stale files (likely root-owned by a docker test container): "+joinStrings(warnings, "; "))
	}
	return strings.Join(parts, "; "), nil
}
