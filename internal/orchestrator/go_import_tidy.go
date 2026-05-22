package orchestrator

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
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
