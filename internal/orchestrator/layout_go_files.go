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
	return fmt.Sprintf("%sgo mod tidy && go test -count=1 ./%s/...", GoShellCDClause(mayorRigDir, v.LayoutRoot), pkg)
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
	return fmt.Sprintf("%sgo mod tidy && go test -count=1 ./%s/... -run '%s'",
		GoShellCDClause(mayorRigDir, v.LayoutRoot), pkg, strings.Join(names, "|"))
}

func goBuildVerifyForPackage(v WorkflowValidation, mayorRigDir, beadPath string) string {
	pkg := GoBuildRelPackage(v.LayoutRoot, beadPath)
	if pkg == "" {
		return GoCompileOnlyVerifyCommand(v, mayorRigDir)
	}
	return fmt.Sprintf("%sgo mod tidy && go build ./%s/...", GoShellCDClause(mayorRigDir, v.LayoutRoot), pkg)
}

// layoutGoRelPathsProtectedFromPrune returns layout-relative .go paths that must not be deleted
// (required_files plus correlated *_test.go for each production source bead).
func layoutGoRelPathsProtectedFromPrune(v WorkflowValidation) map[string]bool {
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
		if strings.HasSuffix(p, ".go") && p != "" {
			protected[p] = true
		}
	}
	for _, f := range files {
		add(f)
		if WorkflowUsesGo(v) && !IsTestImplementPath(f) {
			if testPath := CorrelatedTestPathForSource(f, v); testPath != "" {
				add(testPath)
			}
		}
	}
	return protected
}

// layoutGoBasenamesProtectedFromPrune guards flat layouts (pingapp/main.go) when beads or
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

// PruneStaleLayoutGoFiles removes .go files under layout_root that are not listed in required_files
// (full union when delivery phases are configured). Junk like internal/app/ from hallucinated writes
// is removed even while implement beads are open.
func PruneStaleLayoutGoFiles(townRoot, rig string, v WorkflowValidation) ([]string, error) {
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	if layout == "" || len(v.RequiredFiles) == 0 {
		return nil, nil
	}
	required := layoutGoRelPathsProtectedFromPrune(v)
	if len(required) == 0 {
		return nil, nil
	}
	root := filepath.Join(townRoot, rig, "mayor", "rig", layout)
	basenameProtected := layoutGoBasenamesProtectedFromPrune(v)
	if RequiresExactImplementPaths(v) {
		basenameProtected = nil
	}
	var removed []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if required[rel] {
			return nil
		}
		if basenameProtected[filepath.Base(rel)] {
			return nil
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		removed = append(removed, filepath.ToSlash(filepath.Join(layout, rel)))
		return nil
	})
	return removed, err
}

func PruneStaleLayoutGoFilesLog(townRoot, rig string, v WorkflowValidation) (string, error) {
	removed, err := PruneStaleLayoutGoFiles(townRoot, rig, v)
	if err != nil {
		return "", err
	}
	if len(removed) == 0 {
		return "", nil
	}
	return fmt.Sprintf("removed stale .go files: %s", joinStrings(removed, ", ")), nil
}
