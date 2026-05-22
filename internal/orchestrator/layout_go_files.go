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
func GoTestVerifyCommandForPackage(v WorkflowValidation, beadPath string) string {
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" {
		layout = "."
	}
	pkg := GoBuildRelPackage(v.LayoutRoot, beadPath)
	if pkg == "" {
		return GoCompileOnlyVerifyCommand(v)
	}
	return fmt.Sprintf("cd %s && go mod tidy && go test -count=1 ./%s/...", layout, pkg)
}

// GoCompileVerifyCommandForBead is verify scoped to the active implement file's package.
// Test beads always run go test. Production .go beads run go test when tests exist on disk,
// or when there is no separate *_test.go bead; otherwise go build until the test bead is implemented.
func GoCompileVerifyCommandForBead(v WorkflowValidation, mayorRigDir, beadPath string) string {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	if strings.HasSuffix(beadPath, ".go") && !strings.HasSuffix(beadPath, "go.mod") && !IsServerMainImplementBead(beadPath) {
		if IsTestImplementPath(beadPath) {
			return GoTestVerifyCommandForPackage(v, beadPath)
		}
		testPath := CorrelatedTestPathForSource(beadPath, v.LayoutRoot)
		if testPath != "" && TestPathListedInRequired(beadPath, v.RequiredFiles, v.LayoutRoot) {
			if _, err := os.Stat(filepath.Join(mayorRigDir, testPath)); os.IsNotExist(err) {
				return goBuildVerifyForPackage(v, beadPath)
			}
		}
		return GoTestVerifyCommandForPackage(v, beadPath)
	}
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" {
		layout = "."
	}
	return goBuildVerifyForPackage(v, beadPath)
}

func goBuildVerifyForPackage(v WorkflowValidation, beadPath string) string {
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" {
		layout = "."
	}
	pkg := GoBuildRelPackage(v.LayoutRoot, beadPath)
	if pkg == "" {
		return GoCompileOnlyVerifyCommand(v)
	}
	return fmt.Sprintf("cd %s && go mod tidy && go build ./%s/...", layout, pkg)
}

// layoutGoRelPathsProtectedFromPrune returns layout-relative .go paths that must not be deleted
// (required_files plus correlated *_test.go for each production source bead).
func layoutGoRelPathsProtectedFromPrune(v WorkflowValidation) map[string]bool {
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	if layout == "" || len(v.RequiredFiles) == 0 {
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
	for _, f := range v.RequiredFiles {
		add(f)
		if WorkflowUsesGo(v) && !IsTestImplementPath(f) {
			if testPath := CorrelatedTestPathForSource(f, v.LayoutRoot); testPath != "" {
				add(testPath)
			}
		}
	}
	return protected
}

// PruneStaleLayoutGoFiles removes .go files under layout_root that are not listed in required_files.
func PruneStaleLayoutGoFiles(townRoot, rig string, v WorkflowValidation) ([]string, error) {
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	if layout == "" || len(v.RequiredFiles) == 0 {
		return nil, nil
	}
	root := filepath.Join(townRoot, rig, "mayor", "rig", layout)
	required := layoutGoRelPathsProtectedFromPrune(v)
	if len(required) == 0 {
		return nil, nil
	}
	var removed []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if required[rel] {
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
