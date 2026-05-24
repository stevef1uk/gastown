package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var codeindexSymbolLineRE = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s+\((function|struct|interface|method|type)\)`)

const (
	codeindexImpactMaxBytes  = 2200
	codeindexSymbolsMaxBytes = 2400
	codeindexDepSymbolsMax   = 1200
	codeindexMaxDepSymbolPkgs = 2
	codeindexIndexName       = "codeindex.json"
)

// CodeindexEnabled reports whether optional codeindex integration is on (binary in PATH, not disabled).
func CodeindexEnabled() bool {
	if strings.TrimSpace(os.Getenv("GT_CODEINDEX")) == "0" || strings.TrimSpace(os.Getenv("CODEINDEX")) == "0" {
		return false
	}
	_, err := exec.LookPath("codeindex")
	return err == nil
}

// CodeindexIndexPath is where gastown stores the dependency index for a rig worktree.
func CodeindexIndexPath(mayorRigDir string) string {
	return filepath.Join(mayorRigDir, codeindexIndexName)
}

// codeindexAnalyzeRoot returns the subtree passed to `codeindex analyze` (usually layout_root).
func codeindexAnalyzeRoot(mayorRigDir string, v WorkflowValidation) string {
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	if layout == "" || layout == "." {
		return mayorRigDir
	}
	return filepath.Join(mayorRigDir, layout)
}

// RefreshCodeindexIndex builds codeindex.json + inline symbols when missing or stale.
func RefreshCodeindexIndex(mayorRigDir string, v WorkflowValidation) (log string, err error) {
	if !CodeindexEnabled() {
		return "", nil
	}
	mayorRigDir = strings.TrimSpace(mayorRigDir)
	if mayorRigDir == "" {
		return "", nil
	}
	analyzeRoot := codeindexAnalyzeRoot(mayorRigDir, v)
	if _, err := os.Stat(analyzeRoot); err != nil {
		return "", nil
	}
	indexPath := CodeindexIndexPath(mayorRigDir)
	if !codeindexIndexNeedsRefresh(indexPath, analyzeRoot) {
		return "", nil
	}
	analyze := exec.Command("codeindex", "analyze", analyzeRoot, "--output", indexPath)
	analyze.Dir = mayorRigDir
	if out, runErr := analyze.CombinedOutput(); runErr != nil {
		return "", fmt.Errorf("codeindex analyze: %w: %s", runErr, strings.TrimSpace(string(out)))
	}
	symbols := exec.Command("codeindex", "symbols", analyzeRoot, "--inline", "--index", indexPath)
	symbols.Dir = mayorRigDir
	if out, runErr := symbols.CombinedOutput(); runErr != nil {
		return "codeindex analyze ok; symbols failed: " + strings.TrimSpace(string(out)), runErr
	}
	return "codeindex: built " + codeindexIndexName + " (analyze + inline symbols)", nil
}

func codeindexIndexNeedsRefresh(indexPath, analyzeRoot string) bool {
	info, err := os.Stat(indexPath)
	if err != nil {
		return true
	}
	newest, err := newestSourceMtime(analyzeRoot)
	if err != nil || newest.IsZero() {
		return false
	}
	return info.ModTime().Before(newest)
}

func newestSourceMtime(root string) (time.Time, error) {
	var newest time.Time
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			base := info.Name()
			if base == ".git" || base == "node_modules" || base == ".venv" || base == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".rs", ".rb", ".java", ".php", ".vue":
		default:
			return nil
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	return newest, err
}

// codeindexLayoutRelativePath strips layout_root from a bead path for the codeindex CLI.
func codeindexLayoutRelativePath(beadPath string, v WorkflowValidation) string {
	impactPath := filepath.ToSlash(strings.TrimSpace(beadPath))
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	if layout != "" && strings.HasPrefix(impactPath, layout+"/") {
		impactPath = strings.TrimPrefix(impactPath, layout+"/")
	}
	return impactPath
}

// codeindexImpactCandidates returns paths to try with `codeindex impact`.
// Go rigs index packages (internal/store), not individual .go files — package path first.
func codeindexImpactCandidates(beadPath string, v WorkflowValidation) []string {
	rel := codeindexLayoutRelativePath(beadPath, v)
	if rel == "" {
		return nil
	}
	var candidates []string
	if WorkflowUsesGo(v) && strings.HasSuffix(rel, ".go") {
		if pkg := GoBuildRelPackage(v.LayoutRoot, beadPath); pkg != "" {
			candidates = append(candidates, pkg)
		}
	}
	candidates = append(candidates, rel)
	seen := map[string]bool{}
	var out []string
	for _, c := range candidates {
		if c != "" && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

func runCodeindexImpact(mayorRigDir, indexPath, impactPath string) (string, error) {
	cmd := exec.Command("codeindex", "impact", impactPath, "--index", indexPath)
	cmd.Dir = mayorRigDir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

type codeindexInlineNode struct {
	ID      string                   `json:"id"`
	Symbols []map[string]interface{} `json:"symbols"`
}

// codeindexSymbolsCLIRepoPath is the repo directory argument for manual `codeindex symbols` (must exist on disk).
func codeindexSymbolsCLIRepoPath(v WorkflowValidation, layoutRel string) string {
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	rel := filepath.ToSlash(strings.TrimSpace(layoutRel))
	if layout != "" && rel != "" && !strings.HasPrefix(rel, layout+"/") {
		return filepath.ToSlash(filepath.Join(layout, rel))
	}
	return rel
}

// fetchCodeindexSymbols reads inline symbols from codeindex.json (populated by refresh_codeindex --inline).
func fetchCodeindexSymbols(indexPath string, paths []string) string {
	return extractInlineSymbolsFromCodeindex(indexPath, paths)
}

func extractInlineSymbolsFromCodeindex(indexPath string, paths []string) string {
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return ""
	}
	var doc struct {
		Nodes []codeindexInlineNode `json:"nodes"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return ""
	}
	seen := map[string]bool{}
	var lines []string
	for _, want := range paths {
		want = filepath.ToSlash(strings.TrimSpace(want))
		if want == "" {
			continue
		}
		for _, node := range doc.Nodes {
			nid := filepath.ToSlash(strings.TrimSpace(node.ID))
			if nid != want && !strings.HasPrefix(want, nid+"/") && !strings.HasPrefix(nid, want+"/") {
				continue
			}
			for _, sym := range node.Symbols {
				line := formatCodeindexSymbolLine(sym)
				if line == "" || seen[line] {
					continue
				}
				seen[line] = true
				lines = append(lines, line)
			}
		}
	}
	return strings.Join(lines, "\n")
}

