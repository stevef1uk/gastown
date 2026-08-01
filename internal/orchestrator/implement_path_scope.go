package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FailingVerifyTestPath returns the required test file that verifyOutput cites as failing
// (e.g. vitest "FAIL personal-space/tests/unit/backend/theme.test.ts", go "--- FAIL: TestX
// (path/foo_test.go:12)", pytest "FAILED tests/test_x.py::..."). It matches FAIL lines
// against the profile's required test files only — no language-specific parsing, no import
// resolution — so it is generic across rigs and test runners. Returns "" when none found.
func FailingVerifyTestPath(output string, v WorkflowValidation) string {
	if strings.TrimSpace(output) == "" {
		return ""
	}
	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(strings.ToUpper(line), "FAIL") {
			continue
		}
		lower := strings.ToLower(line)
		for _, req := range v.RequiredFiles {
			norm := NormalizeBeadPathForLayout(filepath.ToSlash(strings.TrimSpace(req)), v.LayoutRoot)
			if norm == "" || !IsTestImplementPath(norm) {
				continue
			}
			if strings.Contains(lower, strings.ToLower(norm)) {
				return norm
			}
		}
	}
	return ""
}

// RouterWiringHintFromVerifyOutput inspects verifyOutput for a 404 on a route
// that the active bead implements (router/handler file). If found and the
// failing test cites an app/server entry, returns guidance to edit that entry
// (which auto-rewinds if its bead is closed). Fully generic: matches required
// test files and 404 patterns; no rig-specific paths or import resolution.
func RouterWiringHintFromVerifyOutput(output string, activeBeadPath string, v WorkflowValidation) string {
	if strings.TrimSpace(output) == "" {
		return ""
	}
	lower := strings.ToLower(output)
	if !strings.Contains(lower, "404") && !strings.Contains(lower, "not found") {
		return ""
	}
	// Active bead must look like a router/handler (routes/... or handlers/...)
	activeLower := strings.ToLower(activeBeadPath)
	isRouter := strings.Contains(activeLower, "/routes/") ||
		strings.Contains(activeLower, "/handlers/") ||
		strings.HasSuffix(activeLower, "_router.go") ||
		strings.HasSuffix(activeLower, "_router.ts") ||
		strings.HasSuffix(activeLower, "_router.js") ||
		strings.HasSuffix(activeLower, "/router.go") ||
		strings.HasSuffix(activeLower, "/router.ts") ||
		strings.HasSuffix(activeLower, "/router.js")
	if !isRouter {
		return ""
	}
	// Find the failing test file from required_files (matches FAIL lines)
	testPath := FailingVerifyTestPath(output, v)
	if testPath == "" {
		return ""
	}
	return fmt.Sprintf(
		"the last Verify 404s on a route from your active bead (%s). Read the failing test %q — it imports the app/server entry that must mount your router. Edit THAT file; if its implement bead is closed in an earlier phase, writing to it auto-rewinds the phase so the bead reopens.",
		activeBeadPath, testPath)
}

// ImplementWriteScopeVerifyHint returns guidance pointing the agent at the failing
// test file when a write is rejected because the path has no implement bead.
// Generic: names the test from verifyOutput, never hardcodes a path.
func ImplementWriteScopeVerifyHint(verifyOutput string, v WorkflowValidation) string {
	test := FailingVerifyTestPath(verifyOutput, v)
	if test == "" {
		return ""
	}
	return fmt.Sprintf(
		"the last Verify failed in %q — read that test to see which module it imports and needs wired (e.g. the server/app entry that must mount the active router). Then edit THAT file; if its implement bead is closed in an earlier phase, writing to it auto-rewinds the phase so the bead reopens",
		test)
}

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
	// Generic: when writing to cmd/server/main.go, verify that handler dependency files
	// have real content — not empty stubs. Otherwise the polecat invents fake exports.
	if IsCmdMainImplementPath(relPath) && WorkflowUsesGo(v) {
		rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
		for _, dep := range requiredFilesBeforePath(relPath, v) {
			dep = filepath.ToSlash(dep)
			if !strings.HasSuffix(dep, ".go") || IsTestImplementPath(dep) || strings.HasSuffix(dep, "/go.mod") {
				continue
			}
			// Only guard handler-style files — not store/schema deps which are fine as stubs.
			if !strings.Contains(dep, "/api/") && !strings.Contains(dep, "/handlers/") && !strings.HasSuffix(dep, "handlers.go") {
				continue
			}
			depPath := filepath.Join(rigDir, filepath.FromSlash(dep))
			info, statErr := os.Stat(depPath)
			if statErr != nil {
				continue
			}
			if info.Size() < 40 {
				return fmt.Errorf("cannot write %s: handler dependency %s is an empty stub (%d bytes). Reopen that bead and implement it first", relPath, dep, info.Size())
			}
		}
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
	if isClosedImplementBeadPath(townRoot, rig, relPath, v) {
		return nil
	}
	return fmt.Errorf("read only files under %s from required_files or dependency packages (active bead %s)",
		strings.Trim(v.LayoutRoot, "/"), allowedID)
}

