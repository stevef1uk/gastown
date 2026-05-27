package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
)

const goBuildCmdServerSuffix = "go build ./cmd/server/..."

// ShouldVerifyCmdServerAfterHandlerEdit reports whether verify must compile cmd/server after handler work.
func ShouldVerifyCmdServerAfterHandlerEdit(mayorRigDir, beadPath string, v WorkflowValidation) bool {
	if !WorkflowUsesGo(v) || !IsHTTPHandlerImplementPath(beadPath) {
		return false
	}
	return GoServerMainExists(mayorRigDir, v)
}

// AppendGoBuildCmdServerToVerify chains cmd/server compile after package-scoped handler verify.
func AppendGoBuildCmdServerToVerify(baseCmd, mayorRigDir, beadPath string, v WorkflowValidation) string {
	baseCmd = strings.TrimSpace(baseCmd)
	if baseCmd == "" || !ShouldVerifyCmdServerAfterHandlerEdit(mayorRigDir, beadPath, v) {
		return baseCmd
	}
	if strings.Contains(baseCmd, goBuildCmdServerSuffix) {
		return baseCmd
	}
	return baseCmd + " && " + goBuildCmdServerSuffix
}

// FormatHandlerExportsForMainBlock warns handler-bead agents what package main may import.
func FormatHandlerExportsForMainBlock(rigDir, beadPath string, v WorkflowValidation) string {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	if !IsHTTPHandlerImplementPath(beadPath) || !WorkflowUsesGo(v) {
		return ""
	}
	if !GoServerMainExists(rigDir, v) {
		return ""
	}
	mainPath := firstRequiredPathSuffix(v, "/cmd/server/main.go")
	if mainPath == "" {
		layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
		if layout != "" {
			mainPath = layout + "/cmd/server/main.go"
		}
	}
	sym := readExportedGoSymbolsFromRig(rigDir, beadPath)
	var names []string
	names = append(names, sym.Types...)
	names = append(names, sym.Funcs...)
	var b strings.Builder
	b.WriteString("### Exports for `cmd/server/main.go` (required before `bd close`)\n")
	if len(names) == 0 {
		b.WriteString("**None on disk yet.** Tests in this package may call unexported helpers (`serveIndex`, etc.) — ")
		b.WriteString("package `main` **cannot** import those names (e.g. `api.serveIndex` will not compile).\n\n")
		b.WriteString("Before closing this bead, export a registrar **`main` can call**, typically:\n")
		b.WriteString("- `func RegisterHandlers(mux *http.ServeMux)` — register `/`, `/static/`, `/api/links` per architecture\n")
		b.WriteString("or export handler funcs with **capitalized** names and register them from `")
		if mainPath != "" {
			b.WriteString(mainPath)
		} else {
			b.WriteString("cmd/server/main.go")
		}
		b.WriteString("`.\n\n")
		b.WriteString("**Verify** (after edits) must include `go build ./cmd/server/...` — gt-agent runs it when ")
		b.WriteString(mainPath)
		b.WriteString(" exists.\n")
		return strings.TrimSpace(b.String())
	}
	b.WriteString("Package `main` may import **only** these exported names from this file:\n")
	for _, n := range names {
		b.WriteString("- `")
		b.WriteString(n)
		b.WriteString("`\n")
	}
	b.WriteString("\nDo **not** reference unexported `serve*` helpers from `main`. ")
	b.WriteString("**Verify:** package tests **and** `go build ./cmd/server/...`.\n")
	if mainPath != "" && fileReferencesHandlerPackage(rigDir, mainPath) {
		b.WriteString("\n**Note:** read `")
		b.WriteString(mainPath)
		b.WriteString("` on disk — wire imports to match the list above.\n")
	}
	return strings.TrimSpace(b.String())
}

func fileReferencesHandlerPackage(rigDir, mainRel string) bool {
	data, err := os.ReadFile(filepath.Join(rigDir, filepath.FromSlash(mainRel)))
	if err != nil {
		return false
	}
	s := string(data)
	return strings.Contains(s, "api.") || strings.Contains(s, "internal/api")
}
