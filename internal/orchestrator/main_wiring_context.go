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

// FormatMainWiringContextForBead injects cmd/server/main.go integration guidance from on-disk
// handlers.go, store.go, and main_test.go (not a fixed API shape).
func FormatMainWiringContextForBead(townRoot, rig, beadPath string, v WorkflowValidation) string {
	if !IsCmdMainImplementPath(beadPath) || !WorkflowUsesGo(v) {
		return ""
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")

	var b strings.Builder
	b.WriteString("### Main wiring (server entrypoint — read before EDIT/WRITE)\n")
	b.WriteString("This bead **wires** packages implemented in earlier beads. Match **Dependency exports**, **Dependency packages**, and **HTTP routing** — do not invent symbols, packages, or URL paths not shown on disk or in SPEC.\n\n")

	b.WriteString("**Package `main` helpers:**\n")
	b.WriteString("- Implement helpers named in `main_test.go` / architecture (e.g. route registration, static file serving).\n")

	handlersPath := firstRequiredPathSuffix(v, "/internal/api/handlers.go")
	var handlersSrc string
	if handlersPath != "" {
		if data, err := os.ReadFile(filepath.Join(rigDir, filepath.FromSlash(handlersPath))); err == nil {
			handlersSrc = string(data)
		}
	}
	storePath := storeRelPathForMain(v)
	var storeSrc string
	if storePath != "" {
		if data, err := os.ReadFile(filepath.Join(rigDir, filepath.FromSlash(storePath))); err == nil {
			storeSrc = string(data)
		}
	}
	handlerMode := detectHandlerWiringMode(handlersSrc)
	storeMode := detectStoreWiringMode(storeSrc)

	mainTestPath := firstRequiredPathSuffix(v, "cmd/server/main_test.go")
	if mainTestPath == "" && layout != "" {
		mainTestPath = layout + "/cmd/server/main_test.go"
	}
	regSig := ""
	if mainTestPath != "" {
		regSig = registerAPISignatureFromMainTest(readMayorRigFileSnippet(townRoot, rig, mainTestPath, maxMainTestSnippetBytes))
	}
	if regSig != "" {
		b.WriteString("- Tests expect: `")
		b.WriteString(regSig)
		b.WriteString("`\n")
	} else {
		b.WriteString("- `registerAPI(mux *http.ServeMux, …)` — signature must match how handlers/store are wired (see below).\n")
	}

	storeLabel := "dependency store package"
	if storePath != "" {
		storeLabel = "`" + storePath + "`"
	}
	b.WriteString("\n**Store dependency ")
	b.WriteString(storeLabel)
	b.WriteString(":**\n")
	b.WriteString("- Open DB and run schema/init helpers per **Dependency packages** / architecture.\n")
	switch storeMode {
	case storeWiringInstance:
		b.WriteString("- **On disk:** exported instance type + constructor — create instance and pass to handlers per **Dependency exports**.\n")
	case storeWiringPackageFuncs:
		b.WriteString("- **On disk:** package-level functions — wire handlers as implemented; no instance constructor.\n")
	default:
		b.WriteString("- Read **Dependency exports** — if an exported name is missing, reopen that bead and add it per architecture.\n")
	}

	handlerLabel := "handler dependency"
	if handlersPath != "" {
		handlerLabel = "`" + handlersPath + "`"
	}
	b.WriteString("\n**Handler dependency ")
	b.WriteString(handlerLabel)
	b.WriteString(":**\n")
	switch handlerMode {
	case handlerWiringFactoryFuncs:
		b.WriteString("- **On disk:** exported handler factory funcs (see **Dependency exports**) — register **SPEC HTTP paths** only; no invented URL shapes.\n")
	case handlerWiringRegisterHandlers:
		b.WriteString("- **On disk:** same-package route registration helper — expose via entrypoint helpers in package `main` per architecture/tests.\n")
	default:
		b.WriteString("- Read handlers snippet — wire only exported entrypoints listed under **Dependency exports**.\n")
	}

	if handlersPath != "" {
		abs := filepath.Join(rigDir, filepath.FromSlash(handlersPath))
		if wiring := formatHandlersWiringFromSource(abs); wiring != "" {
			b.WriteString("\n**From `handlers.go`:**\n")
			b.WriteString(wiring)
			b.WriteString("\n")
		}
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
		if name != "registerHandlers" && !strings.HasPrefix(name, "handle") && name[0] >= 'A' && name[0] <= 'Z' {
			lines = append(lines, "- `"+name+m[2]+"` — exported entrypoint; register on paths from **HTTP routing** / SPEC.")
			continue
		}
		if name != "registerHandlers" && !strings.HasPrefix(name, "handle") {
			continue
		}
		prefix := "production"
		if name != "registerHandlers" && strings.HasPrefix(name, "handle") {
			prefix = "internal"
		}
		if name == "registerHandlers" {
			lines = append(lines, "- `"+name+m[2]+"` — same-package helper; expose via `registerAPI` in package `main` or export `RegisterHandlers`.")
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