func validateImplementWriteScope(townRoot, rig, activeBead, written string, v WorkflowValidation, verifyOutput string, scope ImplementWriteScope) error {
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	if ImplementationQueueGreen(townRoot, rig, v) {
		written = NormalizeBeadPathForLayout(filepath.ToSlash(strings.TrimSpace(written)), v.LayoutRoot)
		// Allow edits when go test fails (e.g. orphaned test file from a previous session).
		// Use compile-only check — the agent needs to fix compilation before sending success.
		if ImplementationModuleCompileOK(rigDir, v.ForActivePhase()) != nil {
			return nil
		}
		allowStubFix := false
		for _, stub := range UnionStubArtifactsOnDisk(rigDir, v) {
			if PathMatchesImplementWrite(written, stub, v.RequiredFiles, v) {
				allowStubFix = true
				break
			}
		}
		// Allow writes during QA rework even when the queue is green (QA sent work back).
		if !allowStubFix && !scope.QAReworkFromQAReview {
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
	if AllowForeignOpenBeadProductionCompileFixForVerifyFailure(townRoot, rig, allowedPath, written, verifyOutput, v) {
		return nil
	}
	if closedOnly, err := ImplementPathHasOnlyClosedBeads(townRoot, rig, written, v); err == nil && closedOnly &&
		!AllowedCorrelatedPackageImplementWrite(allowedPath, written, v) {
		if verifyOutput != "" {
			if reopened, rerr := ReopenClosedBeadForRework(townRoot, rig, written, v); rerr != nil {
				return rerr
			} else if reopened != "" {
				return nil
			}
		}
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
	if hint := ImplementWriteScopeVerifyHint(verifyOutput, v); hint != "" {
		return fmt.Errorf("%w — %s", NewImplementWriteScopeError(townRoot, rig, allowedID, allowedPath, written, v), hint)
	}
	return NewImplementWriteScopeError(townRoot, rig, allowedID, allowedPath, written, v)
}

// requiredFilesBeforePath returns required_files that come before relPath in build order
// (dependencies that cmd/server/main.go would import).
func requiredFilesBeforePath(relPath string, v WorkflowValidation) []string {
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	ordered := orderedImplementBeadPaths(v)
	var before []string
	score := implementationPathScore(relPath)
	for _, f := range ordered {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if f == "" || f == relPath {
			continue
		}
		if implementationPathScore(f) < score {
			before = append(before, f)
		}
	}
	return before
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

func isClosedImplementBeadPath(townRoot, rig, relPath string, v WorkflowValidation) bool {
	if townRoot == "" || rig == "" || !BeadsDatabaseReady(townRoot, rig) {
		return false
	}
	closed, err := implementBeadsIndexedByPath(townRoot, rig, v, "closed")
	if err != nil || len(closed) == 0 {
		return false
	}
	rel := filepath.ToSlash(NormalizeBeadPathForLayout(strings.TrimSpace(relPath), v.LayoutRoot))
	for path, b := range closed {
		if b.ID == "" {
			continue
		}
		if pathMatchesRequired(rel, []string{path}) {
			return true
		}
	}
	return false
}
