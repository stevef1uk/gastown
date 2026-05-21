package orchestrator

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	maxImplementBeadArchExcerpt = 2500
	maxImplementBeadOnDiskBytes = 1500
)

var archBacktickPathRE = regexp.MustCompile("`([^`]+)`")

// nextOpenImplementBeadHook is set by tests to avoid calling bd list.
var nextOpenImplementBeadHook func(townRoot, rig string, v WorkflowValidation) (*PlanBead, error)

// FormatImplementBeadContextBlock injects architecture/spec/on-disk hints for the next implement bead.
func FormatImplementBeadContextBlock(townRoot, rig string, v WorkflowValidation) string {
	if len(v.RequiredFiles) == 0 {
		return ""
	}
	var next *PlanBead
	var err error
	if nextOpenImplementBeadHook != nil {
		next, err = nextOpenImplementBeadHook(townRoot, rig, v)
	} else {
		next, err = NextOpenImplementBead(townRoot, rig, v)
	}
	if err != nil || next == nil {
		return ""
	}
	beadPath := NormalizeBeadPathForLayout(
		ExtractPathFromBeadTitle(next.Title, v.BeadTitleContains),
		v.LayoutRoot,
	)
	if beadPath == "" {
		return ""
	}
	return formatImplementBeadContextForPath(townRoot, rig, beadPath, v)
}

