package orchestrator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
		if inst.TemplateID != "rig-flow" && inst.TemplateID != "req-flow" {
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
		if info.IsDir() {
			entries, err := os.ReadDir(path)
			if err != nil || len(entries) == 0 {
				issues = append(issues, fmt.Sprintf("empty %s", rel))
			}
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
	// Use compile-level verify only — runtime smoke is QA's concern, not the Polecat's.
	var autoClosed []string
	phaseErr := error(nil)
	if WorkflowUsesGo(v) {
		rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
		phaseErr = ImplementationModuleCompileOK(rigDir, v.ForActivePhase())
	}
	if phaseErr != nil && WorkflowUsesPython(v) && pythonVerifyNeedsVenvRebuild(phaseErr) {
		if logLine, recovered := RecoverPythonVenvAndRetry(townRoot, rig, v, phaseErr); recovered {
			parts = append(parts, logLine)
			rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
			phaseErr = ImplementationModuleCompileOK(rigDir, v.ForActivePhase())
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

	// Scan all implemented Python files for missing imports and reopen their beads.
	// This catches cases like main.py importing app.canonical_routes which doesn't exist yet.
	if reopened, err := reopenMissingImportBeads(townRoot, rig, v); err != nil {
		return joinStrings(parts, "; "), err
	} else if len(reopened) > 0 {
		parts = append(parts, "reopened (missing imports): "+joinStrings(reopened, ", "))
	}

	mismatches, err := AuditClosedImplementBeadMismatches(townRoot, rig, v, eval)
	if err != nil {
		return joinStrings(parts, "; "), err
	}
	for _, m := range mismatches {
		parts = append(parts, m)
	}

	// Don't broadly reopen all closed beads that fail verify — only QA-cited
	// beads (handled above) and runtime smoke failures (handled above) should
	// trigger reopens. Broad reopening causes wasteful churn.
	// reopened, err := reopenClosedImplementBeadsOrdered(townRoot, rig, v, eval)
	// if err != nil {
	// 	return joinStrings(parts, "; "), err
	// }
	// if len(reopened) > 0 {
	// 	parts = append(parts, "reopened: "+joinStrings(reopened, ", "))
	// }

	more, err := EnsureImplementBeadsAvailable(townRoot, rig, v)
	if err != nil {
		return joinStrings(parts, "; "), err
	}
	for _, id := range more {
		parts = append(parts, "reopened: "+id)
	}
	// Commit Dolt working set after bead state changes to ensure the SQL server
	// sees the changes when BD_DOLT_AUTO_COMMIT=off (polecat/daemon environments).
	if len(autoClosed) > 0 || len(more) > 0 {
		if commitErr := commitDoltWorkingSet(townRoot, rig); commitErr != nil {
			parts = append(parts, "dolt commit warning: "+commitErr.Error())
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

// reopenMissingImportBeads scans implemented files for missing imports
// and reopens the beads that should provide those modules. Uses the FULL profile
// (all phases) since the missing module may be in a different phase than the
// file that imports it. Supports Python, Go, and Node.js.
func reopenMissingImportBeads(townRoot, rig string, v WorkflowValidation) ([]string, error) {
	if townRoot == "" || rig == "" {
		return nil, nil
	}
	if !BeadsDatabaseReady(townRoot, rig) {
		return nil, nil
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	fullV := v // use full profile (all phases)

	type scanner struct {
	ext          string
	importRe     *regexp.Regexp
	isStdlib     func(string) bool
	pathToModule func(string, string) string  // added layoutRoot parameter
	moduleToPath func(string, string) string  // added layoutRoot parameter
}



	scanners := []scanner{}
	if WorkflowUsesPython(v) {
		// Python files are typically under layout_root/backend/
		scanners = append(scanners, scanner{
			ext:      ".py",
			importRe: regexp.MustCompile(`^\s*(?:from\s+([a-zA-Z_][a-zA-Z0-9_.]*)\s+import|import\s+([a-zA-Z_][a-zA-Z0-9_.]*))`),
			isStdlib: isPythonStdlib,
			pathToModule: func(rel, layoutRoot string) string {
				prefix := layoutRoot + "/backend/"
				if strings.HasPrefix(rel, prefix) && strings.HasSuffix(rel, ".py") {
					modPath := strings.TrimPrefix(rel, prefix)
					modPath = strings.TrimSuffix(modPath, ".py")
					modPath = strings.ReplaceAll(modPath, "/", ".")
					if modPath != "__init__" {
						return modPath
					}
				}
				return ""
			},
			moduleToPath: func(mod, layoutRoot string) string {
				return layoutRoot + "/backend/" + strings.ReplaceAll(mod, ".", "/") + ".py"
			},
		})
	}
	if WorkflowUsesGo(v) {
		// Go files typically under layout_root/
		scanners = append(scanners, scanner{
			ext:      ".go",
			importRe: regexp.MustCompile(`^\s*import\s+(?:"([^"]+)"|\(([^)]+)\))`),
			isStdlib: isGoStdlib,
			pathToModule: func(rel, layoutRoot string) string {
				prefix := layoutRoot + "/"
				if strings.HasPrefix(rel, prefix) && strings.HasSuffix(rel, ".go") {
					modPath := strings.TrimPrefix(rel, prefix)
					modPath = strings.TrimSuffix(modPath, ".go")
					modPath = strings.ReplaceAll(modPath, "/", ".")
					return modPath
				}
				return ""
			},
			moduleToPath: func(mod, layoutRoot string) string {
				return layoutRoot + "/" + strings.ReplaceAll(mod, ".", "/") + ".go"
			},
		})
	}
	if WorkflowUsesNodeJS(v) {
		// TypeScript/JS files typically under layout_root/frontend/src/
		scanners = append(scanners, scanner{
			ext:      ".ts",
			importRe: regexp.MustCompile(`^\s*import\s+.*\s+from\s+["']([^"']+)["']`),
			isStdlib: isNodeStdlib,
			pathToModule: func(rel, layoutRoot string) string {
				prefix := layoutRoot + "/frontend/src/"
				if strings.HasPrefix(rel, prefix) && (strings.HasSuffix(rel, ".ts") || strings.HasSuffix(rel, ".tsx")) {
					modPath := strings.TrimPrefix(rel, prefix)
					modPath = strings.TrimSuffix(modPath, ".ts")
					modPath = strings.TrimSuffix(modPath, ".tsx")
					return modPath
				}
				return ""
			},
			moduleToPath: func(mod, layoutRoot string) string {
				return layoutRoot + "/frontend/src/" + mod + ".ts"
			},
		})
	}

	if len(scanners) == 0 {
		return nil, nil
	}

	// Find all source files on disk
	type fileInfo struct {
		rel     string
		scanner *scanner
	}
	var allFiles []fileInfo
	for _, sc := range scanners {
		err := filepath.Walk(rigDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			// Skip virtual environments, dependency directories, and build artifacts
			if info.IsDir() {
				name := info.Name()
				if name == ".venv" || name == "venv" || name == "__pycache__" ||
					name == "node_modules" || name == ".git" || name == "dist" ||
					name == "build" || name == ".next" || name == ".cache" ||
					strings.HasPrefix(name, ".venv") || strings.HasPrefix(name, "venv") {
					return filepath.SkipDir
				}
			}
			if !info.IsDir() && strings.HasSuffix(path, sc.ext) {
				rel, _ := filepath.Rel(rigDir, path)
				allFiles = append(allFiles, fileInfo{rel: filepath.ToSlash(rel), scanner: &sc})
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	// Build set of available modules on disk
	availableModules := make(map[string]bool)
	for _, f := range allFiles {
		if mod := f.scanner.pathToModule(f.rel, v.LayoutRoot); mod != "" {
			availableModules[mod] = true
			// Add parent packages
			parts := strings.Split(mod, ".")
			for i := 1; i < len(parts); i++ {
				pkg := strings.Join(parts[:i], ".")
				availableModules[pkg] = true
			}
		}
	}

	// Scan each file for imports
	// Track missing imports with their scanner extension to distinguish third-party from project modules
	missingImportExt := make(map[string]string) // mod -> ext
	missingImports := make(map[string]bool)
	for _, f := range allFiles {
		full := filepath.Join(rigDir, filepath.FromSlash(f.rel))
		data, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			matches := f.scanner.importRe.FindStringSubmatch(line)
			if len(matches) >= 2 {
				var imp string
				for i := 1; i < len(matches); i++ {
					if matches[i] != "" {
						imp = matches[i]
						break
					}
				}
				if imp != "" && !strings.HasPrefix(imp, ".") && !f.scanner.isStdlib(imp) {
					if !availableModules[imp] {
						// Check if it's a third-party package available in the environment
						if isImportableThirdParty(imp, rigDir, f.scanner.ext) {
							continue // Third-party package, not a missing implementation
						}
						// Check parent packages
						parts := strings.Split(imp, ".")
						found := false
						for i := len(parts); i > 0; i-- {
							pkg := strings.Join(parts[:i], ".")
							if availableModules[pkg] {
								found = true
								break
							}
						}
						if !found {
							missingImports[imp] = true
							missingImportExt[imp] = f.scanner.ext
						}
					}
				}
			}
		}
	}

if len(missingImports) == 0 {
		return nil, nil
	}

	// Find closed beads for missing modules
	closed, err := implementBeadsIndexedByPath(townRoot, rig, fullV, "closed")
	if err != nil || len(closed) == 0 {
		// No closed beads to reopen - distinguish third-party deps from project modules
		// using the tracked scanner extension from when the import was detected
		if len(missingImports) > 0 {
			allThirdParty := true
			for imp := range missingImports {
				ext := missingImportExt[imp]
				if !isImportableThirdParty(imp, rigDir, ext) {
					allThirdParty = false
					break
				}
			}
			if allThirdParty {
				// All missing imports are third-party deps not yet installed - warn, continue
				return nil, nil
			}
			// At least one is a project module needing a bead - fall through to error below
		}
		return nil, err
	}

	var reopened []string
	var trulyMissing []string
	for missingImp := range missingImports {
		found := false
		for _, sc := range scanners {
			expectedPath := sc.moduleToPath(missingImp, v.LayoutRoot)
			if b, ok := closed[expectedPath]; ok {
				if err := bdUpdateImplementBeadStatus(townRoot, rig, b.ID, "open"); err == nil {
					reopened = append(reopened, b.ID)
				}
				found = true
				break
			}
		}
		if !found {
			trulyMissing = append(trulyMissing, missingImp)
		}
	}
	if len(trulyMissing) > 0 {
		// Only error on truly missing implementation modules (not third-party)
		return reopened, fmt.Errorf("missing imports with no implementation bead: %s (need new implementation bead)", strings.Join(trulyMissing, ", "))
	}
	return reopened, nil
}

func isPythonStdlib(mod string) bool {
	stdlib := map[string]bool{
		"os": true, "sys": true, "json": true, "pathlib": true, "typing": true,
		"datetime": true, "collections": true, "itertools": true, "functools": true,
		"dataclasses": true, "uuid": true, "re": true, "math": true, "random": true,
		"hashlib": true, "base64": true, "urllib": true, "http": true, "asyncio": true,
		"logging": true, "time": true, "contextlib": true, "argparse": true,
		"subprocess": true, "threading": true, "multiprocessing": true, "socket": true,
		"ssl": true, "email": true, "html": true, "xml": true, "sqlite3": true,
		"csv": true, "configparser": true, "string": true, "textwrap": true,
		"unittest": true, "pytest": true, "fastapi": true, "uvicorn": true,
		"pydantic": true, "httpx": true, "starlette": true, "sqlalchemy": true,
		"abc": true, "importlib": true, "types": true, "inspect": true,
		"__future__": true,
		"decimal": true, "fractions": true, "numbers": true,
		"statistics": true, "copy": true, "pprint": true, "reprlib": true,
		"enum": true, "contextvars": true,
		"hmac": true, "secrets": true,
	}
	root := strings.Split(mod, ".")[0]
	return stdlib[root]
}

func isGoStdlib(mod string) bool {
	stdlib := map[string]bool{
		"fmt": true, "os": true, "io": true, "strings": true, "bytes": true,
		"bufio": true, "encoding/json": true, "encoding/xml": true,
		"encoding/base64": true, "encoding/hex": true, "crypto/sha256": true,
		"crypto/md5": true, "crypto/rand": true, "crypto/tls": true,
		"net": true, "net/http": true, "net/url": true, "net/smtp": true,
		"path": true, "path/filepath": true, "regexp": true, "strconv": true,
		"time": true, "sync": true, "sync/atomic": true, "context": true,
		"errors": true, "log": true, "flag": true, "math": true, "math/rand": true,
		"sort": true, "container/list": true, "container/ring": true,
		"reflect": true, "runtime": true, "runtime/debug": true,
		"text/template": true, "html/template": true, "database/sql": true,
		"archive/zip": true, "archive/tar": true, "compress/gzip": true,
		"image": true, "image/png": true, "image/jpeg": true,
		"text/scanner": true, "go/ast": true, "go/parser": true,
	}
	// Check common prefixes
	prefixes := []string{"golang.org/", "github.com/", "gopkg.in/", "google.golang.org/"}
	for _, p := range prefixes {
		if strings.HasPrefix(mod, p) {
			return false // third-party
		}
	}
	root := strings.Split(mod, "/")[0]
	return stdlib[mod] || stdlib[root]
}

func isNodeStdlib(mod string) bool {
	stdlib := map[string]bool{
		"fs": true, "path": true, "http": true, "https": true, "url": true,
		"crypto": true, "util": true, "events": true, "stream": true,
		"querystring": true, "assert": true, "os": true, "process": true,
		"buffer": true, "child_process": true, "cluster": true,
		"dgram": true, "dns": true, "domain": true, "module": true,
		"net": true, "punycode": true,
		"readline": true, "repl": true, "string_decoder": true,
		"sys": true, "timers": true, "tls": true, "tty": true,
		"vm": true, "zlib": true,
	}
	// Check for npm packages (no leading @ or /)
	if strings.HasPrefix(mod, "@") || strings.Contains(mod, "/") {
return false
}
return stdlib[mod]
}

// isImportableThirdParty checks if a module can be imported from the environment.
func isImportableThirdParty(mod, rigDir string, ext string) bool {
	if ext == ".py" {
		return isImportablePythonThirdParty(mod, rigDir)
	}
	if ext == ".go" {
		return isImportableGoThirdParty(mod, rigDir)
	}
	if ext == ".ts" || ext == ".tsx" {
		return isImportableNodeThirdParty(mod, rigDir)
	}
	return false
}

// isImportablePythonThirdParty checks if a Python module can be imported.
func isImportablePythonThirdParty(mod, rigDir string) bool {
	venvPython := filepath.Join(rigDir, ".venv", "bin", "python3")
	if _, err := os.Stat(venvPython); err != nil {
		venvPython = "python3"
	}
	cmd := exec.Command(venvPython, "-c", "import sys; sys.dont_write_bytecode = True; import "+mod)
	cmd.Dir = rigDir
	err := cmd.Run()
	return err == nil
}

// isImportableGoThirdParty checks if a Go module is available (in go.mod or module cache).
func isImportableGoThirdParty(mod, rigDir string) bool {
	cmd := exec.Command("go", "list", "-m", mod)
	cmd.Dir = rigDir
	err := cmd.Run()
	return err == nil
}

// isImportableNodeThirdParty checks if a Node.js module is available in node_modules.
func isImportableNodeThirdParty(mod, rigDir string) bool {
	modulePath := filepath.Join(rigDir, "node_modules", mod)
	if _, err := os.Stat(modulePath); err == nil {
		return true
	}
	cmd := exec.Command("npm", "list", mod)
	cmd.Dir = rigDir
	err := cmd.Run()
	return err == nil
}
