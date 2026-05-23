package orchestrator

import (
	"fmt"
	"path/filepath"
	"strings"
)

// crossBeadSymbolIssues lists content violations when symbols belong on another implement bead.
func crossBeadSymbolIssues(relPath, content string, v WorkflowValidation) []string {
	if !WorkflowUsesGo(v) {
		return nil
	}
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	if relPath == "" {
		return nil
	}
	var issues []string
	hasSchema := profileHasSQLiteSchemaBead(v)

	if IsSQLiteSchemaBeadPath(relPath) {
		for _, sym := range []string{"func NewStore", "type Store struct", "func (s *Store)", "func List(", "func Create(", "func Delete("} {
			if strings.Contains(content, sym) {
				issues = append(issues, fmt.Sprintf("schema bead must not define %q (belongs on store.go bead)", sym))
			}
		}
	}

	if hasSchema && !IsSQLiteSchemaBeadPath(relPath) {
		if strings.Contains(content, "func InitSchema") {
			issues = append(issues, "InitSchema belongs on the schema.go bead — call InitSchema(db), do not redefine")
		}
		if strings.Contains(content, "CREATE TABLE") && !strings.HasSuffix(relPath, "_test.go") {
			issues = append(issues, "CREATE TABLE DDL belongs on the schema.go bead")
		}
	}

	if hasSchema && isStoreLayerProductionBead(relPath) {
		if strings.Contains(content, "type Link struct") {
			issues = append(issues, "type Link belongs on schema.go — use the package-level Link from schema, do not redefine in store.go")
		}
	}

	return issues
}

func profileHasSQLiteSchemaBead(v WorkflowValidation) bool {
	for _, f := range v.RequiredFiles {
		if IsSQLiteSchemaBeadPath(f) {
			return true
		}
	}
	return false
}

func isStoreLayerProductionBead(relPath string) bool {
	if IsTestImplementPath(relPath) || IsSQLiteSchemaBeadPath(relPath) {
		return false
	}
	lower := strings.ToLower(filepath.ToSlash(relPath))
	return strings.Contains(lower, "/internal/store/") && strings.HasSuffix(lower, ".go")
}

// ValidateImplementCrossBeadContent rejects WRITE/EDIT that duplicates symbols owned by sibling beads.
func ValidateImplementCrossBeadContent(relPath, content string, v WorkflowValidation) error {
	issues := crossBeadSymbolIssues(relPath, content, v)
	if len(issues) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(issues, "; "))
}