// isCodeindexTestSymbol reports symbols that belong in *_test.go (not callable from production/main).
func isCodeindexTestSymbol(name, file string) bool {
	file = filepath.ToSlash(strings.TrimSpace(file))
	if strings.HasSuffix(file, "_test.go") {
		return true
	}
	// Go test function naming convention.
	if strings.HasPrefix(name, "Test") && len(name) > 4 {
		r := name[4]
		if r >= 'A' && r <= 'Z' {
			return true
		}
	}
	return false
}

func formatCodeindexSymbolLine(sym map[string]interface{}) string {
	name, _ := sym["name"].(string)
	if name == "" {
		return ""
	}
	exported, ok := sym["exported"].(bool)
	if ok && !exported {
		return ""
	}
	kind, _ := sym["kind"].(string)
	file, _ := sym["file"].(string)
	lineNum, _ := sym["line"].(float64)
	var b strings.Builder
	if isCodeindexTestSymbol(name, file) {
		b.WriteString("[test only] ")
	}
	b.WriteString(name)
	if kind != "" {
		b.WriteString(" (")
		b.WriteString(kind)
		b.WriteString(")")
	}
	if file != "" {
		b.WriteString(" — ")
		b.WriteString(file)
		if lineNum > 0 {
			b.WriteString(fmt.Sprintf(":%.0f", lineNum))
		}
	}
	return b.String()
}

