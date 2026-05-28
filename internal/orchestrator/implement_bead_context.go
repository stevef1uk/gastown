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
	b.WriteString("Match architecture, **plan.md** acceptance, and profile — do not invent packages, paths, or APIs not described below.\n")

	if block := FormatArchitectureContractForBead(townRoot, rig, beadPath, v); block != "" {
		b.WriteString("\n")
		b.WriteString(block)
		b.WriteString("\n")
	}
	if block := FormatPackageBeadOwnershipBlock(filepath.Join(townRoot, rig, "mayor", "rig"), beadPath, v); block != "" {
		b.WriteString("\n")
		b.WriteString(block)
		b.WriteString("\n")
	}
	if plan := PlanExcerptForBead(townRoot, rig, beadPath); plan != "" {
		b.WriteString("\n### From plan.md (acceptance for this bead)\n")
		b.WriteString(plan)
		b.WriteString("\n")
	}
	if checklist := FormatPlanAcceptanceChecklist(townRoot, rig, beadPath, v); checklist != "" {
		b.WriteString("\n")
		b.WriteString(checklist)
		b.WriteString("\n")
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	if block := FormatMainDependencyExportsBlock(rigDir, beadPath, v); block != "" {
		b.WriteString("\n")
		b.WriteString(block)
		b.WriteString("\n")
	}
	if block := FormatMainWiringContextForBead(townRoot, rig, beadPath, v); block != "" {
		b.WriteString("\n")
		b.WriteString(block)
		b.WriteString("\n")
	}
	if block := FormatCodeindexContextForBead(filepath.Join(townRoot, rig, "mayor", "rig"), beadPath, v); block != "" {
		b.WriteString("\n")
		b.WriteString(block)
		b.WriteString("\n")
	}
	if WorkflowUsesGo(v) {
		if modCtx := formatGoModuleImportContext(filepath.Join(townRoot, rig, "mayor", "rig"), v); modCtx != "" {
			b.WriteString("\n")
			b.WriteString(modCtx)
			b.WriteString("\n")
		}
	}
	if note := formatUnitTestGuidanceForBead(townRoot, rig, beadPath, v); note != "" {
		b.WriteString("\n")
		b.WriteString(note)
		b.WriteString("\n")
	}
	if note := FormatHTTPHandlerPrerequisiteBlock(townRoot, rig, beadPath, v); note != "" {
		b.WriteString("\n")
		b.WriteString(note)
		b.WriteString("\n")
	}
	if note := FormatHTTPRoutingGuidanceForBead(townRoot, rig, beadPath, v); note != "" {
		b.WriteString("\n")
		b.WriteString(note)
		b.WriteString("\n")
	}
	if block := FormatHandlerExportsForMainBlock(rigDir, beadPath, v); block != "" {
		b.WriteString("\n")
		b.WriteString(block)
		b.WriteString("\n")
	}
	if block := FormatSpecSchemaContractBlock(townRoot, rig, beadPath); block != "" {
		b.WriteString("\n")
		b.WriteString(block)
		b.WriteString("\n")
	} else if block := FormatSpecStoreContractBlock(townRoot, rig, beadPath, v); block != "" {
		b.WriteString("\n")
		b.WriteString(block)
		b.WriteString("\n")
	}
	if block := FormatStoreTestBeadChecklist(townRoot, rig, beadPath); block != "" {
		b.WriteString("\n")
		b.WriteString(block)
		b.WriteString("\n")
	}

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
	if block := FormatImplementBeadCompileFailureBlock(rigDir, beadPath, v); block != "" {
		b.WriteString("\n")
		b.WriteString(block)
		b.WriteString("\n")
	}
	if block := FormatNilSliceListUnblockHint(townRoot, rig, beadPath, v); block != "" {
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
	deps := orderedDependencyGoFilesForContext(activePath, v)
	if len(deps) == 0 {
		return ""
	}
	maxFiles := maxDependencyContextFiles
	if IsCmdMainImplementPath(activePath) {
		maxFiles = 4
	}
	var b strings.Builder
	b.WriteString("### Dependency packages (read-only — use these APIs; do not invent symbols)\n")
	b.WriteString("You may **read** these paths with `cat` but must **not** write them while this bead is active (their beads are closed).\n")
	total := 0
	shown := 0
	for _, rel := range deps {
		if shown >= maxFiles || total >= maxDependencyContextBytes {
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
		b.WriteString("\n**cmd/main bead:** implement `registerAPI` / `serveStaticFiles` in package `main` per **Main wiring** and `main_test.go`; do not re-implement handler bodies from scratch.\n")
	}
	b.WriteString("\nCall **only** functions/types and signatures from the snippets above, **Architecture contract**, and **SPEC.md**.\n")
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

func formatUnitTestGuidanceForBead(townRoot, rig, beadPath string, v WorkflowValidation) string {
	if IsTestImplementPath(beadPath) {
		return strings.TrimSpace("### Unit tests (this bead)\n" +
			"Implement or extend tests that prove **SPEC.md / plan.md acceptance** for this path — not smoke curls.\n" +
			"Use table-driven or case-per-requirement tests; run **Verify** (go test / pytest -v) before bd close.")
	}
	if WorkflowUsesGo(v) && strings.HasSuffix(beadPath, ".go") && !IsServerMainImplementBead(beadPath) && !strings.HasSuffix(beadPath, "go.mod") {
		testPath := CorrelatedTestPathForSource(beadPath, v.LayoutRoot)
		if testPath == "" {
			return ""
		}
		if TestPathListedInRequired(beadPath, v.RequiredFiles, v.LayoutRoot) && !mayorRigTestFileExists(townRoot, rig, testPath) {
			return strings.TrimSpace("### Unit tests (separate bead)\n" +
				"This bead is **production code only** (`" + beadPath + "`). **Verify** runs `go build` for this package.\n" +
				"Do **not** `cat` `" + testPath + "` — it does not exist until the **`" + testPath + "` implement bead**.\n" +
				"Implement tests on that later bead (table-driven cases from SPEC/plan acceptance).")
		}
		msg := "### Unit tests (required with this code)\nBefore `bd close`, `" + testPath + "` must exist and **Verify** (`go test -count=1`) must pass.\n"
		msg += "Create tests with **WRITE:** or **EDIT:** in this session — do not fail because the file is missing; write it.\n"
		if strings.Contains(filepath.ToSlash(beadPath), "internal/store/store.go") {
			msg += "SQLite store tests must use a **fresh DB per test** (`:memory:` or `filepath.Join(t.TempDir(), \"test.db\")` passed into `OpenDB`) — never reuse `./links.db` from prior runs.\n"
		}
		return strings.TrimSpace(msg)
	}
	if WorkflowUsesPython(v) && strings.HasSuffix(beadPath, ".py") && !IsTestImplementPath(beadPath) {
		testPath := CorrelatedTestPathForSource(beadPath, v.LayoutRoot)
		if testPath == "" {
			return ""
		}
		return strings.TrimSpace("### Unit tests (required with this code)\n" +
			"Add or update `" + testPath + "` with pytest cases mapped to SPEC/plan acceptance for `" + beadPath + "`.\n" +
			"Verify runs pytest on that file when it exists on disk.")
	}
	return ""
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

func formatGoModuleImportContext(rigDir string, v WorkflowValidation) string {
	goModPath := filepath.Join(rigDir, v.LayoutRoot, "go.mod")
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "module ") {
			modName := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "module "))
			return "### Go Module Context\nThe Go module name for this project is `" + modName + "`. When importing internal packages (like `store` or `api`), use `" + modName + "/internal/...` rather than the physical folder path or absolute test rig paths.\n"
		}
	}
	return ""
}
