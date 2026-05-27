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
	out := strings.TrimSpace(b.String())
	handlersRel := firstRequiredPathSuffix(v, "/internal/api/handlers.go")
	if handlersRel == "" {
		return out
	}
	sym := readExportedGoSymbolsFromRig(rigDir, handlersRel)
	if len(sym.Funcs) > 0 || len(sym.Types) > 0 {
		return out
	}
	var extra strings.Builder
	extra.WriteString("\n**Handlers (`")
	extra.WriteString(handlersRel)
	extra.WriteString("`) export nothing yet.** Do not wire `api.serveIndex` or other unexported names from `main` — ")
	extra.WriteString("reopen the handlers implement bead and add `RegisterHandlers(mux *http.ServeMux)` (or exported handlers) first.\n")
	extra.WriteString("**Verify:** `cd ")
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout != "" {
		extra.WriteString(layout)
	} else {
		extra.WriteString(".")
	}
	extra.WriteString(" && go build ./cmd/server/...` before `bd close` on this bead.\n")
	return out + extra.String()
}
