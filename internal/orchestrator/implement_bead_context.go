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
	if plan := PlanExcerptForBead(townRoot, rig, beadPath, v); plan != "" {
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
		rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
		if strings.HasSuffix(filepath.ToSlash(beadPath), "/go.mod") || beadPath == "go.mod" {
			if block := FormatGoModBeadContext(rigDir, v); block != "" {
				b.WriteString("\n")
				b.WriteString(block)
				b.WriteString("\n")
			}
		}
		if modCtx := formatGoModuleImportContext(rigDir, v); modCtx != "" {
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
	if block := FormatFrontendBeadChecklist(rigDir, beadPath, v); block != "" {
		b.WriteString("\n")
		b.WriteString(block)
		b.WriteString("\n")
	}
	if block := FormatDockerfileBeadContext(rigDir, beadPath, v); block != "" {
		b.WriteString("\n")
		b.WriteString(block)
		b.WriteString("\n")
	}
	if block := FormatStoreTestBeadChecklist(townRoot, rig, beadPath); block != "" {
		b.WriteString("\n")
		b.WriteString(block)
		b.WriteString("\n")
	}
	if block := FormatE2ETestBeadChecklist(rigDir, beadPath, v); block != "" {
		b.WriteString("\n")
		b.WriteString(block)
		b.WriteString("\n")
	}

	if excerpt := architectureExcerptForBead(townRoot, rig, beadPath, v); excerpt != "" {
		b.WriteString("\n### From architecture.md\n")
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
		testPath := CorrelatedTestPathForSource(beadPath, v)
		if testPath == "" {
			return ""
		}
		if TestPathListedInRequired(beadPath, v) && !mayorRigTestFileExists(townRoot, rig, testPath) {
			return strings.TrimSpace("### Unit tests (separate bead)\n" +
				"This bead is **production code only** (`" + beadPath + "`). **Verify** runs `go build` for this package.\n" +
				"Do **not** `cat` `" + testPath + "` — it does not exist until the **`" + testPath + "` implement bead**.\n" +
				"Implement tests on that later bead (table-driven cases from SPEC/plan acceptance).")
		}
		mayorDir := filepath.Join(townRoot, rig, "mayor", "rig")
		verifyKind := "go test -count=1"
		if CanonicalImplementationVerifyIsGoBuildOnly(v, mayorDir, beadPath) {
			verifyKind = "go build"
		}
		msg := "### Unit tests (required with this code)\nBefore `bd close`, `" + testPath + "` must exist"
		if verifyKind == "go build" {
			msg += ".\nWhile other beads' `*_test.go` files exist in this package, **Verify** uses **`go build`** on production code — do not run full **`go test`** (it recompiles foreign tests and clears session verify).\n"
		} else {
			msg += " and **Verify** (`go test -count=1`) must pass.\n"
		}
		msg += "Create tests with **WRITE:** or **EDIT:** in this session — do not fail because the file is missing; write it.\n"
		if strings.Contains(filepath.ToSlash(beadPath), "internal/store/store.go") {
			msg += "SQLite store tests must use a **fresh DB per test** (`:memory:` or `filepath.Join(t.TempDir(), \"test.db\")` passed into `OpenDB`) — never reuse `./links.db` from prior runs.\n"
		}
		return strings.TrimSpace(msg)
	}
	if WorkflowUsesPython(v) && strings.HasSuffix(beadPath, ".py") && !IsTestImplementPath(beadPath) {
		testPath := CorrelatedTestPathForSource(beadPath, v)
		if testPath == "" {
			return ""
		}
		return strings.TrimSpace("### Unit tests (required with this code)\n" +
			"Add or update `" + testPath + "` with pytest cases mapped to SPEC/plan acceptance for `" + beadPath + "`.\n" +
			"Verify runs pytest on that file when it exists on disk.")
	}
	if IsTestImplementPath(beadPath) && (strings.HasSuffix(beadPath, ".test.ts") || strings.HasSuffix(beadPath, ".test.tsx") ||
		strings.HasSuffix(beadPath, ".spec.ts") || strings.HasSuffix(beadPath, ".spec.tsx")) {
		return strings.TrimSpace("### Unit tests (this bead)\n" +
			"Write Vitest tests using `@testing-library/react` for component tests.\n" +
			"Test rendering, user interactions, state changes, and error states per SPEC.md.\n" +
			"No trivial placeholder tests — each test must exercise real behavior from the spec.\n" +
			"Run **Verify** (`npx vitest run`) before bd close.")
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

// FormatFrontendBeadChecklist returns concrete implementation requirements for HTML/JS/CSS
// beads extracted from SPEC.md and architecture.md. The LLM must check off each item before
// closing the bead — this prevents generic/placeholder frontend code.
func FormatFrontendBeadChecklist(rigDir, beadPath string, v WorkflowValidation) string {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	if !IsFrontendImplementPath(beadPath) {
		return ""
	}
	specData, err := os.ReadFile(filepath.Join(rigDir, "SPEC.md"))
	if err != nil {
		return ""
	}
	archData, err := os.ReadFile(filepath.Join(rigDir, "architecture.md"))
	if err != nil {
		archData = nil
	}
	specDoc := string(specData)
	archDoc := ""
	if archData != nil {
		archDoc = string(archData)
	}

	var b strings.Builder
	b.WriteString("### Frontend implementation checklist\n")

	if strings.HasSuffix(strings.ToLower(beadPath), ".html") {
		// Extract form fields from the SPEC's POST body JSON example.
		fields := extractSpecFormFields(specDoc)
		if len(fields) > 0 {
			b.WriteString("**Required form inputs (from SPEC POST body):**\n")
			for _, f := range fields {
				b.WriteString("- `<input>` for `" + f + "` (id must match app.js)\n")
			}
		}
		// Extract HTML IDs from architecture or existing app.js.
		jsIDs := extractJSReferencedIDs(rigDir, beadPath)
		if len(jsIDs) > 0 {
			b.WriteString("\n**DOM IDs used by app.js (must match exactly):**\n")
			for _, id := range jsIDs {
				b.WriteString("- `" + id + "`\n")
			}
		}
		// Extract static asset references from architecture HTTP table.
		staticRefs := extractArchStaticRefs(archDoc)
		if len(staticRefs) > 0 {
			b.WriteString("\n**Static asset references (paths from architecture HTTP table):**\n")
			for _, ref := range staticRefs {
				b.WriteString("- `" + ref + "`\n")
			}
		}
		b.WriteString("\n**Rules:**\n")
		b.WriteString("- Use EXACT IDs from the list above — do not invent names like `links-list` vs `links`\n")
		b.WriteString("- Every POST body field in SPEC needs a corresponding input element\n")
		b.WriteString("- Static references use paths from architecture.md only — not `/app.js` or guessed paths\n")
		b.WriteString("- Read the existing app.js and style.css in the same directory to align IDs and classes\n")
		b.WriteString("- Write the COMPLETE file — no `<!-- your HTML here -->` placeholders\n")
	}

	if strings.HasSuffix(strings.ToLower(beadPath), ".js") {
		endpoints := extractSpecAPIEndpoints(specDoc)
		if len(endpoints) > 0 {
			b.WriteString("**API endpoints to call (from SPEC HTTP table):**\n")
			for _, ep := range endpoints {
				b.WriteString("- " + ep + "\n")
			}
		}
		b.WriteString("\n**Rules:**\n")
		b.WriteString("- Use EXACT DOM IDs matching index.html\n")
		b.WriteString("- URL paths must match SPEC HTTP table exactly\n")
		b.WriteString("- Include error handling for every fetch call\n")
	}

	if strings.HasSuffix(strings.ToLower(beadPath), ".css") {
		b.WriteString("Provide minimal, functional CSS. At minimum style the form, list, and container elements referenced in index.html.\n")
		b.WriteString("Match class names and IDs used in index.html — do not invent selectors.\n")
	}
	return strings.TrimSpace(b.String())
}

// extractSpecFormFields returns field names from SPEC POST body JSON examples like {"title":"...","url":"..."}.
func extractSpecFormFields(specDoc string) []string {
	re := regexp.MustCompile(`"(\w+)"\s*:\s*"`)
	seen := map[string]bool{}
	var fields []string
	for _, m := range re.FindAllStringSubmatch(specDoc, -1) {
		if len(m) >= 2 {
			f := strings.TrimSpace(m[1])
			if f == "error" || seen[f] {
				continue
			}
			seen[f] = true
			fields = append(fields, f)
		}
	}
	return fields
}

// extractJSReferencedIDs parses the existing app.js (if present) for DOM element IDs.
func extractJSReferencedIDs(rigDir, htmlPath string) []string {
	dir := filepath.Dir(filepath.Join(rigDir, filepath.FromSlash(htmlPath)))
	jsPath := filepath.Join(dir, "app.js")
	data, err := os.ReadFile(jsPath)
	if err != nil {
		return nil
	}
	// Match getElementById('id'), querySelector('#id'), or dataset references.
	re := regexp.MustCompile(`(?:getElementById|querySelector)\s*\(\s*['"]([^'"]+)['"]`)
	seen := map[string]bool{}
	var ids []string
	for _, m := range re.FindAllStringSubmatch(string(data), -1) {
		if len(m) >= 2 {
			id := strings.TrimSpace(m[1])
			if id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// extractArchStaticRefs parses architecture.md for /static/{file} style references.
func extractArchStaticRefs(archDoc string) []string {
	re := regexp.MustCompile(`/static/\{?(\w+)`)
	seen := map[string]bool{}
	var refs []string
	for _, m := range re.FindAllStringSubmatch(archDoc, -1) {
		if len(m) >= 2 {
			r := "/static/" + strings.TrimRight(m[1], "}")
			if !seen[r] {
				seen[r] = true
				refs = append(refs, r)
			}
		}
	}
	return refs
}

// extractSpecAPIEndpoints parses the SPEC HTTP table for API endpoint paths.
func extractSpecAPIEndpoints(specDoc string) []string {
	re := regexp.MustCompile(`\| (GET|POST|DELETE) \| ([^\|]+) \|`)
	seen := map[string]bool{}
	var eps []string
	for _, m := range re.FindAllStringSubmatch(specDoc, -1) {
		if len(m) >= 3 {
			method := strings.TrimSpace(m[1])
			path := strings.TrimSpace(m[2])
			if path == "/" || path == "/static/{file}" {
				continue
			}
			key := method + " " + path
			if !seen[key] {
				seen[key] = true
				eps = append(eps, key)
			}
		}
	}
	return eps
}
