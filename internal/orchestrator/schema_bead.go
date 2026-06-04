package orchestrator

import (
	"path/filepath"
	"strings"
)

// SQLiteSchemaRelPath is the canonical store-layer schema bead (under layout_root).
const SQLiteSchemaRelPath = "internal/store/schema.go"

// SQLiteSchemaBeadPath returns layout_root/internal/store/schema.go when layout is set.
func SQLiteSchemaBeadPath(layoutRoot string) string {
	layout := strings.Trim(strings.TrimSpace(layoutRoot), "/")
	if layout == "" || layout == "." {
		return SQLiteSchemaRelPath
	}
	return layout + "/" + SQLiteSchemaRelPath
}

// SQLiteStoreRelPath is the canonical store API bead (under layout_root).
const SQLiteStoreRelPath = "internal/store/store.go"

// SQLiteStoreBeadPath returns layout_root/internal/store/store.go when layout is set.
func SQLiteStoreBeadPath(layoutRoot string) string {
	layout := strings.Trim(strings.TrimSpace(layoutRoot), "/")
	if layout == "" || layout == "." {
		return SQLiteStoreRelPath
	}
	return layout + "/" + SQLiteStoreRelPath
}

// StoreBeadPathFromProfile returns the profile store.go required_files path, or SQLiteStoreBeadPath(layout_root).
func StoreBeadPathFromProfile(v WorkflowValidation) string {
	v = v.ForActivePhase()
	for _, f := range v.RequiredFiles {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if strings.HasSuffix(strings.ToLower(f), "/internal/store/store.go") {
			return f
		}
	}
	return SQLiteStoreBeadPath(v.LayoutRoot)
}

// IsSQLiteSchemaBeadPath reports whether path is the injected schema/migrate bead.
func IsSQLiteSchemaBeadPath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	return strings.HasSuffix(lower, "/internal/store/schema.go") ||
		strings.HasSuffix(lower, "/internal/store/migrate.go")
}

// profileNeedsSQLiteSchemaBead reports whether the rig profile targets a Go SQLite store layer.
func profileNeedsSQLiteSchemaBead(v WorkflowValidation) bool {
	if !WorkflowUsesGo(v) {
		return false
	}
	check := func(files []string) bool {
		hasStore := false
		for _, f := range files {
			f = filepath.ToSlash(strings.TrimSpace(f))
			if f == "" || IsSQLiteSchemaBeadPath(f) {
				continue
			}
			lower := strings.ToLower(f)
			if strings.Contains(lower, "/internal/store/") && strings.HasSuffix(lower, ".go") &&
				!strings.HasSuffix(lower, "_test.go") {
				hasStore = true
			}
		}
		return hasStore
	}
	if check(v.RequiredFiles) {
		return true
	}
	for _, p := range v.DeliveryPhases {
		if check(p.RequiredFiles) {
			return true
		}
	}
	sum := strings.ToLower(v.SpecSummary)
	return strings.Contains(sum, "sqlite") &&
		(strings.Contains(sum, "store") || strings.Contains(sum, "database") || strings.Contains(sum, "persist"))
}

func hasSQLiteSchemaInFiles(files []string) bool {
	for _, f := range files {
		if IsSQLiteSchemaBeadPath(f) {
			return true
		}
	}
	return false
}

func fileListNeedsSQLiteSchema(files []string) bool {
	for _, f := range files {
		lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(f)))
		if strings.Contains(lower, "/internal/store/") && strings.HasSuffix(lower, ".go") &&
			!strings.HasSuffix(lower, "_test.go") && !IsSQLiteSchemaBeadPath(f) {
			return true
		}
	}
	return false
}

func injectSchemaIntoFileList(files []string, schemaPath string) []string {
	schemaPath = filepath.ToSlash(strings.TrimSpace(schemaPath))
	if schemaPath == "" || hasSQLiteSchemaInFiles(files) {
		return files
	}
	if !fileListNeedsSQLiteSchema(files) {
		return files
	}
	out := make([]string, 0, len(files)+1)
	out = append(out, schemaPath)
	seen := map[string]bool{schemaPath: true}
	for _, f := range files {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

// InjectSQLiteSchemaBead adds internal/store/schema.go to required_files and delivery phases
// when the profile implements a Go store package but omits explicit DDL/migrate ownership.
func InjectSQLiteSchemaBead(v WorkflowValidation) WorkflowValidation {
	if !profileNeedsSQLiteSchemaBead(v) {
		return v
	}
	schema := SQLiteSchemaBeadPath(v.LayoutRoot)
	if hasSQLiteSchemaInFiles(v.RequiredFiles) {
		return v
	}
	v.RequiredFiles = injectSchemaIntoFileList(v.RequiredFiles, schema)
	for i := range v.DeliveryPhases {
		v.DeliveryPhases[i].RequiredFiles = injectSchemaIntoFileList(v.DeliveryPhases[i].RequiredFiles, schema)
	}
	return v
}