func formatCodeindexSymbolsSection(label, path, body string, maxBytes int) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	prod, test := partitionCodeindexSymbolLines(body)
	var b strings.Builder
	b.WriteString("### Codeindex symbols")
	if label != "" {
		b.WriteString(" (")
		b.WriteString(label)
		b.WriteString(")")
	}
	b.WriteString("\n")
	b.WriteString("Lines prefixed **`[test only]`** are for `*_test.go` — **do not** call them from `main.go` or production handlers.\n")
	b.WriteString("Use **production** lines (no prefix) plus **Main wiring** / **Architecture contract** / dependency snippets.\n")
	if path != "" {
		b.WriteString("Path: `")
		b.WriteString(path)
		b.WriteString("`\n\n")
	}
	if prod != "" {
		b.WriteString("**Production symbols:**\n```\n")
		b.WriteString(truncateCodeindexText(prod, maxBytes))
		b.WriteString("\n```\n")
	}
	if test != "" {
		if prod != "" {
			b.WriteString("\n")
		}
		b.WriteString("**Test-only symbols** (not for main.go):\n```\n")
		// Reserve budget for production block when both present.
		testMax := maxBytes / 2
		if prod == "" {
			testMax = maxBytes
		}
		b.WriteString(truncateCodeindexText(test, testMax))
		b.WriteString("\n```\n")
	}
	if prod == "" && test != "" {
		b.WriteString("\n(No exported production symbols in this package — use **Main wiring** and dependency file snippets.)\n")
	}
	return b.String()
}

func partitionCodeindexSymbolLines(body string) (production, testOnly string) {
	var prodLines, testLines []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[test only]") {
			testLines = append(testLines, line)
		} else {
			prodLines = append(prodLines, line)
		}
	}
	return strings.Join(prodLines, "\n"), strings.Join(testLines, "\n")
}