func formatImplementBeadContextForPath(townRoot, rig, beadPath string, v WorkflowValidation) string {
	beadPath = NormalizeBeadPathForLayout(filepath.ToSlash(strings.TrimSpace(beadPath)), v.LayoutRoot)
	if beadPath == "" {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Implement context for `")
	b.WriteString(beadPath)
	b.WriteString("`\n")
	b.WriteString("Match architecture and profile — do not invent packages, paths, or APIs not described below.\n")

	if excerpt := architectureExcerptForBead(townRoot, rig, beadPath, v); excerpt != "" {
		b.WriteString("\n### From architecture.md\n")
		b.WriteString(excerpt)
		b.WriteString("\n")
	}
	if excerpt := specSummaryExcerptForBead(v.SpecSummary, beadPath, v.LayoutRoot); excerpt != "" {
		b.WriteString("\n### From workflow profile\n")
		b.WriteString(excerpt)
		b.WriteString("\n")
	}
	if snippet := readMayorRigFileSnippet(townRoot, rig, beadPath, maxImplementBeadOnDiskBytes); snippet != "" {
		b.WriteString("\n### Current file on disk\n")
		b.WriteString("```\n")
		b.WriteString(snippet)
		b.WriteString("\n```\n")
	}
	if dep := formatDependencyPackagesContext(townRoot, rig, beadPath, v); dep != "" {
		b.WriteString("\n")
		b.WriteString(dep)
		b.WriteString("\n")
	}
	if block := FormatIncrementalEditBlock(townRoot, rig, beadPath, v); block != "" {
		b.WriteString("\n")
		b.WriteString(block)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

const (
	maxDependencyContextFiles  = 3
	maxDependencyContextBytes  = 2400
	maxDependencySnippetBytes  = 800
)

// formatDependencyPackagesContext injects read-only snippets from earlier implement files (e.g. store for cmd/main).
func formatDependencyPackagesContext(townRoot, rig, activePath string, v WorkflowValidation) string {
	if len(v.RequiredFiles) == 0 {
		return ""
	}
	deps := EarlierRequiredFilesForBead(activePath, v.RequiredFiles)
	if len(deps) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("### Dependency packages (read-only — use these APIs; do not invent symbols)\n")
	b.WriteString("You may **read** these paths with `cat` but must **not** write them while this bead is active (their beads are closed).\n")
	total := 0
	shown := 0
	for _, rel := range deps {
		if shown >= maxDependencyContextFiles || total >= maxDependencyContextBytes {
			break
		}
		if !strings.HasSuffix(strings.ToLower(rel), ".go") {
			continue
		}
		snippet := readMayorRigFileSnippet(townRoot, rig, rel, maxDependencySnippetBytes)
		if snippet == "" {
			continue
		}
		block := "`" + rel + "`:\n```\n" + snippet + "\n```\n"
		if total+len(block) > maxDependencyContextBytes {
			break
		}
		b.WriteString(block)
		total += len(block)
		shown++
	}
	if shown == 0 {
		return ""
	}
	if IsCmdMainImplementPath(activePath) {
		b.WriteString("\n**cmd/main bead:** wire routes only — call handlers in `internal/api` (or architecture path); do not re-implement handler bodies in main.go.\n")
	}
	b.WriteString("\nCall **only** functions/types shown in the snippets above (e.g. `AddLink`, `GetAllLinks`, `DeleteLink`) — do not invent `CreateLink`, `ErrNotFound`, or other symbols.\n")
	return strings.TrimSpace(b.String())
}

func architectureExcerptForBead(townRoot, rig, beadPath string, v WorkflowValidation) string {
	archPath := filepath.Join(townRoot, rig, "mayor", "rig", "architecture.md")
	data, err := os.ReadFile(archPath)
	if err != nil {
		return ""
	}
	return excerptLinesForPath(string(data), beadPath, v.LayoutRoot, maxImplementBeadArchExcerpt)
}

func specSummaryExcerptForBead(specSummary, beadPath, layoutRoot string) string {
	specSummary = strings.TrimSpace(specSummary)
	if specSummary == "" {
		return ""
	}
	excerpt := excerptLinesForPath(specSummary, beadPath, layoutRoot, 1200)
	if excerpt != "" {
		return excerpt
	}
	// Fall back to first chunk when the path is only named once in a long paragraph.
	if strings.Contains(specSummary, beadPath) || strings.Contains(specSummary, filepath.Base(beadPath)) {
		if len(specSummary) > 800 {
			return specSummary[:800] + "…"
		}
		return specSummary
	}
	return ""
}

func excerptLinesForPath(doc, beadPath, layoutRoot string, maxBytes int) string {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	layoutRoot = strings.Trim(strings.TrimSpace(layoutRoot), "/")
	rel := beadPath
	if layoutRoot != "" && strings.HasPrefix(beadPath, layoutRoot+"/") {
		rel = strings.TrimPrefix(beadPath, layoutRoot+"/")
	}
	keys := dedupeStrings([]string{beadPath, rel, filepath.Base(beadPath)})

	lines := strings.Split(doc, "\n")
	var picked []int
	for i, line := range lines {
		lower := strings.ToLower(line)
		for _, k := range keys {
			if k == "" {
				continue
			}
			if strings.Contains(lower, strings.ToLower(k)) {
				picked = append(picked, i)
				break
			}
		}
	}
	if len(picked) == 0 {
		for _, m := range archBacktickPathRE.FindAllStringSubmatch(doc, -1) {
			p := filepath.ToSlash(m[1])
			for _, k := range keys {
				if k != "" && (p == k || strings.HasSuffix(p, "/"+k) || filepath.Base(p) == filepath.Base(k)) {
					for i, line := range lines {
						if strings.Contains(line, m[0]) {
							picked = append(picked, i)
						}
					}
				}
			}
		}
	}
	if len(picked) == 0 {
		return ""
	}

	seen := map[int]bool{}
	var out []string
	total := 0
	appendLine := func(j int) bool {
		if j < 0 || j >= len(lines) || seen[j] {
			return true
		}
		line := lines[j]
		if total+len(line)+1 > maxBytes {
			return false
		}
		seen[j] = true
		out = append(out, line)
		total += len(line) + 1
		return true
	}
	for _, i := range picked {
		if i > 0 && strings.HasPrefix(strings.TrimSpace(lines[i-1]), "#") {
			if !appendLine(i - 1) {
				return strings.Join(out, "\n")
			}
		}
		if !appendLine(i) {
			return strings.Join(out, "\n")
		}
		for j := i + 1; j < len(lines) && j <= i+3; j++ {
			if strings.TrimSpace(lines[j]) == "" {
				break
			}
			if !appendLine(j) {
				return strings.Join(out, "\n")
			}
		}
	}
	return strings.Join(out, "\n")
}

func readMayorRigFileSnippet(townRoot, rig, relPath string, maxBytes int) string {
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	if relPath == "" {
		return ""
	}
	abs := filepath.Join(townRoot, rig, "mayor", "rig", relPath)
	data, err := os.ReadFile(abs)
	if err != nil {
		return ""
	}
	s := string(data)
	if len(s) > maxBytes {
		return s[:maxBytes] + "\n... (truncated)\n"
	}
	return s
}
