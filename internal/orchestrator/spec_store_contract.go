package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
)

// IsStorePackageBeadPath reports store-layer production or test beads.
func IsStorePackageBeadPath(beadPath string) bool {
	p := filepath.ToSlash(strings.TrimSpace(beadPath))
	return strings.Contains(p, "internal/store/")
}

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

// LoadSpecStoreContractFromRig reads SPEC.md Store + Data model sections under mayor/rig.
func LoadSpecStoreContractFromRig(townRoot, rig string) string {
	if townRoot == "" || rig == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(townRoot, rig, "mayor", "rig", "SPEC.md"))
	if err != nil {
		return ""
	}
	doc := string(data)
	var parts []string
	if s := ExtractSpecMarkdownSection(doc, "Data model"); s != "" {
		parts = append(parts, "### Data model (SPEC.md)\n"+s)
	}
	if s := ExtractSpecMarkdownSection(doc, "Store"); s != "" {
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
	contract := LoadSpecStoreContractFromRig(townRoot, rig)
	base := strings.TrimSpace(`### Schema bead (SPEC.md — DDL only)
This bead is **` + "`schema.go`" + ` only**. Export:

` + "```go\nfunc InitSchema(db *sql.DB) error\n```" + `

- Use the **` + "`links`" + `** table DDL from SPEC **Data model** (not ` + "`bookmarks`" + `).
- Do **not** implement ` + "`List`" + ` / ` + "`Create`" + ` / ` + "`Delete`" + ` here — those belong on the **` + "`store.go`" + `** bead.
- You may add **` + "`schema_test.go`" + `** in this session (correlated test) calling ` + "`InitSchema`" + ` on ` + "`:memory:`" + ` and asserting the ` + "`links`" + ` table exists.
- No SQLite driver import required in ` + "`schema.go`" + ` if you only call ` + "`db.Exec`" + ` with DDL strings.`)
	if contract != "" && strings.Contains(contract, "Data model") {
		return base + "\n\n" + contract
	}
	return base
}

// FormatSpecStoreContractBlock injects SPEC store contract for store.go / store_test.go beads (not schema.go).
func FormatSpecStoreContractBlock(townRoot, rig, beadPath string) string {
	if !IsStorePackageBeadPath(beadPath) || IsSQLiteSchemaBeadPath(beadPath) {
		return ""
	}
	contract := LoadSpecStoreContractFromRig(townRoot, rig)
	if contract == "" {
		return strings.TrimSpace(`### Store package (SPEC)
Implement **only** the Store API from SPEC.md: ` + "`List(ctx context.Context) ([]Link, error)`" + `, ` + "`Create(ctx, title, url string) (Link, error)`" + `, ` + "`Delete(ctx, id int64) error`" + `.
Use the ` + "`links`" + ` table and ` + "`Link`" + ` struct from SPEC. Tests use ` + "`:memory:`" + ` SQLite and ` + "`InitSchema`" + ` — not a shared ` + "`./links.db`" + ` file.`)
	}
	extra := strings.TrimSpace(`**Alignment rules (mandatory):**
- Production and tests must use the **same** method names and signatures as SPEC (no ` + "`AddBookmark`" + ` / ` + "`GetAllBookmarks`" + ` / ` + "`Create(Link)`" + ` variants).
- ` + "`store_test.go`" + ` must call ` + "`NewStore(\":memory:\")`" + ` or open ` + "`:memory:`" + ` + ` + "`InitSchema`" + `; each test starts with an empty DB.
- ` + "`schema.go`" + ` (already implemented) owns DDL — call ` + "`InitSchema(db)`" + `, do not duplicate ` + "`CREATE TABLE`" + ` strings in ` + "`store.go`" + ` or tests.`)
	return contract + "\n\n" + extra
}

// FormatStoreTestBeadChecklist returns architecture-named tests for store_test.go beads.
func FormatStoreTestBeadChecklist(beadPath string) string {
	if !strings.HasSuffix(filepath.ToSlash(beadPath), "internal/store/store_test.go") {
		return ""
	}
	return strings.TrimSpace(`### store_test.go checklist (architecture.md)
Implement these tests against the **SPEC Store API** (not alternate symbol names):
- ` + "`TestStore_List_Empty`" + ` — ` + "`List`" + ` returns non-nil empty slice
- ` + "`TestStore_Create_List_Success`" + `
- ` + "`TestStore_Create_InvalidURL`" + `, ` + "`TestStore_Create_EmptyTitle`" + `, ` + "`TestStore_Create_TitleTooLong`" + `
- ` + "`TestStore_Delete_Success`" + `, ` + "`TestStore_Delete_NonExistentID`" + ``)
}
