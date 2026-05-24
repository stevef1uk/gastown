package orchestrator

import (
	"fmt"
	"strings"
)

// FormatMainDependencyExportsBlock lists exported symbols on disk from store/api deps
// so main.go wiring matches what earlier beads actually implemented.
func FormatMainDependencyExportsBlock(rigDir, beadPath string, v WorkflowValidation) string {
	if !IsCmdMainImplementPath(beadPath) || !WorkflowUsesGo(v) {
		return ""
	}
	var parts []string
	for _, suffix := range []string{"/internal/store/store.go", "/internal/api/handlers.go"} {
		rel := firstRequiredPathSuffix(v, suffix)
		if rel == "" {
			continue
		}
		sym := readExportedGoSymbolsFromRig(rigDir, rel)
		if len(sym.Types) == 0 && len(sym.Funcs) == 0 {
			parts = append(parts, fmt.Sprintf("- `%s`: *(not on disk yet or no exports — finish that bead first)*", rel))
			continue
		}
		var names []string
		names = append(names, sym.Types...)
		names = append(names, sym.Funcs...)
		parts = append(parts, fmt.Sprintf("- `%s`: %s", rel, strings.Join(names, ", ")))
	}
	if len(parts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("### Dependency exports (on disk — wire entrypoint to these)\n")
	b.WriteString("Call **only** exported names listed below. If a name the entrypoint needs is missing, **reopen** that bead (`bd update <id> --status=open`) and export it per architecture — do not invent symbols or paths.\n")
	for _, p := range parts {
		b.WriteString(p)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}
