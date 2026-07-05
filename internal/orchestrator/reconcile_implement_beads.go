package orchestrator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/config"
)

// ValidateBeadArtifactOnDisk reports whether a layout-relative implement path exists and is not stubbed.
func ValidateBeadArtifactOnDisk(rigDir, beadPath string, v WorkflowValidation) error {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	if beadPath == "" {
		return fmt.Errorf("empty bead path")
	}
	if beadImplementationNeedsRework(rigDir, beadPath, v.ForActivePhase()) {
		return fmt.Errorf("artifact %s missing, empty, or stub", beadPath)
	}
	return nil
}

// ReconcileClosedImplementBeads reopens closed implement beads whose files are missing,
// empty, or stubbed. Unlike EnsureImplementBeadsAvailable, this runs even when other
// implement beads are still open (e.g. closed te-rnd while te-avv is in progress).
func ReconcileClosedImplementBeads(townRoot, rig string, v WorkflowValidation) ([]string, error) {
	if rig == "" || townRoot == "" {
		return nil, nil
	}
	if !BeadsDatabaseReady(townRoot, rig) {
		return nil, nil
	}
	v = v.ForActivePhase()
	active, err := ListImplementBeadsOpenOrInProgress(townRoot, rig, v)
	if err == nil && len(active) > 0 {
		// Active phase: Only reopen if beads are missing/stubbed, ignore full-project compile errors.
		rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
		var reopened []string
		closed, err := implementBeadsIndexedByPath(townRoot, rig, v, "closed")
		if err != nil || len(closed) == 0 {
			return nil, err
		}
		for _, rel := range orderedImplementBeadPaths(v) {
			b, ok := closed[filepath.ToSlash(rel)]
			if !ok || IsProjectSetupArtifactPath(rel, v) {
				continue
			}
			if beadImplementationNeedsRework(rigDir, rel, v) {
				if err := bdUpdateImplementBeadStatus(townRoot, rig, b.ID, "open"); err == nil {
					reopened = append(reopened, b.ID)
				}
			}
		}
		return reopened, nil
	}

	if ImplementationQueueGreen(townRoot, rig, v) {
		return nil, nil
	}
	return reopenClosedImplementBeads(townRoot, rig, v)
}

// requiredFileAtOrBeforeQueueHead reports whether rel is the queue head or an earlier required_files entry.
func requiredFileAtOrBeforeQueueHead(rel, headPath string, v WorkflowValidation) bool {
	headPath = filepath.ToSlash(strings.TrimSpace(headPath))
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	if headPath == "" || rel == "" {
		return true
	}
	for _, want := range OrderRequiredFilesForImplementation(v.RequiredFiles) {
		if pathMatchesRequired(want, []string{rel}) {
			return true
		}
		if pathMatchesRequired(want, []string{headPath}) {
			return false
		}
	}
	return false
}

// workflowIsInQAReview reports whether the active rig-flow workflow is in qa_review state.
// When true, reconcile should not auto-reopen QA-cited beads — QA stage means implement
// is done; beads stay in their current state until QA completes (all_passed) or fails.
func workflowIsInQAReview(townRoot, rig string) bool {
	snap, err := LoadInstancesSnapshot(townRoot)
	if err != nil || snap == nil {
		return false
	}
	for _, inst := range snap.Instances {
		if inst.TemplateID != "rig-flow" {
			continue
		}
		if inst.Variables == nil || inst.Variables["rig"] != rig {
			continue
		}
		return inst.CurrentState == "qa_review"
	}
	return false
}

func frontendAutoClosedIDs(ids []string, townRoot, rig string, v WorkflowValidation) []string {
	var out []string
	for _, id := range ids {
		if isFrontendBeadForPath(townRoot, rig, id, v) {
			out = append(out, id)
		}
	}
	return out
}

func isFrontendBeadForPath(townRoot, rig, beadID string, v WorkflowValidation) bool {
	path := ImplementBeadPathForID(townRoot, rig, beadID, v)
	return IsFrontendImplementPath(path)
}

// AuditRequiredImplementFiles reports required_files that are missing, empty, or stubbed on disk.
// When townRoot and rig are set, files after the open queue head are skipped (not implemented yet).
func AuditRequiredImplementFiles(rigDir string, v WorkflowValidation) []string {
	return auditRequiredImplementFiles(rigDir, "", "", v, nil)
}

