package orchestrator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	goCompileDiagFileRE     = regexp.MustCompile(`(?m)(?:^|[\s\]])\.?/?([a-zA-Z0-9_./-]+\.go):\d+`)
	goCompileTestPackageRE  = regexp.MustCompile(`\[(?:[a-zA-Z0-9_.-]+\.)?([a-zA-Z0-9_./-]+)\.test\]`)
	goCompileHashPackageRE  = regexp.MustCompile(`(?m)^#\s+([a-zA-Z0-9_./-]+)\s+\[`)
)

// GoCompileOutputHasUnusedImport reports build/test output blocked by unused imports.
func GoCompileOutputHasUnusedImport(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "imported and not used") ||
		strings.Contains(lower, "not used (") && strings.Contains(lower, "import ")
}

// FormatUnusedImportCompileHint returns polecat guidance when verify fails on unused imports.
func FormatUnusedImportCompileHint(output string) string {
	if !GoCompileOutputHasUnusedImport(output) {
		return ""
	}
	return strings.TrimSpace(`### Unused import (compile blocked)
go test/build failed because an import is unused — often left after a failed large EDIT.

**Do not** send another huge SEARCH block. Use a **small EDIT** on the import block only (remove the unused line), or run verify again after gt-agent runs goimports.

Example:
` + "```" + `
EDIT: linkshelf/internal/api/handlers_test.go
<<<<<<< SEARCH
	"encoding/json"
	"fmt"
	"net/http"
=======
	"encoding/json"
	"net/http"
>>>>>>> REPLACE
` + "```")
}

// RunGoimportsOnFile runs goimports -w on relPath under mayorRigDir (module root).
// No-op when goimports is not installed.
func RunGoimportsOnFile(mayorRigDir, relPath string) (ran bool, err error) {
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	if relPath == "" || !strings.HasSuffix(relPath, ".go") {
		return false, nil
	}
	path, err := exec.LookPath("goimports")
	if err != nil {
		return false, nil
	}
	mayorRigDir = strings.TrimSpace(mayorRigDir)
	if mayorRigDir == "" {
		return false, fmt.Errorf("empty mayorRigDir")
	}
	cmd := exec.Command(path, "-w", relPath)
	cmd.Dir = mayorRigDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return true, fmt.Errorf("goimports -w %s: %w: %s", relPath, err, strings.TrimSpace(string(out)))
	}
	return true, nil
}

// goCompilePackageDirFromOutput returns the package directory from go test headers
// (e.g. # linkshelf/internal/api [linkshelf/internal/api.test] → linkshelf/internal/api).
func goCompilePackageDirFromOutput(output, layoutRoot string) string {
	if m := goCompileTestPackageRE.FindStringSubmatch(output); len(m) >= 2 {
		return NormalizeBeadPathForLayout(filepath.ToSlash(m[1]), layoutRoot)
	}
	if m := goCompileHashPackageRE.FindStringSubmatch(output); len(m) >= 2 {
		return NormalizeBeadPathForLayout(filepath.ToSlash(m[1]), layoutRoot)
	}
	return ""
}

// GoFilePathsFromCompileOutput extracts .go paths from go test/build stderr (e.g. ./handlers_test.go:6:2).
func GoFilePathsFromCompileOutput(output, layoutRoot string) []string {
	layout := strings.Trim(strings.TrimSpace(layoutRoot), "/")
	pkgDir := goCompilePackageDirFromOutput(output, layout)
	seen := map[string]bool{}
	var paths []string
	add := func(p string) {
		p = filepath.ToSlash(strings.TrimSpace(p))
		p = strings.TrimPrefix(p, "./")
		if !strings.Contains(p, "/") && pkgDir != "" {
			p = pkgDir + "/" + p
		}
		if layout != "" {
			p = NormalizeBeadPathForLayout(p, layout)
		}
		if p == "" || !strings.HasSuffix(p, ".go") || seen[p] {
			return
		}
		seen[p] = true
		paths = append(paths, p)
	}
	for _, m := range goCompileDiagFileRE.FindAllStringSubmatch(output, -1) {
		if len(m) >= 2 {
			add(m[1])
		}
	}
	return paths
}

