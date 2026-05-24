package orchestrator

import (
	"fmt"
	"path/filepath"
	"strings"
)

// crossBeadSymbolIssues lists content violations when symbols belong on another implement bead.
// Uses earlier/later profile paths in the same Go package and symbols on disk (not rig-specific names).
func crossBeadSymbolIssues(mayorRigDir, relPath, content string, v WorkflowValidation) []string {
	if !WorkflowUsesGo(v) {
		return nil
	}
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	if relPath == "" {
		return nil
	}
	var issues []string
	incoming := exportedSymbolsInContent(content)
	earlier := symbolsDefinedOnEarlierSiblings(mayorRigDir, relPath, v)

	for _, name := range incoming.Types {
		for _, sib := range earlierSamePackageFiles(relPath, v) {
			sibSym := readExportedGoSymbolsFromRig(mayorRigDir, sib)
			for _, t := range sibSym.Types {
				if t == name {
					issues = append(issues, fmt.Sprintf("type %s is already defined in %s — use the package-level %s from that file; do not redefine in %s", name, sib, name, filepath.Base(relPath)))
					break
				}
			}
		}
	}
	for _, name := range incoming.Funcs {
		for _, f := range earlier.Funcs {
			if f == name {
				sib := findSiblingDefiningSymbol(mayorRigDir, relPath, name, true, v)
				issues = append(issues, fmt.Sprintf("%s is already defined in %s — call it, do not redefine in %s", name, sib, filepath.Base(relPath)))
				break
			}
		}
	}

	if packageHasSchemaOwner(relPath, v) && !IsSQLiteSchemaBeadPath(relPath) && !strings.HasSuffix(relPath, "_test.go") {
		if strings.Contains(content, "CREATE TABLE") {
			issues = append(issues, "CREATE TABLE DDL belongs on the schema/migrate implement bead for this package")
		}
	}

	if IsSQLiteSchemaBeadPath(relPath) {
		later := symbolsOwnedByLaterSiblings(mayorRigDir, relPath, v)
		for _, name := range incoming.Types {
			for _, t := range later.Types {
				if t == name {
					issues = append(issues, fmt.Sprintf("type %s belongs on a later implement file in this package — schema bead is DDL/types only", name))
				}
			}
		}
		for _, name := range incoming.Funcs {
			for _, f := range later.Funcs {
				if f == name {
					issues = append(issues, fmt.Sprintf("func %s belongs on a later implement file in this package (e.g. store API)", name))
				}
			}
		}
	}

	return issues
}

func findSiblingDefiningSymbol(rigDir, relPath, name string, isFunc bool, v WorkflowValidation) string {
	for _, sib := range earlierSamePackageFiles(relPath, v) {
		sym := readExportedGoSymbolsFromRig(rigDir, sib)
		if isFunc {
			for _, f := range sym.Funcs {
				if f == name {
					return sib
				}
			}
		} else {
			for _, t := range sym.Types {
				if t == name {
					return sib
				}
			}
		}
	}
	return "an earlier implement file"
}

// ValidateImplementCrossBeadContent rejects WRITE/EDIT that duplicates symbols owned by sibling beads.
func ValidateImplementCrossBeadContent(mayorRigDir, relPath, content string, v WorkflowValidation) error {
	issues := crossBeadSymbolIssues(mayorRigDir, relPath, content, v)
	if len(issues) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(issues, "; "))
}