// codeindexDependencySymbolPaths returns package paths for closed dependency .go beads (Go rigs).
func codeindexDependencySymbolPaths(beadPath string, v WorkflowValidation) []string {
	if !WorkflowUsesGo(v) || !strings.HasSuffix(beadPath, ".go") || IsTestImplementPath(beadPath) {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, dep := range EarlierRequiredFilesForBead(beadPath, v.RequiredFiles) {
		if len(out) >= codeindexMaxDepSymbolPkgs {
			break
		}
		if !strings.HasSuffix(strings.ToLower(dep), ".go") || strings.HasSuffix(dep, "_test.go") {
			continue
		}
		pkg := GoBuildRelPackage(v.LayoutRoot, dep)
		if pkg == "" || seen[pkg] {
			continue
		}
		seen[pkg] = true
		out = append(out, pkg)
	}
	return out
}

// CodeindexPolecatCMDExamples returns example shell lines (from mayor/rig) for symbols/impact on the active bead.
func CodeindexPolecatCMDExamples(beadPath string, v WorkflowValidation) []string {
	candidates := codeindexImpactCandidates(beadPath, v)
	if len(candidates) == 0 {
		return nil
	}
	target := codeindexSymbolsCLIRepoPath(v, candidates[0])
	return []string{
		"codeindex symbols " + target + " --index codeindex.json",
		"codeindex impact " + candidates[0] + " --index codeindex.json",
	}
}

// FormatCodeindexContextForBead injects blast-radius impact and polecat CMD hints for the active implement path.
func FormatCodeindexContextForBead(mayorRigDir, beadPath string, v WorkflowValidation) string {
	if !CodeindexEnabled() {
		return ""
	}
	beadPath = NormalizeBeadPathForLayout(filepath.ToSlash(strings.TrimSpace(beadPath)), v.LayoutRoot)
	if beadPath == "" {
		return ""
	}
	mayorRigDir = strings.TrimSpace(mayorRigDir)
	indexPath := CodeindexIndexPath(mayorRigDir)
	if _, err := os.Stat(indexPath); err != nil {
		return ""
	}
	candidates := codeindexImpactCandidates(beadPath, v)
	var b strings.Builder
	// Auto-inject symbol tables (polecats rarely run codeindex CMD voluntarily).
	for _, depPkg := range codeindexDependencySymbolPaths(beadPath, v) {
		if sym := fetchCodeindexSymbols(indexPath, []string{depPkg}); sym != "" {
			b.WriteString(formatCodeindexSymbolsSection("dependency "+depPkg, depPkg, sym, codeindexDepSymbolsMax))
		}
	}
	activeLabel := "active bead"
	if len(candidates) > 0 {
		activeLabel = "active package " + candidates[0]
	}
	indexSym := fetchCodeindexSymbols(indexPath, candidates)
	archNames := ArchitectureContractSymbolNames(mayorRigDir, beadPath, v)

	if indexSym != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(formatCodeindexSymbolsSection(activeLabel, candidates[0], indexSym, codeindexSymbolsMaxBytes))
	} else if len(archNames) > 0 && len(candidates) > 0 && WorkflowUsesGo(v) && strings.HasSuffix(beadPath, ".go") {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(formatSymbolNamesAsCodeindexSection("architecture / SPEC (default)", candidates[0], archNames))
	} else if len(candidates) > 0 && WorkflowUsesGo(v) && strings.HasSuffix(beadPath, ".go") {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(formatCodeindexEmptyPackageSection(mayorRigDir, candidates[0], indexPath, beadPath, v))
	}
	if align := FormatCodeindexSymbolAlignmentSection(mayorRigDir, beadPath, v, indexPath, candidates); align != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(align)
	}
	var text string
	var failText string
	for _, impactPath := range candidates {
		out, err := runCodeindexImpact(mayorRigDir, indexPath, impactPath)
		if err == nil && out != "" {
			text = out
			break
		}
		if out != "" {
			failText = out
		}
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	if text != "" {
		b.WriteString("### Codeindex blast radius (optional — read before large EDITs)\n")
		b.WriteString("Install: `pip install codeindex`. gt-agent refreshes `codeindex.json` at task start.\n\n")
		b.WriteString("```\n")
		b.WriteString(truncateCodeindexText(text, codeindexImpactMaxBytes))
		b.WriteString("\n```\n")
	} else if failText != "" {
		b.WriteString("### Codeindex (impact lookup failed)\n")
		b.WriteString(truncateCodeindexText(failText, 800))
		b.WriteString("\n")
	}
	if strings.TrimSpace(b.String()) == "" {
		appendCodeindexPolecatCMDHint(&b, beadPath, v)
		if strings.TrimSpace(b.String()) == "" {
			return ""
		}
		return strings.TrimSpace(b.String())
	}
	if text != "" {
		if WorkflowUsesGo(v) && strings.HasSuffix(beadPath, ".go") {
			b.WriteString("\nImpact shows who imports this **package**. Match **Codeindex symbols** and **Dependency packages** before EDIT/WRITE.\n")
		} else {
			b.WriteString("\nMatch **Codeindex symbols** and **Dependency packages** before EDIT/WRITE.\n")
		}
	} else if b.Len() > 0 {
		b.WriteString("\nMatch **Codeindex symbols** and **Dependency packages** before EDIT/WRITE.\n")
	}
	appendCodeindexPolecatCMDHint(&b, beadPath, v)
	return strings.TrimSpace(b.String())
}

