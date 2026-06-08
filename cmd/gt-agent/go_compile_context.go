package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

var goDiagFileRE = regexp.MustCompile(`(?m)(?:^|\s|\])([a-zA-Z0-9_./-]+\.go):\d+`)

func goToolOutputLooksFailed(cmd, output string) bool {
	lower := strings.ToLower(cmd + "\n" + output)
	if !strings.Contains(lower, "go mod") && !strings.Contains(lower, "go build") &&
		!strings.Contains(lower, "go test") && !strings.Contains(lower, "go run") &&
		!strings.Contains(lower, "go vet") && !strings.Contains(lower, "go get") {
		return false
	}
	failMarkers := []string{
		"exit status",
		"cannot find module",
		"invalid import",
		"no required module",
		"build failed",
		"syntax error",
		"undefined:",
		"not used",
		"repository not found",
	}
	for _, m := range failMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	if orchestrator.GoToolOutputMatchedNoPackages(output) && !orchestrator.GoModScaffoldOnlyCommand(cmd) {
		return true
	}
	return strings.Contains(output, ": error:")
}

func extractGoSourcePathsFromOutput(output, layoutRoot string, required []string, mayorRigDir string) []string {
	layout := strings.Trim(strings.TrimSpace(layoutRoot), "/")
	seen := map[string]bool{}
	var paths []string
	add := func(p string) {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if layout != "" {
			p = orchestrator.NormalizeBeadPathForLayout(p, layout)
		}
		if p == "" || !strings.HasSuffix(p, ".go") || seen[p] {
			return
		}
		seen[p] = true
		paths = append(paths, p)
	}
	for _, m := range goDiagFileRE.FindAllStringSubmatch(output, -1) {
		add(m[1])
	}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, " imports") {
			continue
		}
		p := strings.TrimSpace(strings.TrimPrefix(line, "go:"))
		p = strings.TrimSuffix(strings.TrimSpace(p), " imports")
		if strings.HasSuffix(p, ".go") {
			add(p)
			continue
		}
		// e.g. "linkshelf/cmd/server imports" — package dir without .go
		if layout != "" && strings.HasPrefix(p, layout+"/") {
			if strings.HasSuffix(p, "/server") || strings.HasSuffix(p, "/cmd/server") {
				add(p + "/main.go")
			}
		}
	}
	prodPaths := orchestrator.ProductionGoPathsFromRequired(required)
	if len(paths) == 0 && len(prodPaths) > 0 {
		for _, p := range prodPaths {
			add(p)
		}
	}
	// Module/import tidy failures often cite one package; include production sources from profile.
	if strings.Contains(output, "finding module for package") ||
		strings.Contains(output, "cannot find module") {
		for _, p := range prodPaths {
			add(p)
		}
	}
	// Undefined selector failures often cite only the caller; include production sources
	// from required_files so the agent can see whether the referenced API exists.
	if strings.Contains(output, "undefined:") {
		if orchestrator.GoCompileErrorsOnlyInTestFiles(output, layout) {
			for _, p := range orchestrator.GoTestFailureProductionPaths(output, layout) {
				add(p)
			}
		} else {
			for _, p := range prodPaths {
				add(p)
			}
			for _, p := range orchestrator.ProductionPathsFromImportedPackages(mayorRigDir, layout, paths) {
				add(p)
			}
		}
	}
	return paths
}

const (
	maxGoCompileContextFiles = 4
	maxGoCompileContextBytes = 4500
	maxGoCompileSnippetBytes = 1200
)

// appendGoCompileSourceContext adds file snippets to LLM feedback after failed go commands.
// mayorRigDir is {townRoot}/{rig}/mayor/rig (paths in go output are relative to that directory).
func appendGoCompileSourceContext(b *strings.Builder, townRoot, rig, mayorRigDir, layoutRoot, activeBeadPath string, v orchestrator.WorkflowValidation, cmd, cmdOutput string) {
	if !goToolOutputLooksFailed(cmd, cmdOutput) {
		return
	}
	paths := extractGoSourcePathsFromOutput(cmdOutput, layoutRoot, v.RequiredFiles, mayorRigDir)
	paths = orchestrator.CompileErrorPathsIncludingClosedDeps(townRoot, rig, activeBeadPath, paths, cmdOutput, v)
	if len(paths) == 0 {
		return
	}
	b.WriteString("\n### Source context (current .go on disk — fix errors, then re-run verify)\n")
	total := 0
	shown := 0
	for _, rel := range paths {
		if shown >= maxGoCompileContextFiles || total >= maxGoCompileContextBytes {
			break
		}
		abs := rel
		if !filepath.IsAbs(rel) {
			abs = filepath.Join(mayorRigDir, rel)
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		snippet := string(data)
		if len(snippet) > maxGoCompileSnippetBytes {
			snippet = snippet[:maxGoCompileSnippetBytes] + "\n... (truncated)\n"
		}
		block := fmt.Sprintf("\n--- %s ---\n%s\n", rel, snippet)
		if total+len(block) > maxGoCompileContextBytes {
			break
		}
		b.WriteString(block)
		total += len(block)
		shown++
	}
	if shown == 0 {
		b.WriteString("(could not read source files from paths in go output)\n")
	}
	if strings.Contains(cmdOutput, "cannot find module providing package") ||
		strings.Contains(cmdOutput, "invalid import path") {
		b.WriteString("\nHint: fix import paths in the .go files above, then re-run verify.\n")
	}
	if strings.Contains(cmdOutput, "undefined:") {
		b.WriteString("\nHint: an undefined Go symbol means the referenced API is missing or misnamed. Inspect the defining package files above; either add the missing export or change the caller to use an API that exists, then re-run verify.\n")
	}
	if hint := orchestrator.FormatSamePackageTestAPIHint(activeBeadPath, mayorRigDir, cmdOutput, v); hint != "" {
		b.WriteString("\n")
		b.WriteString(hint)
		b.WriteString("\n")
	}
	if hint := orchestrator.FormatClosedDependencyCompileHints(townRoot, rig, activeBeadPath, paths, cmdOutput, v); hint != "" {
		b.WriteString("\n")
		b.WriteString(hint)
		b.WriteString("\n")
	}
	if strings.Contains(cmdOutput, "matched no packages") && strings.Contains(cmd, "...") {
		b.WriteString("\nHint: the go command may include **prose glued after `...`** (e.g. `./internal/store/...We need`). Run only the shell command — no English text on the same line as **CMD:**.\n")
	}
	if hint := orchestrator.FormatGoTestFailureHints(townRoot, rig, activeBeadPath, cmdOutput, paths, v); hint != "" {
		b.WriteString("\n")
		b.WriteString(hint)
		b.WriteString("\n")
	}
	if hint := orchestrator.StorePackageTestIsolationHint(mayorRigDir, layoutRoot, cmdOutput); hint != "" {
		b.WriteString("\n")
		b.WriteString(hint)
		b.WriteString("\n")
	}
	if hint := orchestrator.FormatCorruptedGoFileRecoveryHint(cmdOutput, paths); hint != "" {
		b.WriteString("\n")
		b.WriteString(hint)
		b.WriteString("\n")
	}
	if hint := orchestrator.FormatUnusedImportCompileHint(cmdOutput); hint != "" {
		b.WriteString("\n")
		b.WriteString(hint)
		b.WriteString("\n")
	}
	b.WriteString("\nHint: fix internal packages with **sed -i** or a small **patch**. **cmd/…/main.go** may use `cat > … <<'EOF'` when the file has duplicate handlers or stub bodies; use symbols from **Dependency packages** / Source context, not invented names.\n")
}
