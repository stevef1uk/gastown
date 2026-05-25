package orchestrator

import (
	"path/filepath"
	"sort"
	"strings"
)

// ArchitectureContractOwnedSymbolNames returns exported symbols this implement path **owns**
// per ownership tables in SPEC/architecture/plan (Owns/Defines column only — not Depends/Must-not-define).
func ArchitectureContractOwnedSymbolNames(mayorRigDir, beadPath string, v WorkflowValidation) []string {
	sym := architectureOwnedSymbolsForBead(mayorRigDir, beadPath, v)
	out := append(append([]string{}, sym.Types...), sym.Funcs...)
	sort.Strings(out)
	return out
}

func architectureOwnedSymbolsForBead(mayorRigDir, beadPath string, v WorkflowValidation) goExportedSymbols {
	beadPath = NormalizeBeadPathForLayout(filepath.ToSlash(strings.TrimSpace(beadPath)), v.LayoutRoot)
	if beadPath == "" {
		return goExportedSymbols{}
	}
	return ownedSymbolsFromOwnershipTables(
		readRigDoc(mayorRigDir, "SPEC.md"),
		readRigDoc(mayorRigDir, "architecture.md"),
		readRigDoc(mayorRigDir, "plan.md"),
		beadPath,
		v.LayoutRoot,
	)
}

func ownedSymbolsFromOwnershipTables(specDoc, archDoc, planDoc, beadPath, layoutRoot string) goExportedSymbols {
	beadPath = NormalizeBeadPathForLayout(filepath.ToSlash(strings.TrimSpace(beadPath)), layoutRoot)
	seenT := map[string]bool{}
	seenF := map[string]bool{}
	var types, funcs []string
	merge := func(cell string) {
		s := symbolsFromOwnershipOwnsCell(cell)
		for _, n := range s.Types {
			if !seenT[n] {
				seenT[n] = true
				types = append(types, n)
			}
		}
		for _, n := range s.Funcs {
			if !seenF[n] {
				seenF[n] = true
				funcs = append(funcs, n)
			}
		}
	}
	for _, doc := range []string{specDoc, archDoc, planDoc} {
		if strings.TrimSpace(doc) == "" {
			continue
		}
		lines := strings.Split(doc, "\n")
		var ownsIdx int = -1
		for _, line := range lines {
			trim := strings.TrimSpace(line)
			if !strings.HasPrefix(trim, "|") {
				continue
			}
			if isMarkdownTableSeparator(trim) {
				continue
			}
			cells := splitMarkdownTableRow(trim)
			if len(cells) < 2 {
				continue
			}
			lower := strings.ToLower(trim)
			if ownsIdx < 0 && ownershipTableHeaderRow(lower) {
				ownsIdx, _ = ownershipTableColumnRoles(cells)
				continue
			}
			if ownsIdx < 0 || !lineMatchesOwnershipTableRow(lower, cells, beadPath) {
				continue
			}
			if ownsIdx < len(cells) {
				merge(cells[ownsIdx])
			}
		}
	}
	sort.Strings(types)
	sort.Strings(funcs)
	return goExportedSymbols{Types: types, Funcs: funcs}
}

// lineMatchesOwnershipTableRow matches ownership-table data rows to a single implement file path.
// Uses full path and file basename only — not package-stem keys (e.g. "store" must not match schema.go).
func lineMatchesOwnershipTableRow(lowerLine string, cells []string, beadPath string) bool {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	if beadPath == "" {
		return false
	}
	base := strings.ToLower(filepath.Base(beadPath))
	lowerBead := strings.ToLower(beadPath)
	if strings.Contains(lowerLine, lowerBead) {
		return true
	}
	for _, c := range cells {
		c = strings.ToLower(strings.TrimSpace(c))
		if c == base || c == lowerBead || strings.HasSuffix(c, "/"+base) {
			return true
		}
	}
	return false
}

// symbolsFromOwnershipOwnsCell parses backtick Go fragments in ownership-table Owns cells.
func symbolsFromOwnershipOwnsCell(cell string) goExportedSymbols {
	cell = strings.TrimSpace(cell)
	if cell == "" || cell == "—" || cell == "-" {
		return goExportedSymbols{}
	}
	for _, sym := range extractContractSymbolsFromGoSource(cell) {
		cell = cell + "\n" + sym
	}
	normalized := strings.ReplaceAll(cell, "`", "\n")
	return splitExportedGoSymbols(normalized)
}

func splitMarkdownTableRow(line string) []string {
	parts := strings.Split(line, "|")
	var cells []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			cells = append(cells, p)
		}
	}
	return cells
}

func isMarkdownTableSeparator(line string) bool {
	trim := strings.TrimSpace(line)
	if !strings.HasPrefix(trim, "|") {
		return false
	}
	inner := strings.ReplaceAll(trim, "|", "")
	inner = strings.ReplaceAll(inner, "-", "")
	inner = strings.ReplaceAll(inner, ":", "")
	inner = strings.ReplaceAll(inner, " ", "")
	return inner == ""
}

func ownershipTableHeaderRow(lowerLine string) bool {
	if !strings.Contains(lowerLine, "|") {
		return false
	}
	hasFile := strings.Contains(lowerLine, "file")
	hasOwn := strings.Contains(lowerLine, "own") || strings.Contains(lowerLine, "export") ||
		strings.Contains(lowerLine, "defines")
	hasDep := strings.Contains(lowerLine, "depends") || strings.Contains(lowerLine, "must not")
	return (hasFile && (hasOwn || hasDep)) || (hasOwn && hasDep)
}

func ownershipTableColumnRoles(cells []string) (ownsIdx, dependsIdx int) {
	ownsIdx, dependsIdx = -1, -1
	for i, c := range cells {
		lower := strings.ToLower(strings.TrimSpace(c))
		if lower == "" {
			continue
		}
		switch {
		case strings.Contains(lower, "must not") || strings.Contains(lower, "do not define") ||
			strings.Contains(lower, "depends") || strings.Contains(lower, "imports from"):
			if dependsIdx < 0 {
				dependsIdx = i
			}
		case strings.Contains(lower, "own") || strings.Contains(lower, "export") ||
			(strings.Contains(lower, "define") && !strings.Contains(lower, "not")):
			if ownsIdx < 0 {
				ownsIdx = i
			}
		}
	}
	if ownsIdx < 0 && len(cells) >= 4 {
		ownsIdx = len(cells) - 2
		dependsIdx = len(cells) - 1
	} else if ownsIdx < 0 && len(cells) == 3 {
		ownsIdx = 1
		dependsIdx = 2
	}
	return ownsIdx, dependsIdx
}
