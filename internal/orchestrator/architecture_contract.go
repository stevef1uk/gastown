package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const maxArchitectureContractBytes = 2800

var (
	goExportedFuncRE     = regexp.MustCompile(`(?m)^func\s+([A-Z][A-Za-z0-9_]*)\s*\(`)
	goExportedTypeRE     = regexp.MustCompile(`(?m)^type\s+([A-Z][A-Za-z0-9_]*)\s+(?:struct|interface)`)
	goExportedMethodRE   = regexp.MustCompile(`(?m)^func\s+\([^)]*\*?([A-Z][A-Za-z0-9_]*)\)\s+([A-Z][A-Za-z0-9_]*)\s*\(`)
	goInlineSnippetRE    = regexp.MustCompile("`([^`]*(?:\\bfunc\\s+|\\btype\\s+)[^`]*)`")
	goCodeFenceRE        = regexp.MustCompile("(?s)```(?:go|golang)?\\s*\\n(.*?)```")
	archTestNameRE       = regexp.MustCompile(`\b(Test[A-Za-z0-9_]+)\b`)
)

// FormatArchitectureContractForBead injects deterministic contract text from SPEC, architecture, and plan.
func FormatArchitectureContractForBead(townRoot, rig, beadPath string, v WorkflowValidation) string {
	beadPath = NormalizeBeadPathForLayout(filepath.ToSlash(strings.TrimSpace(beadPath)), v.LayoutRoot)
	if beadPath == "" {
		return ""
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	specDoc := readRigDoc(rigDir, "SPEC.md")
	archDoc := readRigDoc(rigDir, "architecture.md")
	planDoc := readRigDoc(rigDir, "plan.md")
	if specDoc == "" && archDoc == "" && planDoc == "" {
		return ""
	}

	var b strings.Builder
	b.WriteString("### Architecture contract (from SPEC / architecture / plan)\n")
	b.WriteString("Implement **only** what these sources describe for this bead — do not invent alternate package APIs.\n")

	if block := formatHTTPContractForBead(specDoc, archDoc, planDoc, beadPath, v); block != "" {
		b.WriteString("\n")
		b.WriteString(block)
	}
	if block := formatSpecSectionsForBead(specDoc, beadPath); block != "" {
		b.WriteString("\n")
		b.WriteString(block)
	}
	if names := extractTestNamesForBead(archDoc, planDoc, beadPath, v.LayoutRoot); len(names) > 0 {
		b.WriteString("\n**Tests named in architecture/plan:** ")
		b.WriteString(strings.Join(names, ", "))
		b.WriteString("\n")
	}
	if syms := extractContractSymbolsFromDocs(specDoc, archDoc, planDoc, beadPath, v.LayoutRoot); len(syms) > 0 {
		b.WriteString("\n**Exported names from design docs (use these):** ")
		b.WriteString(strings.Join(syms, ", "))
		b.WriteString("\n")
	}

	out := strings.TrimSpace(b.String())
	if out == "### Architecture contract (from SPEC / architecture / plan)\nImplement **only** what these sources describe for this bead — do not invent alternate package APIs." {
		return ""
	}
	return truncateCodeindexText(out, maxArchitectureContractBytes)
}

func readRigDoc(rigDir, name string) string {
	data, err := os.ReadFile(filepath.Join(rigDir, name))
	if err != nil {
		return ""
	}
	return string(data)
}

func formatHTTPContractForBead(specDoc, archDoc, planDoc, beadPath string, v WorkflowValidation) string {
	if !IsHTTPRoutingGuidanceBead(beadPath) && !IsHTTPHandlerImplementPath(beadPath) {
		return ""
	}
	merged := specDoc + "\n" + archDoc + "\n" + planDoc
	api := parseAPISmokeSpecText(merged, v)
	if len(api.Probes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("**HTTP endpoints (from SPEC/architecture — same as runtime smoke curls):**\n")
	for _, p := range api.Probes {
		if p.Source == "static" {
			continue
		}
		b.WriteString("- `")
		b.WriteString(strings.ToUpper(strings.TrimSpace(p.Method)))
		b.WriteString(" ")
		b.WriteString(normalizeSmokePath(p.Path))
		b.WriteString("`")
		if p.Expect == SmokeExpectEmptyJSONArray {
			b.WriteString(" (must return `[]` on fresh server)")
		}
		b.WriteString("\n")
	}
	if len(api.StaticAssets) > 0 {
		b.WriteString("**Static assets (from web/):** ")
		b.WriteString(strings.Join(api.StaticAssets, ", "))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func formatSpecSectionsForBead(specDoc, beadPath string) string {
	if strings.TrimSpace(specDoc) == "" {
		return ""
	}
	var parts []string
	switch {
	case IsSQLiteSchemaBeadPath(beadPath):
		if s := ExtractSpecMarkdownSection(specDoc, "Data model"); s != "" {
			parts = append(parts, "**Data model (SPEC):**\n```\n"+truncateCodeindexText(s, 900)+"\n```")
		}
	case IsStorePackageBeadPath(beadPath) && !IsSQLiteSchemaBeadPath(beadPath):
		if s := ExtractSpecMarkdownSection(specDoc, "Store"); s != "" {
			parts = append(parts, "**Store API (SPEC):**\n```\n"+truncateCodeindexText(s, 900)+"\n```")
		}
		if s := ExtractSpecMarkdownSection(specDoc, "Data model"); s != "" && !IsSQLiteSchemaBeadPath(beadPath) {
			parts = append(parts, "**Data model (SPEC):**\n```\n"+truncateCodeindexText(s, 600)+"\n```")
		}
	case IsHTTPHandlerImplementPath(beadPath) || IsHTTPHandlerTestPath(beadPath):
		for _, heading := range []string{"HTTP", "API", "Routes", "Endpoints"} {
			if s := ExtractSpecMarkdownSection(specDoc, heading); s != "" {
				parts = append(parts, "**"+heading+" (SPEC):**\n```\n"+truncateCodeindexText(s, 900)+"\n```")
				break
			}
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n")
}

func extractTestNamesForBead(archDoc, planDoc, beadPath, layoutRoot string) []string {
	keys := beadContextKeys(beadPath, layoutRoot)
	seen := map[string]bool{}
	var out []string
	for _, doc := range []string{archDoc, planDoc} {
		for _, line := range strings.Split(doc, "\n") {
			lower := strings.ToLower(line)
			if !lineMatchesBeadKeys(lower, keys) {
				continue
			}
			for _, m := range archTestNameRE.FindAllStringSubmatch(line, -1) {
				if len(m) >= 2 && !seen[m[1]] {
					seen[m[1]] = true
					out = append(out, m[1])
				}
			}
		}
	}
	sort.Strings(out)
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}

func extractContractSymbolsFromDocs(specDoc, archDoc, planDoc, beadPath, layoutRoot string) []string {
	keys := beadContextKeys(beadPath, layoutRoot)
	seen := map[string]bool{}
	var out []string
	for _, doc := range []string{specDoc, archDoc, planDoc} {
		if !docRelevantToBead(doc, keys) {
			continue
		}
		for _, block := range goCodeFenceRE.FindAllStringSubmatch(doc, -1) {
			if len(block) < 2 {
				continue
			}
			for _, sym := range extractContractSymbolsFromGoSource(block[1]) {
				if !seen[sym] {
					seen[sym] = true
					out = append(out, sym)
				}
			}
		}
		for _, line := range strings.Split(doc, "\n") {
			if !lineMatchesBeadKeys(strings.ToLower(line), keys) {
				continue
			}
			for _, m := range goInlineSnippetRE.FindAllStringSubmatch(line, -1) {
				if len(m) < 2 {
					continue
				}
				for _, sym := range extractContractSymbolsFromGoSource(m[1]) {
					if !seen[sym] {
						seen[sym] = true
						out = append(out, sym)
					}
				}
			}
		}
	}
	sort.Strings(out)
	if len(out) > 20 {
		out = out[:20]
	}
	return out
}

// extractContractSymbolsFromGoSource collects exported names from SPEC/architecture Go snippets,
// including receiver methods (func (s *Store) List) and inline backtick fragments.
func extractContractSymbolsFromGoSource(goSrc string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" || !astIsExportedName(name) || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}
	for _, sym := range extractGoExportedSymbols(goSrc) {
		add(sym)
	}
	for _, m := range goExportedMethodRE.FindAllStringSubmatch(goSrc, -1) {
		if len(m) >= 3 {
			add(m[1]) // receiver type, e.g. Store
			add(m[2]) // method name, e.g. List
		}
	}
	return out
}

func extractGoExportedSymbols(goSrc string) []string {
	seen := map[string]bool{}
	var out []string
	for _, re := range []*regexp.Regexp{goExportedFuncRE, goExportedTypeRE} {
		for _, m := range re.FindAllStringSubmatch(goSrc, -1) {
			if len(m) >= 2 && !seen[m[1]] {
				seen[m[1]] = true
				out = append(out, m[1])
			}
		}
	}
	return out
}

func docRelevantToBead(doc string, keys []string) bool {
	lower := strings.ToLower(doc)
	return lineMatchesBeadKeys(lower, keys)
}

func beadContextKeys(beadPath, layoutRoot string) []string {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	layoutRoot = strings.Trim(strings.TrimSpace(layoutRoot), "/")
	rel := beadPath
	if layoutRoot != "" && strings.HasPrefix(beadPath, layoutRoot+"/") {
		rel = strings.TrimPrefix(beadPath, layoutRoot+"/")
	}
	return dedupeStrings([]string{beadPath, rel, filepath.Base(beadPath), strings.TrimSuffix(filepath.Base(beadPath), ".go")})
}

func lineMatchesBeadKeys(lowerLine string, keys []string) bool {
	for _, k := range keys {
		if k == "" {
			continue
		}
		if strings.Contains(lowerLine, strings.ToLower(k)) {
			return true
		}
	}
	return false
}

// ArchitectureContractSymbolNames returns exported names parsed from SPEC/architecture/plan for this bead.
func ArchitectureContractSymbolNames(mayorRigDir, beadPath string, v WorkflowValidation) []string {
	beadPath = NormalizeBeadPathForLayout(filepath.ToSlash(strings.TrimSpace(beadPath)), v.LayoutRoot)
	if beadPath == "" {
		return nil
	}
	return extractContractSymbolsFromDocs(
		readRigDoc(mayorRigDir, "SPEC.md"),
		readRigDoc(mayorRigDir, "architecture.md"),
		readRigDoc(mayorRigDir, "plan.md"),
		beadPath,
		v.LayoutRoot,
	)
}

func symbolNameSet(names []string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, n := range names {
		if n != "" {
			out[n] = true
		}
	}
	return out
}

func sortedSymbolDiff(have, allowed map[string]bool) []string {
	var out []string
	for n := range have {
		if !allowed[n] {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// FormatCodeindexSymbolAlignmentSection reports drift between codeindex and architecture/SPEC symbols.
func FormatCodeindexSymbolAlignmentSection(mayorRigDir, beadPath string, v WorkflowValidation, indexPath string, indexCandidates []string) string {
	if !WorkflowUsesGo(v) || !strings.HasSuffix(beadPath, ".go") {
		return ""
	}
	archSyms := ArchitectureContractSymbolNames(mayorRigDir, beadPath, v)
	indexSyms := CodeindexActivePackageSymbolNames(indexPath, indexCandidates)
	if len(archSyms) == 0 && len(indexSyms) == 0 {
		return ""
	}
	archSet := symbolNameSet(archSyms)
	indexSet := symbolNameSet(indexSyms)

	if len(indexSyms) == 0 || len(archSyms) == 0 {
		return ""
	}
	var b strings.Builder
	indexOnly := sortedSymbolDiff(indexSet, archSet)
	archOnly := sortedSymbolDiff(archSet, indexSet)
	if len(indexOnly) == 0 && len(archOnly) == 0 {
		return ""
	}
	b.WriteString("### Symbol alignment (codeindex vs architecture)\n")
	b.WriteString("Resolve these before inventing new exported names — implementation must match **both** when both are present.\n")
	if len(indexOnly) > 0 {
		b.WriteString("\n**In codeindex but not in architecture/SPEC:** ")
		b.WriteString(strings.Join(indexOnly, ", "))
		b.WriteString(" — update code to match design docs, or refresh the index if the drift is intentional.\n")
	}
	if len(archOnly) > 0 {
		beadFile := filepath.Join(mayorRigDir, filepath.FromSlash(beadPath))
		if _, err := os.Stat(beadFile); err == nil {
			b.WriteString("\n**In architecture/SPEC but missing from codeindex:** ")
			b.WriteString(strings.Join(archOnly, ", "))
			b.WriteString(" — run `refresh_codeindex` / implement missing symbols, then reconcile.\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// BuildImplementSymbolAllowlist returns exported names the polecat may define or call for this bead.
func BuildImplementSymbolAllowlist(mayorRigDir, beadPath string, v WorkflowValidation) map[string]bool {
	allowed := AllowedCodeindexSymbolNames(mayorRigDir, beadPath, v)
	if allowed == nil {
		allowed = map[string]bool{}
	}
	beadPath = NormalizeBeadPathForLayout(filepath.ToSlash(strings.TrimSpace(beadPath)), v.LayoutRoot)
	if beadPath == "" {
		return allowed
	}
	for _, sym := range ArchitectureContractSymbolNames(mayorRigDir, beadPath, v) {
		allowed[sym] = true
	}
	return allowed
}

// ValidateImplementExportedSymbols rejects new exported funcs/types not in the design allowlist.
func ValidateImplementExportedSymbols(mayorRigDir, relPath, content string, v WorkflowValidation) error {
	if !WorkflowUsesGo(v) || strings.HasSuffix(relPath, "_test.go") {
		return nil
	}
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	if IsSQLiteSchemaBeadPath(relPath) {
		return nil
	}
	content = strings.TrimSpace(content)
	if relPath == "" || content == "" {
		return nil
	}
	allowed := BuildImplementSymbolAllowlist(mayorRigDir, relPath, v)
	if len(allowed) == 0 {
		return nil
	}
	var issues []string
	for _, sym := range extractGoExportedSymbols(content) {
		if allowed[sym] {
			continue
		}
		issues = append(issues, sym)
	}
	if len(issues) == 0 {
		return nil
	}
	sort.Strings(issues)
	return fmt.Errorf("%s: exported name(s) not in architecture/SPEC/codeindex allowlist: %s — use names from **Architecture contract** and **Codeindex symbols** only",
		relPath, strings.Join(issues, ", "))
}