// FormatMissingImplementFilesBlock returns prompt text when queue-head required files are absent on disk.
func FormatMissingImplementFilesBlock(townRoot, rig string, v WorkflowValidation) string {
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	issues := auditRequiredImplementFiles(rigDir, townRoot, rig, v.ForActivePhase(), nil)
	if len(issues) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Required implement files missing on disk\n")
	b.WriteString("Pre-run reconcile found artifacts that are **not on disk** (often after an implementation timeout reset removed open/in_progress bead files). ")
	b.WriteString("JSON success does not create files — use **WRITE:** or `CMD:` heredoc in this session, then Verify and `bd close`.\n\n")
	for _, issue := range issues {
		b.WriteString("- ")
		b.WriteString(issue)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func auditRequiredImplementFiles(rigDir, townRoot, rig string, v WorkflowValidation, eval *implementBeadVerifyEvaluator) []string {
	v = v.ForActivePhase()
	headPath := ""
	if townRoot != "" && rig != "" && BeadsDatabaseReady(townRoot, rig) {
		if next, err := NextOpenImplementBead(townRoot, rig, v); err == nil && next != nil {
			headPath = NormalizeBeadPathForLayout(
				ExtractPathFromBeadTitle(next.Title, v.BeadTitleContains), v.LayoutRoot)
		}
	}
	var issues []string
	for _, rel := range orderedImplementBeadPaths(v) {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" {
			continue
		}
		if headPath != "" && !requiredFileAtOrBeforeQueueHead(rel, headPath, v) {
			continue
		}
		if eval != nil && eval.VerifySatisfied(rel) {
			continue
		}
		path := filepath.Join(rigDir, filepath.FromSlash(rel))
		info, err := os.Stat(path)
		if err != nil {
			issues = append(issues, fmt.Sprintf("missing %s", rel))
			continue
		}
		if info.Size() == 0 {
			issues = append(issues, fmt.Sprintf("empty %s", rel))
			continue
		}
		if IsProjectSetupArtifactPath(rel, v) {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			issues = append(issues, fmt.Sprintf("unreadable %s", rel))
			continue
		}
		opts := StubCheckOptionsFromValidation(v)
		if err := CheckContentNotStub(data, rel, opts); err != nil {
			issues = append(issues, fmt.Sprintf("stub %s", rel))
		}
		if err := CheckPythonSourceValid(data, rel); err != nil {
			issues = append(issues, fmt.Sprintf("invalid %s: %v", rel, err))
		}
	}
	return issues
}

// AuditClosedImplementBeadMismatches reports closed beads whose on-disk artifact still needs work.
func AuditClosedImplementBeadMismatches(townRoot, rig string, v WorkflowValidation, eval *implementBeadVerifyEvaluator) ([]string, error) {
	v = v.ForActivePhase()
	closed, err := implementBeadsIndexedByPath(townRoot, rig, v, "closed")
	if err != nil {
		return nil, err
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	var issues []string
	for _, rel := range orderedImplementBeadPaths(v) {
		b, ok := closed[filepath.ToSlash(rel)]
		if !ok {
			continue
		}
		if IsProjectSetupArtifactPath(rel, v) {
			continue
		}
		if eval != nil && eval.VerifySatisfied(rel) {
			continue
		}
		if beadImplementationNeedsRework(rigDir, rel, v) {
			issues = append(issues, fmt.Sprintf("closed %s should not be closed (%s)", b.ID, rel))
		}
	}
	return issues, nil
}

// ReconcileImplementBeads runs a deterministic pipeline for Go rigs (same order every pre_run):
//  1. Auto-close beads whose on-disk file passes profile Verify (go test/build per bead; frontend: non-stub).
//     When phase verify is red, only frontend beads auto-close (open or in_progress); Go beads stay open.
//  2. Audit required_files and closed-bead mismatches (Verify-green paths skip stub heuristics).
//  3. Reopen closed beads that still fail Verify, in profile build order (never reopen Verify-green).
//  4. EnsureImplementBeadsAvailable when the queue is idle and disk work is incomplete.
func ReconcileImplementBeads(townRoot, rig string, v WorkflowValidation) (string, error) {
	if rig == "" || townRoot == "" {
		return "", nil
	}
	v = v.ForActivePhase()
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	eval := newImplementBeadVerifyEvaluator(rigDir, v)
	var parts []string

	// During qa_review, leave beads in their current state. QA verifies and
	// reports pass/fail; implementation rework handles bead changes in its own
	// reconcile cycle. Auto-close/reopen here causes churn with QA failure loops.
	if workflowIsInQAReview(townRoot, rig) {
		return "", nil
	}

	// Deterministic reconcile: close green beads first, then audit, then reopen only what still fails verify.
	// When phase verify is red, still auto-close frontend beads that pass per-bead verify so the queue
	// can advance (e.g. style.css done while handlers.go compile is broken). Go beads stay open until
	// phase verify passes — premature success JSON is still blocked by implementation guards.
	var autoClosed []string
	phaseErr := error(nil)
	if WorkflowNeedsRuntimeSmoke(townRoot, rig, v) {
		phaseErr = ImplementationPhaseVerifyOK(townRoot, rig, v)
	}
	if phaseErr != nil && WorkflowUsesPython(v) && pythonVerifyNeedsVenvRebuild(phaseErr) {
		if logLine, recovered := RecoverPythonVenvAndRetry(townRoot, rig, v, phaseErr); recovered {
			parts = append(parts, logLine)
			phaseErr = ImplementationPhaseVerifyOK(townRoot, rig, v)
		}
	}
	// Always auto-close green go.mod beads (go-module phase) even when full phase verify is red.
	goModClosed, err := CloseGreenGoModBeads(townRoot, rig, v, eval)
	if err != nil {
		return "", err
	}
	if len(goModClosed) > 0 {
		autoClosed = append(autoClosed, goModClosed...)
	}
	// Conservative auto-close: inert frontend/config beads (empty or stubbed files)
	// This helps rigs progress past trivial frontend placeholders while leaving
	// implementation queue semantics intact for core code.
	inertClosed, cerr := CloseInertImplementBeads(townRoot, rig, v)
	if cerr != nil {
		return "", cerr
	}
	if len(inertClosed) > 0 {
		autoClosed = append(autoClosed, inertClosed...)
	}

	if phaseErr == nil {
		var err error
		moreClosed, err := CloseImplementBeadsWithGreenGoVerify(townRoot, rig, v, eval)
		if err != nil {
			return "", err
		}
		autoClosed = append(autoClosed, moreClosed...)
	} else {
		parts = append(parts, fmt.Sprintf("skipped Go auto-close: phase verify not green (%v)", phaseErr))
		// Runtime smoke failure reopens handler/web beads — auto-closing frontend first causes
		// close/reopen churn every pre_run and blocks polecat from fixing closed paths.
		if !ImplementationVerifyNeedsRuntimeRework(phaseErr) {
			var err error
			autoClosed, err = CloseImplementBeadsWithGreenFrontendVerify(townRoot, rig, v, eval)
			if err != nil {
				return "", err
			}
		} else {
			parts = append(parts, "skipped frontend auto-close: runtime smoke rework pending")
		}
	}
	if len(autoClosed) > 0 {
		label := "auto-closed (verify green)"
		if phaseErr != nil {
			label = "auto-closed frontend (verify green)"
		}
		// During QA rework, reopen beads that QA explicitly cited so the polecat
		// sees them as open and the implement_bead_context injector shows the SPEC contract.
		// Only during implementation rework (not qa_review itself) — once QA stage is
		// reached, beads stay closed; QA verifies, implementation fixes in its own cycle.
		qaIDs := QAReopenedBeadIDs(townRoot, rig)
		if len(qaIDs) > 0 && !workflowIsInQAReview(townRoot, rig) {
			qaSet := make(map[string]bool, len(qaIDs))
			for _, id := range qaIDs {
				qaSet[strings.TrimSpace(id)] = true
			}
			remaining := autoClosed[:0]
			for _, closedID := range autoClosed {
				if qaSet[closedID] {
					if err := bdUpdateImplementBeadStatus(townRoot, rig, closedID, "open"); err != nil {
						parts = append(parts, fmt.Sprintf("could not reopen QA bead %s: %v", closedID, err))
						remaining = append(remaining, closedID)
						continue
					}
					parts = append(parts, "reopened for QA rework: "+closedID)
					continue
				}
				remaining = append(remaining, closedID)
			}
			autoClosed = remaining
		}
		if len(autoClosed) > 0 {
			parts = append(parts, label+": "+joinStrings(autoClosed, ", "))
		}
	}
	// Reopen frontend beads that were auto-closed but fail web artifact consistency checks.
	// This prevents the close/reopen cycle when QA rejects frontend work for static path/DOM ID
	// mismatches but reconcile immediately re-closes the beads on the next pre_run.
	if len(autoClosed) > 0 && len(frontendAutoClosedIDs(autoClosed, townRoot, rig, v)) > 0 {
		if err := validateFrontendArtifactConsistency(townRoot, rig, v); err != nil {
			var reopened []string
			for _, id := range autoClosed {
				if isFrontendBeadForPath(townRoot, rig, id, v) {
					if rerr := bdUpdateImplementBeadStatus(townRoot, rig, id, "open"); rerr == nil {
						reopened = append(reopened, id)
					}
				}
			}
			if len(reopened) > 0 {
				parts = append(parts, "reopened frontend (artifact mismatch): "+joinStrings(reopened, ", "))
			}
		}
	}
	if phaseErr != nil && ImplementationVerifyNeedsRuntimeRework(phaseErr) {
		reopened, rerr := ReopenImplementationBeadsAfterSmokeFailure(townRoot, rig, v, phaseErr)
		if rerr != nil {
			return joinStrings(parts, "; "), rerr
		}
		if len(reopened) > 0 {
			parts = append(parts, "reopened (phase verify blocked): "+joinStrings(reopened, ", "))
		}
	}

	for _, issue := range auditRequiredImplementFiles(rigDir, townRoot, rig, v, eval) {
		parts = append(parts, issue)
	}
	mismatches, err := AuditClosedImplementBeadMismatches(townRoot, rig, v, eval)
	if err != nil {
		return joinStrings(parts, "; "), err
	}
	for _, m := range mismatches {
		parts = append(parts, m)
	}

	reopened, err := reopenClosedImplementBeadsOrdered(townRoot, rig, v, eval)
	if err != nil {
		return joinStrings(parts, "; "), err
	}
	if len(reopened) > 0 {
		parts = append(parts, "reopened: "+joinStrings(reopened, ", "))
	}

	more, err := EnsureImplementBeadsAvailable(townRoot, rig, v)
	if err != nil {
		return joinStrings(parts, "; "), err
	}
	for _, id := range more {
		if !containsString(reopened, id) {
			parts = append(parts, "reopened: "+id)
		}
	}
	if len(parts) == 0 {
		return "implement beads and required_files are consistent", nil
	}
	return joinStrings(parts, "; "), nil
}

func containsString(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// CloseInertImplementBeads closes open/in_progress implement beads whose on-disk
// artifact is empty or clearly a trivial stub and where the path is a frontend
// or small config/manifest file. This is conservative: only frontend-like
// artifacts are auto-closed here to avoid hiding missing implementation work.
func CloseInertImplementBeads(townRoot, rig string, v WorkflowValidation) ([]string, error) {
	v = v.ForActivePhase()
	if townRoot == "" || rig == "" {
		return nil, nil
	}
	if !BeadsDatabaseReady(townRoot, rig) {
		return nil, nil
	}
	active, err := ListImplementBeadsOpenOrInProgress(townRoot, rig, v)
	if err != nil || len(active) == 0 {
		return nil, err
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	var closed []string
	for _, b := range active {
		rel := resolveImplementBeadPath(b.Title, v)
		if rel == "" || IsProjectSetupArtifactPath(rel, v) {
			continue
		}
		// Only consider frontend / small assets or dependency manifests for inert auto-close.
		if !IsFrontendImplementPath(rel) && !IsDependencyManifest(rel) {
			continue
		}
		full := filepath.Join(rigDir, filepath.FromSlash(rel))
		info, err := os.Stat(full)
		if err != nil {
			continue
		}
		// Empty file -> inert
		if info.Size() == 0 {
			beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
			cmd := exec.Command("bd", "close", b.ID, "--reason=auto:inert-bead")
			cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
			cmd.Dir = rigDir
			out, err := cmd.CombinedOutput()
			if err == nil {
				closed = append(closed, b.ID)
				continue
			}
			return closed, fmt.Errorf("bd close %s: %w: %s", b.ID, err, strings.TrimSpace(string(out)))
		}
		// Non-empty but stubbed according to heuristics -> inert
		data, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		opts := StubCheckOptionsFromValidation(v)
		if err := CheckContentNotStub(data, rel, optsForPath(rel, opts)); err != nil {
			beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
			cmd := exec.Command("bd", "close", b.ID, "--reason=auto:inert-bead")
			cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
			cmd.Dir = rigDir
			out, err := cmd.CombinedOutput()
			if err == nil {
				closed = append(closed, b.ID)
				continue
			}
			return closed, fmt.Errorf("bd close %s: %w: %s", b.ID, err, strings.TrimSpace(string(out)))
		}
	}
	return closed, nil
}
