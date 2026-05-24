package orchestrator

import (
	"strings"
)

// FormatPackageBeadOwnershipBlock documents same-package implement bead symbol ownership.
func FormatPackageBeadOwnershipBlock(mayorRigDir, beadPath string, v WorkflowValidation) string {
	if !WorkflowUsesGo(v) || !strings.HasSuffix(beadPath, ".go") {
		return ""
	}
	pkg := GoBuildRelPackage(v.LayoutRoot, beadPath)
	if pkg == "" {
		return ""
	}
	earlier := earlierSamePackageFiles(beadPath, v)
	if len(earlier) == 0 && !IsSQLiteSchemaBeadPath(beadPath) {
		return ""
	}
	var b strings.Builder
	b.WriteString("### Go package bead ownership (`")
	b.WriteString(pkg)
	b.WriteString("`)\n")
	b.WriteString("Multiple implement beads may share one Go package directory. Each bead owns **different symbols** — do not redefine types or funcs from earlier files.\n")
	if IsSQLiteSchemaBeadPath(beadPath) {
		b.WriteString("- **This bead (schema/migrate):** DDL (`CREATE TABLE`), migrations, shared domain **types**, schema helpers from architecture/SPEC.\n")
		b.WriteString("- **Later beads in this package:** persistence API (`Store`, CRUD methods) — do not implement them here.\n")
	} else if len(earlier) > 0 {
		b.WriteString("- **Earlier files in this package (already implemented):**\n")
		for _, sib := range earlier {
			sym := readExportedGoSymbolsFromRig(mayorRigDir, sib)
			b.WriteString("  - `")
			b.WriteString(sib)
			b.WriteString("`")
			if len(sym.Types) > 0 || len(sym.Funcs) > 0 {
				b.WriteString(": ")
				if len(sym.Types) > 0 {
					b.WriteString("types ")
					b.WriteString(strings.Join(sym.Types, ", "))
				}
				if len(sym.Funcs) > 0 {
					if len(sym.Types) > 0 {
						b.WriteString("; ")
					}
					b.WriteString("funcs ")
					b.WriteString(strings.Join(sym.Funcs, ", "))
				}
			}
			b.WriteString("\n")
		}
		b.WriteString("- **This file:** add only new symbols for this bead. **Do not** repeat `type X struct` for types listed above — use them from the same package.\n")
	}
	return strings.TrimSpace(b.String())
}