func appendCodeindexPolecatCMDHint(b *strings.Builder, beadPath string, v WorkflowValidation) {
	cmds := CodeindexPolecatCMDExamples(beadPath, v)
	if len(cmds) == 0 {
		return
	}
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	if layout == "" {
		layout = "."
	}
	b.WriteString("\n**Optional CMD refresh** (symbols are already injected above; use only if index is stale):\n")
	for _, c := range cmds {
		b.WriteString("- `CMD: " + c + "` — ")
		if strings.HasPrefix(c, "codeindex symbols") {
			b.WriteString("exported funcs/types in scope (use real names, do not invent).\n")
		} else {
			b.WriteString("who imports this path/package (blast radius).\n")
		}
	}
	b.WriteString("- Refresh stale index: `CMD: codeindex analyze " + layout + " --output codeindex.json && codeindex symbols " + layout + " --inline --index codeindex.json`\n")
}

func truncateCodeindexText(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "\n…"
}

// CodeindexActivePackageSymbolNames returns exported symbol names from the index for the active package path(s).
func CodeindexActivePackageSymbolNames(indexPath string, candidates []string) []string {
	symText := fetchCodeindexSymbols(indexPath, candidates)
	if symText == "" {
		return nil
	}
	return symbolNamesFromCodeindexLines(strings.Split(symText, "\n"))
}

func symbolNamesFromCodeindexLines(lines []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if m := codeindexSymbolLineRE.FindStringSubmatch(line); len(m) >= 2 && !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	sort.Strings(out)
	return out
}

func formatSymbolNamesAsCodeindexSection(label, path string, names []string) string {
	if len(names) == 0 {
		return ""
	}
	var lines []string
	for _, n := range names {
		lines = append(lines, n+" (from architecture/SPEC)")
	}
	body := strings.Join(lines, "\n")
	return formatCodeindexSymbolsSection(label, path, body, codeindexSymbolsMaxBytes)
}

func formatCodeindexEmptyPackageSection(mayorRigDir, pkg, indexPath, beadPath string, v WorkflowValidation) string {
	pkg = filepath.ToSlash(strings.TrimSpace(pkg))
	if pkg == "" {
		return ""
	}
	archNames := ArchitectureContractSymbolNames(mayorRigDir, beadPath, v)
	if len(archNames) > 0 {
		return formatSymbolNamesAsCodeindexSection("architecture / SPEC (default)", pkg, archNames)
	}
	var b strings.Builder
	b.WriteString("### Codeindex symbols (active package ")
	b.WriteString(pkg)
	b.WriteString(" — new/empty)\n")
	b.WriteString("No exported symbols indexed yet and none parsed from architecture/SPEC for this bead. Follow **Architecture contract**, **plan.md**, and **dependency** symbols above.\n")
	var depNames []string
	for _, depPkg := range codeindexDependencySymbolPaths(beadPath, v) {
		for _, n := range CodeindexActivePackageSymbolNames(indexPath, []string{depPkg}) {
			depNames = append(depNames, n)
		}
	}
	if len(depNames) > 0 {
		b.WriteString("Dependency exports to use: ")
		b.WriteString(strings.Join(depNames, ", "))
		b.WriteString(".\n")
	}
	return b.String()
}

