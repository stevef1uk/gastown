package orchestrator

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	maxMainWiringSnippetBytes = 1200
	maxMainTestSnippetBytes   = 600
)

var goFuncDeclLineRE = regexp.MustCompile(`(?m)^func\s+([A-Za-z_][A-Za-z0-9_]*)\s*(\([^)]*\))`)

// FormatMainWiringContextForBead injects cmd/server/main.go integration guidance: real handler
// wiring from handlers.go, main_test helpers, and store package-level API (not Store/LinkStore).
func FormatMainWiringContextForBead(townRoot, rig, beadPath string, v WorkflowValidation) string {
	if !IsCmdMainImplementPath(beadPath) || !WorkflowUsesGo(v) {
		return ""
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")

	var b strings.Builder
	b.WriteString("### Main wiring (cmd/server — read before EDIT/WRITE)\n")
	b.WriteString("This bead **wires** existing packages. Do **not** invent `LinkStore`, `HandleListLinks`, `SetDB`, or other APIs not shown below.\n\n")

	b.WriteString("**`main_test.go` requires these functions in package `main`:**\n")
	b.WriteString("- `registerAPI(mux *http.ServeMux)` — register API routes (tests call this, not `api.HandleListLinks`).\n")
	b.WriteString("- `serveStaticFiles(mux *http.ServeMux)` — serve `web/` (see architecture / handlers snippet for paths).\n\n")

	b.WriteString("**Store (package `internal/store`):**\n")
	b.WriteString("- Call `store.InitSchema(db)` on the `*sql.DB` you open in `main`.\n")
	b.WriteString("- Handlers use package-level `store.List`, `store.Create`, `store.Delete` (see store.go snippet).\n")
	b.WriteString("- Plan text may say \"Store instance\" — the implemented API is **package functions**, not `NewStore` / `LinkStore`.\n")
	b.WriteString("- If tests use a file DB but `store.List` still hits an in-memory default, wire the opened DB in **this** bead (e.g. exported `SetDB` in store) only when store.go has no setter and handlers cannot see your DB.\n\n")

	handlersPath := firstRequiredPathSuffix(v, "/internal/api/handlers.go")
	if handlersPath != "" {
		abs := filepath.Join(rigDir, filepath.FromSlash(handlersPath))
		if wiring := formatHandlersWiringFromSource(abs); wiring != "" {
			b.WriteString("**From `handlers.go` (wire via `registerAPI` in main — `registerHandlers` is same-package only):**\n")
			b.WriteString(wiring)
			b.WriteString("\n")
		}
	}

	mainTestPath := firstRequiredPathSuffix(v, "cmd/server/main_test.go")
	if mainTestPath == "" && layout != "" {
		mainTestPath = layout + "/cmd/server/main_test.go"
	}
	if mainTestPath != "" {
		snip := readMayorRigFileSnippet(townRoot, rig, mainTestPath, maxMainTestSnippetBytes)
		if snip != "" {
			b.WriteString("**`main_test.go` excerpt (helpers under test):**\n```go\n")
			b.WriteString(truncateCodeindexText(snip, maxMainTestSnippetBytes))
			b.WriteString("\n```\n")
		}
	}

	b.WriteString("\n**Verify:** `cd ")
	if layout != "" {
		b.WriteString(layout)
	} else {
		b.WriteString(".")
	}
	b.WriteString(" && go test -count=1 ./cmd/server/...` before `bd close`.\n")

	return strings.TrimSpace(b.String())
}

func firstRequiredPathSuffix(v WorkflowValidation, suffix string) string {
	suffix = filepath.ToSlash(suffix)
	for _, p := range v.RequiredFiles {
		if strings.HasSuffix(filepath.ToSlash(p), suffix) {
			return filepath.ToSlash(p)
		}
	}
	return ""
}

func formatHandlersWiringFromSource(absPath string) string {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return ""
	}
	src := string(data)
	var lines []string
	for _, m := range goFuncDeclLineRE.FindAllStringSubmatch(src, -1) {
		if len(m) < 3 {
			continue
		}
		name := m[1]
		if name != "registerHandlers" && !strings.HasPrefix(name, "handle") {
			continue
		}
		prefix := "production"
		if name != "registerHandlers" && strings.HasPrefix(name, "handle") {
			prefix = "internal"
		}
		if name == "registerHandlers" {
			lines = append(lines, "- `"+name+m[2]+"` — **unexported**; expose via `registerAPI` in package `main` (duplicate route table or thin wrapper calling into api after exporting `RegisterHandlers`).")
			continue
		}
		lines = append(lines, "- `"+name+m[2]+"` ("+prefix+", used inside api — do not re-export from main)")
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

// orderedDependencyGoFilesForContext returns dependency paths for snippet injection.
// For cmd/server/main.go, handlers.go and main_test.go are prioritized over *_test.go in store/api.
func orderedDependencyGoFilesForContext(activePath string, v WorkflowValidation) []string {
	deps := EarlierRequiredFilesForBead(activePath, v.RequiredFiles)
	if !IsCmdMainImplementPath(activePath) {
		return deps
	}
	prioritySuffixes := []string{
		"/internal/api/handlers.go",
		"/cmd/server/main_test.go",
		"/internal/store/schema.go",
		"/internal/store/store.go",
	}
	seen := map[string]bool{}
	var ordered []string
	appendUnique := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		ordered = append(ordered, p)
	}
	for _, suffix := range prioritySuffixes {
		for _, d := range deps {
			if strings.HasSuffix(filepath.ToSlash(d), suffix) {
				appendUnique(d)
			}
		}
	}
	for _, d := range deps {
		lower := strings.ToLower(d)
		if strings.HasSuffix(lower, "_test.go") {
			// Skip other packages' test files; keep cmd/server/main_test.go only.
			if !strings.Contains(lower, "cmd/server/main_test.go") {
				continue
			}
		}
		appendUnique(d)
	}
	return ordered
}
