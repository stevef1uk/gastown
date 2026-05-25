package orchestrator

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// ProductionPathsFromImportedPackages lists production .go files in packages imported by citedPaths (on disk under rigDir).
func ProductionPathsFromImportedPackages(rigDir, layoutRoot string, citedPaths []string) []string {
	rigDir = strings.TrimRight(strings.TrimSpace(rigDir), "/")
	layout := strings.Trim(strings.TrimSpace(layoutRoot), "/")
	if rigDir == "" || layout == "" {
		return nil
	}
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
	for _, rel := range citedPaths {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" || !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			continue
		}
		if !strings.HasPrefix(rel, layout+"/") {
			continue
		}
		abs := filepath.Join(rigDir, filepath.FromSlash(rel))
		for _, imp := range goFileImportPaths(abs) {
			if !strings.HasPrefix(imp, layout+"/") {
				continue
			}
			pkgDir := filepath.Join(rigDir, filepath.FromSlash(imp))
			entries, err := os.ReadDir(pkgDir)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
					continue
				}
				add(filepath.ToSlash(filepath.Join(imp, e.Name())))
			}
		}
	}
	return out
}

func goFileImportPaths(abs string) []string {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, abs, nil, parser.ImportsOnly)
	if err != nil {
		return nil
	}
	var paths []string
	for _, imp := range f.Imports {
		if imp == nil || imp.Path == nil {
			continue
		}
		p := strings.Trim(imp.Path.Value, `"`)
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}
