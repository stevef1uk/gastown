package orchestrator

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// GoSourceBytesValid reports whether src is syntactically valid Go source.
func GoSourceBytesValid(src []byte) error {
	if len(strings.TrimSpace(string(src))) == 0 {
		return fmt.Errorf("empty source")
	}
	fset := token.NewFileSet()
	_, err := parser.ParseFile(fset, "file.go", src, parser.ParseComments)
	return err
}

// GoFileAtMayorRigParses reports whether an on-disk implement file parses as Go.
func GoFileAtMayorRigParses(townRoot, rig, relPath string) bool {
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	if relPath == "" || !strings.HasSuffix(relPath, ".go") {
		return true
	}
	abs := filepath.Join(townRoot, rig, "mayor", "rig", filepath.FromSlash(relPath))
	data, err := os.ReadFile(abs)
	if err != nil {
		return true
	}
	return GoSourceBytesValid(data) == nil
}

// FormatCorruptedGoFileRecoveryHint returns feedback when verify shows syntax errors on the active file.
func FormatCorruptedGoFileRecoveryHint(cmdOutput string, paths []string) string {
	if !strings.Contains(cmdOutput, "syntax error") {
		return ""
	}
	for _, p := range paths {
		if strings.HasSuffix(p, ".go") {
			return strings.TrimSpace(fmt.Sprintf(`### Corrupted Go file recovery
**%s** does not compile (stacked EDIT/search-replace fragments). Do **not** apply more small EDIT patches.

Replace the entire file in one shot:
- **WRITE:** %s`+"\n"+`then a complete, valid Go source body until `+"`---END WRITE---`"+`
- Match the **SPEC Store contract** and **Implement context** signatures exactly (one `+"`List`"+` / `+"`Create`"+` / `+"`Delete`"+` — do not mix alternate names like `+"`AddBookmark`"+` or `+"`GetAllBookmarks`"+`).
- Then run **Verify** before `+"`bd close`"+`.`, p, p))
		}
	}
	return ""
}
