package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
	return strings.Contains(output, ": error:")
}

func extractGoSourcePathsFromOutput(output, layoutRoot string) []string {
	layout := strings.Trim(strings.TrimSpace(layoutRoot), "/")
	seen := map[string]bool{}
	var paths []string
	add := func(p string) {
		p = filepath.ToSlash(strings.TrimSpace(p))
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
	if len(paths) == 0 && layout != "" {
		for _, p := range requiredGoPathsUnderLayout(layout) {
			add(p)
		}
	}
	// Module/import tidy failures often cite one package; include sibling sources for context.
	if layout != "" && (strings.Contains(output, "finding module for package") ||
		strings.Contains(output, "cannot find module")) {
		for _, p := range requiredGoPathsUnderLayout(layout) {
			add(p)
		}
	}
	return paths
}

func requiredGoPathsUnderLayout(layout string) []string {
	return []string{
		layout + "/cmd/server/main.go",
		layout + "/internal/store/store.go",
		layout + "/internal/api/handlers.go",
	}
}

const (
	maxGoCompileContextFiles = 4
	maxGoCompileContextBytes = 4500
	maxGoCompileSnippetBytes = 1200
)

// appendGoCompileSourceContext adds file snippets to LLM feedback after failed go commands.
// mayorRigDir is {townRoot}/{rig}/mayor/rig (paths in go output are relative to that directory).
func appendGoCompileSourceContext(b *strings.Builder, mayorRigDir, layoutRoot, cmd, cmdOutput string) {
	if !goToolOutputLooksFailed(cmd, cmdOutput) {
		return
	}
	paths := extractGoSourcePathsFromOutput(cmdOutput, layoutRoot)
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
}
