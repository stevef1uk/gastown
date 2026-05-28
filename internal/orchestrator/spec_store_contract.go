package orchestrator

import (
	"path/filepath"
	"strings"
)

// ExtractSpecMarkdownSection returns lines under a ## heading until the next ## heading.
func ExtractSpecMarkdownSection(doc, heading string) string {
	doc = strings.ReplaceAll(doc, "\r\n", "\n")
	heading = strings.TrimSpace(heading)
	if heading == "" {
		return ""
	}
	var want []string
	for _, line := range strings.Split(heading, " ") {
		if t := strings.TrimSpace(line); t != "" {
			want = append(want, strings.ToLower(t))
		}
	}
	lines := strings.Split(doc, "\n")
	start := -1
	for i, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), "##") {
			continue
		}
		h := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "##")))
		if sectionHeadingMatches(h, want) {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return ""
	}
	var out []string
	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "##") {
			break
		}
		out = append(out, lines[i])
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func sectionHeadingMatches(h string, want []string) bool {
	for _, w := range want {
		if w != "" && strings.Contains(h, w) {
			return true
		}
	}
	return false
}

// IsStorePackageBeadPath reports store-layer production or test beads.
func IsStorePackageBeadPath(beadPath string) bool {
	p := filepath.ToSlash(strings.TrimSpace(beadPath))
	return strings.Contains(p, "/internal/store/")
}

// LoadSpecStoreContractFromRig reads SPEC.md Store + Data model sections under mayor/rig.
func LoadSpecStoreContractFromRig(townRoot, rig string) string {
	if townRoot == "" || rig == "" {
		return ""
	}
	return loadSpecStoreContractFromDir(filepath.Join(townRoot, rig, "mayor", "rig"))
}

func loadSpecStoreContractFromDir(rigDir string) string {
	specDoc := readRigDoc(rigDir, "SPEC.md")
	if strings.TrimSpace(specDoc) == "" {
		return ""
	}
	var parts []string
	if s := ExtractSpecMarkdownSection(specDoc, "Data model"); s != "" {
		parts = append(parts, "### Data model (SPEC.md)\n"+s)
	}
	if s := ExtractSpecMarkdownSection(specDoc, "Store"); s != "" {
		parts = append(parts, "### Store API (SPEC.md — authoritative)\n"+s)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n\n")
}

// FormatSpecSchemaContractBlock injects DDL-only guidance for the schema.go implement bead.
func FormatSpecSchemaContractBlock(townRoot, rig, beadPath string) string {
	if !IsSQLiteSchemaBeadPath(beadPath) {
		return ""
	}
	var b strings.Builder
	b.WriteString("### Schema bead (from SPEC.md)\n")
	b.WriteString("This bead owns **schema/migration DDL only** for the store package.\n")
	b.WriteString("- Export schema helpers named in **Architecture contract** / SPEC **Data model** (e.g. `InitSchema`).\n")
	b.WriteString("- Do **not** implement persistence/query methods here — those belong on the **store.go** bead.\n")
	b.WriteString("- Tests may use `:memory:` SQLite and assert tables from SPEC DDL.\n")
	return strings.TrimSpace(b.String())
}

// FormatSpecStoreContractBlock injects SPEC store contract for store.go / store_test.go beads (not schema.go).
func FormatSpecStoreContractBlock(townRoot, rig, beadPath string, v WorkflowValidation) string {
	if !IsStorePackageBeadPath(beadPath) || IsSQLiteSchemaBeadPath(beadPath) {
		return ""
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	var b strings.Builder
	b.WriteString("### Store package (from SPEC.md)\n")
	b.WriteString("Implement **only** the Store API for this bead — same names/signatures in production and tests.\n")
	b.WriteString("- Call schema helpers from the schema/migrate bead; do not duplicate `CREATE TABLE` in this file.\n")
	b.WriteString("- **Do not redefine domain types** already in an earlier same-package file (see **Go package bead ownership**). Use package-level types from the schema bead.\n")
	for _, schemaPath := range schemaBeadPathsInProfile(beadPath, v) {
		sym := readExportedGoSymbolsFromRig(rigDir, schemaPath)
		if len(sym.Types) > 0 {
			b.WriteString("- Types already defined in `")
			b.WriteString(schemaPath)
			b.WriteString("`: ")
			b.WriteString(strings.Join(sym.Types, ", "))
			b.WriteString(" — reference them; omit `type ... struct` from your WRITE.\n")
			break
		}
	}
	b.WriteString("- Use a **fresh DB per test** (`:memory:` or temp file) — not a shared on-disk DB from prior runs.\n")
	return strings.TrimSpace(b.String())
}

// FormatStoreTestBeadChecklist returns test guidance for store_test.go beads (names from architecture/plan).
func FormatStoreTestBeadChecklist(townRoot, rig, beadPath string) string {
	if !strings.HasSuffix(filepath.ToSlash(beadPath), "internal/store/store_test.go") {
		return ""
	}
	names := extractTestNamesForBead(
		readRigDoc(filepath.Join(townRoot, rig, "mayor", "rig"), "architecture.md"),
		readRigDoc(filepath.Join(townRoot, rig, "mayor", "rig"), "plan.md"),
		beadPath,
		"",
	)
	if len(names) == 0 {
		return strings.TrimSpace("### store_test.go\nImplement table-driven tests mapped to SPEC **Store** acceptance and architecture unit-test bullets.")
	}
	return strings.TrimSpace("### store_test.go (architecture/plan)\nImplement: " + strings.Join(names, ", "))
}

func schemaBeadPathsInProfile(activePath string, v WorkflowValidation) []string {
	var out []string
	for _, p := range EarlierRequiredFilesForBead(activePath, v.RequiredFiles) {
		if IsSQLiteSchemaBeadPath(p) {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		for _, p := range v.RequiredFiles {
			if IsSQLiteSchemaBeadPath(p) {
				out = append(out, p)
			}
		}
	}
	return out
}
