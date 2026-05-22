package orchestrator

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ValidateImplementWritePath checks whether relPath may be written during implementation.
// fullReplace true simulates heredoc/WRITE (rejects incremental-edit files); false allows partial edits (EDIT/sed).
func ValidateImplementWritePath(townRoot, rig, activeBead, relPath string, v WorkflowValidation, fullReplace bool) error {
	relPath = NormalizeBeadPathForLayout(SanitizeNativeEditRelPath(relPath), v.LayoutRoot)
	if relPath == "" {
		return fmt.Errorf("empty path")
	}
	if !IsValidImplementBeadPath(relPath) {
		return fmt.Errorf("invalid implement path %q", relPath)
	}
	if fullReplace {
		fake := fmt.Sprintf("cat > %s <<'EOF'", relPath)
		if reason := RejectFullFileHeredocReason(fake, townRoot, rig, activeBead, v); reason != "" {
			return fmt.Errorf("%s", reason)
		}
	}
	return validateImplementWriteScope(townRoot, rig, activeBead, relPath, v)
}

// ValidateImplementReadPath allows reads for the active bead, open/next implement paths, and earlier dependencies.
func ValidateImplementReadPath(townRoot, rig, activeBead, relPath string, v WorkflowValidation) error {
	relPath = NormalizeBeadPathForLayout(filepath.ToSlash(strings.TrimSpace(relPath)), v.LayoutRoot)
	if relPath == "" {
		return fmt.Errorf("empty path")
	}
	if len(v.RequiredFiles) == 0 {
		return nil
	}
	allowedID := strings.TrimSpace(activeBead)
	if allowedID == "" {
		next, err := NextOpenImplementBead(townRoot, rig, v)
		if err != nil || next == nil {
			return nil
		}
		allowedID = next.ID
	}
	allowedPath := ImplementBeadPathForID(townRoot, rig, allowedID, v)
	if allowedPath != "" && PathMatchesImplementWrite(relPath, allowedPath, v.RequiredFiles) {
		return nil
	}
	if allowedPath != "" && WorkflowUsesGo(v) && !IsTestImplementPath(allowedPath) {
		if testPath := CorrelatedTestPathForSource(allowedPath, v.LayoutRoot); testPath != "" {
			if PathMatchesImplementWrite(relPath, testPath, v.RequiredFiles) {
				return nil
			}
		}
	}
	if allowedPath != "" && AllowedEarlierImplementDependencyWrite(townRoot, rig, allowedPath, relPath, v) {
		return nil
	}
	for _, want := range v.RequiredFiles {
		if PathMatchesImplementWrite(relPath, want, v.RequiredFiles) {
			return nil
		}
	}
	return fmt.Errorf("read only files under %s from required_files or dependency packages (active bead %s)",
		strings.Trim(v.LayoutRoot, "/"), allowedID)
}

func validateImplementWriteScope(townRoot, rig, activeBead, written string, v WorkflowValidation) error {
	allowedID := strings.TrimSpace(activeBead)
	if allowedID == "" {
		next, err := NextOpenImplementBead(townRoot, rig, v)
		if err != nil || next == nil {
			return nil
		}
		allowedID = next.ID
	}
	allowedPath := ImplementBeadPathForID(townRoot, rig, allowedID, v)
	if closedOnly, err := ImplementPathHasOnlyClosedBeads(townRoot, rig, written, v); err == nil && closedOnly {
		if allowedPath != "" {
			return fmt.Errorf("do not overwrite %q — its implement bead is closed (reopen that bead or edit only %s for %s)",
				written, allowedPath, allowedID)
		}
		return fmt.Errorf("do not overwrite %q — its implement bead is closed (active bead %s)", written, allowedID)
	}
	if allowedPath == "" {
		return nil
	}
	if PathMatchesImplementWrite(written, allowedPath, v.RequiredFiles) {
		return nil
	}
	// Same-bead unit tests (e.g. schema.go → schema_test.go) are not separate implement beads.
	if WorkflowUsesGo(v) && !IsTestImplementPath(allowedPath) {
		if testPath := CorrelatedTestPathForSource(allowedPath, v.LayoutRoot); testPath != "" {
			if PathMatchesImplementWrite(written, testPath, v.RequiredFiles) {
				return nil
			}
		}
	}
	if AllowedEarlierImplementDependencyWrite(townRoot, rig, allowedPath, written, v) {
		return nil
	}
	if strings.HasSuffix(filepath.ToSlash(allowedPath), "go.mod") && strings.HasSuffix(written, ".go") {
		for _, want := range v.RequiredFiles {
			if PathMatchesImplementWrite(written, want, v.RequiredFiles) {
				return nil
			}
		}
	}
	return fmt.Errorf("write only the active/next implement file (%s for bead %s), not %q",
		allowedPath, allowedID, written)
}
