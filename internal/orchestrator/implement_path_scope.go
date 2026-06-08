package orchestrator

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ValidateImplementWritePath checks whether relPath may be written during implementation.
// fullReplace true simulates heredoc/WRITE (rejects incremental-edit files); false allows partial edits (EDIT/sed).
// verifyOutput is recent go test/build stderr (optional); enables closed-dep fixes for nil-slice List failures.
func ValidateImplementWritePath(townRoot, rig, activeBead, relPath string, v WorkflowValidation, fullReplace bool, verifyOutput string, scope *ImplementWriteScope) error {
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
	var sc ImplementWriteScope
	if scope != nil {
		sc = *scope
	}
	return validateImplementWriteScope(townRoot, rig, activeBead, relPath, v, verifyOutput, sc)
}

// ValidateImplementReadPath allows reads for the active bead, open/next implement paths, and earlier dependencies.
// verifyOutput is optional recent go test/build stderr; enables read of foreign *_test.go cited in verify failures.
func ValidateImplementReadPath(townRoot, rig, activeBead, relPath string, v WorkflowValidation, verifyOutput string) error {
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
	if allowedPath != "" && PathMatchesImplementWrite(relPath, allowedPath, v.RequiredFiles, v) {
		return nil
	}
	if allowedPath != "" && WorkflowUsesGo(v) && !IsTestImplementPath(allowedPath) {
		if testPath := CorrelatedTestPathForSource(allowedPath, v); testPath != "" {
			if PathMatchesImplementWrite(relPath, testPath, v.RequiredFiles, v) {
				return nil
			}
		}
	}
	if allowedPath != "" && AllowedCorrelatedPackageImplementWrite(allowedPath, relPath, v) {
		return nil
	}
	if allowedPath != "" && AllowedEarlierImplementDependencyWrite(townRoot, rig, allowedPath, relPath, v) {
		return nil
	}
	mayorRigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	if allowedPath != "" && IsForeignBeadTestFileForActive(allowedPath, relPath, v, mayorRigDir) {
		if AllowForeignOpenBeadCompileFixForVerifyFailure(townRoot, rig, allowedPath, relPath, verifyOutput, v) {
			return nil
		}
		if src := SourcePathForCorrelatedTest(relPath, v.LayoutRoot); src != "" {
			if _, _, ok := OpenImplementBeadForPath(townRoot, rig, src, v); ok {
				return nil
			}
		}
	}
	for _, want := range v.RequiredFiles {
		if PathMatchesImplementWrite(relPath, want, v.RequiredFiles, v) {
			return nil
		}
	}
	return fmt.Errorf("read only files under %s from required_files or dependency packages (active bead %s)",
		strings.Trim(v.LayoutRoot, "/"), allowedID)
}