// packageDirsForGoFiles returns unique directory paths (layout-relative) for the given files.
func packageDirsForGoFiles(files []string) []string {
	seen := map[string]bool{}
	var dirs []string
	for _, f := range files {
		d := filepath.ToSlash(filepath.Dir(f))
		if d == "" || d == "." || seen[d] {
			continue
		}
		seen[d] = true
		dirs = append(dirs, d)
	}
	return dirs
}

// GoFilesInPackageDir lists layout-relative .go files in a package directory under mayorRigDir.
func GoFilesInPackageDir(mayorRigDir, pkgDir string) ([]string, error) {
	pkgDir = filepath.ToSlash(strings.TrimSpace(pkgDir))
	if pkgDir == "" || pkgDir == "." {
		return nil, nil
	}
	abs := filepath.Join(mayorRigDir, filepath.FromSlash(pkgDir))
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		files = append(files, filepath.ToSlash(filepath.Join(pkgDir, e.Name())))
	}
	return files, nil
}

// RunGoimportsOnCompileOutput runs goimports on all .go files in packages cited by compile output.
// Fixes unused imports in test files while the active bead is a sibling production file.
func RunGoimportsOnCompileOutput(mayorRigDir, layoutRoot, output string) (touched []string, ran bool, err error) {
	if !GoCompileOutputHasUnusedImport(output) {
		return nil, false, nil
	}
	cited := GoFilePathsFromCompileOutput(output, layoutRoot)
	dirs := packageDirsForGoFiles(cited)
	seen := map[string]bool{}
	for _, dir := range dirs {
		files, readErr := GoFilesInPackageDir(mayorRigDir, dir)
		if readErr != nil {
			return touched, true, readErr
		}
		for _, f := range files {
			if seen[f] {
				continue
			}
			seen[f] = true
			didRun, tidyErr := RunGoimportsOnFile(mayorRigDir, f)
			if !didRun {
				continue
			}
			ran = true
			if tidyErr != nil {
				return touched, true, tidyErr
			}
			touched = append(touched, f)
		}
	}
	if !ran {
		for _, f := range cited {
			didRun, tidyErr := RunGoimportsOnFile(mayorRigDir, f)
			if !didRun {
				continue
			}
			ran = true
			if tidyErr != nil {
				return touched, true, tidyErr
			}
			touched = append(touched, f)
		}
	}
	return touched, ran, nil
}

var nilDBPointerHintPatterns = []string{
	"nil pointer dereference",
	"invalid memory address",
	"nil pointer",
}

// GoNilDBPointerHint returns a targeted hint when verify output contains a nil-*sql.DB panic
// (a package-level *sql.DB variable or database handle was not initialized before use).
func GoNilDBPointerHint(output string) string {
	if strings.TrimSpace(output) == "" {
		return ""
	}
	hasNil := false
	hasDB := false
	for _, pat := range nilDBPointerHintPatterns {
		if strings.Contains(output, pat) {
			hasNil = true
			break
		}
	}
	if strings.Contains(output, "*sql.DB") || strings.Contains(output, "DB.QueryContext") ||
		strings.Contains(output, "database/sql.(*DB)") {
		hasDB = true
	}
	if !hasNil || !hasDB {
		return ""
	}
	return strings.TrimSpace(`### Package-level *sql.DB is nil (uninitialized)

` + "`panic: runtime error: invalid memory address or nil pointer dereference`" + ` in a ` + "`*sql.DB`" + ` method means a package-level database handle was never set before a test or handler called into it.

**Fix:** add a DB init block at the top of the failing test function:

` + "```go" + `
db, err := sql.Open("sqlite3", ":memory:")
if err != nil {
    t.Fatal(err)
}
// set the package-level DB variable (e.g. store.DB, pkg.DB, etc.)
// from the package the trace points to
<package>.DB = db
// initialize the schema (call the InitSchema-like function from that package)
<package>.InitSchema(db)
` + "```" + `

Import ` + "`\"database/sql\"`" + ` and the package owning the DB variable. Do **not** rename handler functions or refactor proven code — the nil pointer is a test setup-only issue.`)
}
