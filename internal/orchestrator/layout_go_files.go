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

// GoCompileVerifyCommandForBead is compile verify scoped to the active implement file's package.
func GoCompileVerifyCommandForBead(v WorkflowValidation, beadPath string) string {
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

// PruneStaleLayoutGoFiles removes .go files under layout_root that are not listed in required_files.
func PruneStaleLayoutGoFiles(townRoot, rig string, v WorkflowValidation) ([]string, error) {
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	if layout == "" || len(v.RequiredFiles) == 0 {
		return nil, nil
	}
	root := filepath.Join(townRoot, rig, "mayor", "rig", layout)
	required := map[string]bool{}
	for _, f := range v.RequiredFiles {
		p := filepath.ToSlash(strings.TrimSpace(f))
		p = strings.TrimPrefix(p, layout+"/")
		if strings.HasSuffix(p, ".go") {
			required[p] = true
		}
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