func validateImplementWriteScope(townRoot, rig, activeBead, written string, v WorkflowValidation, verifyOutput string, scope ImplementWriteScope) error {
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	if ImplementationQueueGreen(townRoot, rig, v) {
		written = NormalizeBeadPathForLayout(filepath.ToSlash(strings.TrimSpace(written)), v.LayoutRoot)
		allowStubFix := false
		for _, stub := range UnionStubArtifactsOnDisk(rigDir, v) {
			if PathMatchesImplementWrite(written, stub, v.RequiredFiles, v) {
				allowStubFix = true
				break
			}
		}
		if !allowStubFix {
			return fmt.Errorf("implementation queue is finished and go test ./... passes — do not EDIT/WRITE implement files; send JSON {\"outcome\":\"success\",\"summary\":\"...\"} only")
		}
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
	if AllowedQAReworkWebImplementWrite(townRoot, rig, allowedID, allowedPath, written, scope, v) {
		if err := ValidateHTTPHandlerBeadPrerequisites(filepath.Join(townRoot, rig, "mayor", "rig"), written, v); err != nil {
			return err
		}
		return nil
	}
	if AllowClosedDepFixForVerifyFailure(townRoot, rig, allowedPath, written, verifyOutput, v) {
		return nil
	}
	if AllowForeignOpenBeadCompileFixForVerifyFailure(townRoot, rig, allowedPath, written, verifyOutput, v) {
		return nil
	}
	if closedOnly, err := ImplementPathHasOnlyClosedBeads(townRoot, rig, written, v); err == nil && closedOnly &&
		!AllowedCorrelatedPackageImplementWrite(allowedPath, written, v) {
		if reopened, rerr := EnsureOpenImplementBeadForRework(townRoot, rig, written, v); rerr != nil {
			return rerr
		} else if reopened != "" {
			return nil
		}
		if allowedPath != "" {
			return fmt.Errorf("do not overwrite %q — its implement bead is closed (reopen that bead or edit only %s for %s)",
				written, allowedPath, allowedID)
		}
		return fmt.Errorf("do not overwrite %q — its implement bead is closed (active bead %s)", written, allowedID)
	}
	if allowedPath == "" {
		if allowedID != "" {
			return fmt.Errorf("could not resolve file path for active implement bead %s (check bd list / BEADS_DIR)", allowedID)
		}
		return nil
	}
	if PathMatchesImplementWrite(written, allowedPath, v.RequiredFiles, v) {
		if err := ValidateHTTPHandlerBeadPrerequisites(filepath.Join(townRoot, rig, "mayor", "rig"), written, v); err != nil {
			return err
		}
		return nil
	}
	// Same-bead unit tests (e.g. schema.go → schema_test.go) are not separate implement beads.
	if WorkflowUsesGo(v) && !IsTestImplementPath(allowedPath) {
		if testPath := CorrelatedTestPathForSource(allowedPath, v); testPath != "" {
			if PathMatchesImplementWrite(written, testPath, v.RequiredFiles, v) {
				return nil
			}
		}
	}
	if AllowedCorrelatedPackageImplementWrite(allowedPath, written, v) {
		if err := ValidateHTTPHandlerBeadPrerequisites(filepath.Join(townRoot, rig, "mayor", "rig"), written, v); err != nil {
			return err
		}
		return nil
	}
	if AllowedEarlierImplementDependencyWrite(townRoot, rig, allowedPath, written, v) {
		return nil
	}
	if strings.HasSuffix(filepath.ToSlash(allowedPath), "go.mod") && strings.HasSuffix(written, ".go") {
		for _, want := range v.RequiredFiles {
			if PathMatchesImplementWrite(written, want, v.RequiredFiles, v) {
				return nil
			}
		}
	}
	return NewImplementWriteScopeError(townRoot, rig, allowedID, allowedPath, written, v)
}

// OpenImplementBeadForPath returns an open or in_progress implement bead that owns filePath.
func OpenImplementBeadForPath(townRoot, rig, filePath string, v WorkflowValidation) (id, beadPath string, ok bool) {
	filePath = NormalizeBeadPathForLayout(filepath.ToSlash(strings.TrimSpace(filePath)), v.LayoutRoot)
	if filePath == "" {
		return "", "", false
	}
	beads, err := ListImplementBeadsOpenOrInProgress(townRoot, rig, v)
	if err != nil {
		return "", "", false
	}
	for _, b := range beads {
		p := NormalizeBeadPathForLayout(ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains), v.LayoutRoot)
		if p == "" {
			continue
		}
		if PathMatchesImplementWrite(filePath, p, v.RequiredFiles, v) {
			return b.ID, p, true
		}
	}
	return "", "", false
}

// NewImplementWriteScopeError explains why a native EDIT/WRITE path was rejected.
func NewImplementWriteScopeError(townRoot, rig, allowedID, allowedPath, written string, v WorkflowValidation) error {
	base := fmt.Errorf("write only the active/next implement file (%s for bead %s), not %q",
		allowedPath, allowedID, written)
	if AllowedCorrelatedPackageImplementWrite(allowedPath, written, v) {
		return fmt.Errorf("%w — use **EDIT:**/**WRITE:** on %q to align the same Go package, then **Verify** (go test on this package)",
			base, written)
	}
	if targetID, targetPath, ok := OpenImplementBeadForPath(townRoot, rig, written, v); ok && targetID != "" && targetID != allowedID {
		return fmt.Errorf("%w — %q belongs to open bead %s (%s); finish bead %s (%s) first, then `CMD: bd update %s --status=in_progress` → **EDIT:** → Verify → `bd close %s`",
			base, written, targetID, targetPath, allowedID, allowedPath, targetID, targetID)
	}
	return base
}