// FormatCodeindexSymbolsReminderForBead returns a compact symbol-only block for turn N feedback (no impact/CMD hints).
func FormatCodeindexSymbolsReminderForBead(mayorRigDir, beadPath string, v WorkflowValidation) string {
	if !CodeindexEnabled() {
		return ""
	}
	beadPath = NormalizeBeadPathForLayout(filepath.ToSlash(strings.TrimSpace(beadPath)), v.LayoutRoot)
	if beadPath == "" {
		return ""
	}
	mayorRigDir = strings.TrimSpace(mayorRigDir)
	indexPath := CodeindexIndexPath(mayorRigDir)
	if _, err := os.Stat(indexPath); err != nil {
		return ""
	}
	candidates := codeindexImpactCandidates(beadPath, v)
	var b strings.Builder
	for _, depPkg := range codeindexDependencySymbolPaths(beadPath, v) {
		if sym := fetchCodeindexSymbols(indexPath, []string{depPkg}); sym != "" {
			b.WriteString(formatCodeindexSymbolsSection("dependency "+depPkg, depPkg, sym, codeindexDepSymbolsMax))
		}
	}
	indexSym := fetchCodeindexSymbols(indexPath, candidates)
	archNames := ArchitectureContractSymbolNames(mayorRigDir, beadPath, v)

	if indexSym != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		label := "active package " + candidates[0]
		b.WriteString(formatCodeindexSymbolsSection(label, candidates[0], indexSym, codeindexSymbolsMaxBytes))
	} else if len(archNames) > 0 && len(candidates) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(formatSymbolNamesAsCodeindexSection("architecture / SPEC (default)", candidates[0], archNames))
	} else if len(candidates) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(formatCodeindexEmptyPackageSection(mayorRigDir, candidates[0], indexPath, beadPath, v))
	}
	if align := FormatCodeindexSymbolAlignmentSection(mayorRigDir, beadPath, v, indexPath, candidates); align != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(align)
	}
	return strings.TrimSpace(b.String())
}

// CodeindexContextSummary returns a one-line log summary of an injected codeindex block.
func CodeindexContextSummary(block string) string {
	block = strings.TrimSpace(block)
	if block == "" {
		return "empty"
	}
	names := extractCodeindexSymbolNames(block)
	if len(names) == 0 {
		if strings.Contains(block, "new/empty") {
			return "empty package guidance"
		}
		return "context injected"
	}
	if len(names) > 8 {
		return fmt.Sprintf("%d symbols (%s, …)", len(names), strings.Join(names[:8], ", "))
	}
	return fmt.Sprintf("%d symbols (%s)", len(names), strings.Join(names, ", "))
}

func extractCodeindexSymbolNames(block string) []string {
	seen := map[string]bool{}
	var names []string
	inSymbolsFence := false
	inTestSection := false
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "### Codeindex symbols") {
			inSymbolsFence = false
			inTestSection = false
			continue
		}
		if strings.HasPrefix(line, "**Test-only symbols**") {
			inTestSection = true
			continue
		}
		if strings.HasPrefix(line, "**Production symbols**") {
			inTestSection = false
			continue
		}
		if line == "```" {
			if inSymbolsFence {
				inSymbolsFence = false
			} else if strings.Contains(block, "### Codeindex symbols") {
				inSymbolsFence = true
			}
			continue
		}
		if !inSymbolsFence {
			continue
		}
		if inTestSection {
			continue
		}
		m := codeindexSymbolLineRE.FindStringSubmatch(line)
		if len(m) < 2 || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		names = append(names, m[1])
	}
	return names
}

// AllowedCodeindexSymbolNames returns exported symbol names in scope for a bead (deps + active package).
func AllowedCodeindexSymbolNames(mayorRigDir, beadPath string, v WorkflowValidation) map[string]bool {
	beadPath = NormalizeBeadPathForLayout(filepath.ToSlash(strings.TrimSpace(beadPath)), v.LayoutRoot)
	if beadPath == "" {
		return nil
	}
	indexPath := CodeindexIndexPath(strings.TrimSpace(mayorRigDir))
	if _, err := os.Stat(indexPath); err != nil {
		return nil
	}
	allowed := map[string]bool{}
	var paths []string
	paths = append(paths, codeindexDependencySymbolPaths(beadPath, v)...)
	paths = append(paths, codeindexImpactCandidates(beadPath, v)...)
	for _, line := range strings.Split(fetchCodeindexSymbols(indexPath, paths), "\n") {
		line = strings.TrimSpace(line)
		if i := strings.Index(line, " ("); i > 0 {
			allowed[line[:i]] = true
		}
	}
	return allowed
}
