package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
)

// bdUpdateImplementBeadStatusHook is set by tests to avoid calling bd update.
var bdUpdateImplementBeadStatusHook func(townRoot, rig, beadID, status string) error

// EnsureImplementBeadsAvailable reopens closed implement beads only when polecat work is incomplete
// (missing/stub files). It does not reopen when all implement beads are closed and disk work is ready.
func EnsureImplementBeadsAvailable(townRoot, rig string, v WorkflowValidation) ([]string, error) {
	open, err := listImplementBeadsForGuard(townRoot, rig, v, "open")
	if err != nil {
		return nil, err
	}
	if len(open) > 0 {
		return nil, nil
	}
	inProgress, err := listImplementBeadsForGuard(townRoot, rig, v, "in_progress")
	if err != nil {
		return nil, err
	}
	if len(inProgress) > 0 {
		return nil, nil
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	if ImplementationDiskWorkReady(rigDir, v) == nil {
		// Queue is idle (no open/in_progress) and required files exist on disk.
		// Do not reopen closed beads here: pre_run runs every fetch_task and would undo
		// finished work when runtime smoke fails while unit tests pass. Phase verify
		// (including smoke) still runs on implementation success JSON in gt-agent.
		return nil, nil
	}
	return reopenClosedImplementBeads(townRoot, rig, v)
}

// QAReopenedBeadIDs returns the IDs of beads that were reopened due to a pending QA failure
// rework. During QA rework the reconcile step must not auto-close these beads — the polecat
// needs them open so the implement_bead_context injector shows the SPEC contract to fix.
func QAReopenedBeadIDs(townRoot, rig string) []string {
	if townRoot == "" || rig == "" {
		return nil
	}
	snap, err := LoadInstancesSnapshot(townRoot)
	if err != nil || snap == nil {
		return nil
	}
	for _, inst := range snap.Instances {
		if inst.TemplateID != "rig-flow" {
			continue
		}
		if inst.Variables == nil || inst.Variables["rig"] != rig {
			continue
		}
		rw := inst.PendingRework
		if rw == nil || rw.FromState != "qa_review" {
			return nil
		}
		prefix, _ := RigIssuePrefix(townRoot, rig)
		known, _, _ := ListRigBeadIDSet(townRoot, rig)
		return ExtractKnownRigBeadIDsFromSummary(rw.Summary, prefix, known)
	}
	return nil
}

// ReopenImplementationBeadsAfterQAFailure reopens closed implement beads when QA sends
// polecat back so work can continue without manual bd update.
func ReopenImplementationBeadsAfterQAFailure(townRoot, rig string, v WorkflowValidation, summary string) ([]string, error) {
	if rig == "" || townRoot == "" {
		return nil, nil
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	qaFailed := qaFailureRequiresImplementationRework(summary)

	stubFiles := stubbedRequiredFiles(rigDir, v)
	if !qaFailed && len(stubFiles) == 0 {
		return nil, nil
	}

	open, err := listImplementBeadsForGuard(townRoot, rig, v, "open")
	if err != nil {
		return nil, err
	}
	runtimeRework := qaFailed && qaRuntimeFailureSummary(summary)
	var reopened []string

	prefix, _ := RigIssuePrefix(townRoot, rig)
	known, _, _ := ListRigBeadIDSet(townRoot, rig)
	citedIDs := ExtractKnownRigBeadIDsFromSummary(summary, prefix, known)
	if qaFailed && len(citedIDs) > 0 {
		more, err := reopenClosedImplementBeadsForIDs(townRoot, rig, v, citedIDs)
		if err != nil {
			return reopened, err
		}
		reopened = append(reopened, more...)
	}

	if runtimeRework {
		more, err := reopenClosedImplementBeadsForPaths(townRoot, rig, v, implementPathsForRuntimeRework(v))
		if err != nil {
			return reopened, err
		}
		reopened = append(reopened, more...)
	}
	if len(open) > 0 && len(stubFiles) == 0 && !runtimeRework {
		// Open implement work exists and only tests failed — leave beads as-is.
		return dedupeStrings(reopened), nil
	}
	if qaFailed && len(citedIDs) > 0 && len(stubFiles) == 0 {
		// Targeted QA reopen only — avoid reopening the whole queue.
		return dedupeStrings(reopened), nil
	}

	more, err := reopenClosedImplementBeads(townRoot, rig, v)
	if err != nil {
		return reopened, err
	}
	reopened = append(reopened, more...)
	return dedupeStrings(reopened), nil
}

// qaRuntimeFailureSummary reports QA feedback about HTTP/smoke/runtime (not unit-test-only).
func qaRuntimeFailureSummary(summary string) bool {
	lower := strings.ToLower(strings.TrimSpace(summary))
	if lower == "" {
		return false
	}
	for _, needle := range []string{
		"smoke", "404", "405", "curl", "web asset", "/app.js", "/style.css", "/static",
		"runtime", "not served", "http status", "returned 4", "returned 5", "get /",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

// implementPathsForRuntimeRework lists handler + web paths to reopen after QA smoke failure.
func implementPathsForRuntimeRework(v WorkflowValidation) []string {
	seen := map[string]bool{}
	var out []string
	for _, rel := range append(append([]string(nil), v.RequiredFiles...), v.UnionRequiredFiles()...) {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" || seen[rel] {
			continue
		}
		lower := strings.ToLower(rel)
		// Only reopen server/handler beads for smoke failures — frontend files
		// (index.html, app.js, style.css) are not the cause of server smoke failures.
		if isServerOrHandlerPath(lower) {
			seen[rel] = true
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out
}

// isServerOrHandlerPath reports whether a path is a server entrypoint or API handler
// (Go or Python). Frontend files (html, js, css) and test files are excluded.
func isServerOrHandlerPath(lower string) bool {
	// Go server/handler paths
	if strings.Contains(lower, "/api/handlers") || strings.Contains(lower, "/cmd/server/") {
		return true
	}
	// Python server entrypoints
	if strings.HasSuffix(lower, "app.py") || strings.HasSuffix(lower, "main.py") ||
		strings.HasSuffix(lower, "server.py") || strings.Contains(lower, "wsgi") ||
		strings.Contains(lower, "asgi") {
		return true
	}
	// Python API/route handlers
	if strings.Contains(lower, "/api/") && strings.HasSuffix(lower, ".py") {
		return true
	}
	return false
}

func reopenClosedImplementBeadsForIDs(townRoot, rig string, v WorkflowValidation, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	want := map[string]bool{}
	for _, id := range ids {
		id = strings.ToLower(strings.TrimSpace(id))
		if id != "" {
			want[id] = true
		}
	}
	closed, err := listImplementBeadsForGuard(townRoot, rig, v, "closed")
	if err != nil {
		return nil, err
	}
	var reopened []string
	for _, b := range closed {
		if b.ID == "" || !want[strings.ToLower(b.ID)] {
			continue
		}
		if err := bdUpdateImplementBeadStatus(townRoot, rig, b.ID, "open"); err != nil {
			return reopened, err
		}
		// Reset turn count for reopened bead
		if err := resetBeadTurnCount(townRoot, rig, b.ID); err != nil {
			// Non-fatal, just log
		}
		reopened = append(reopened, b.ID)
	}
	return reopened, nil
}

func reopenClosedImplementBeadsForPaths(townRoot, rig string, v WorkflowValidation, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	want := map[string]bool{}
	for _, p := range paths {
		want[filepath.ToSlash(strings.TrimSpace(p))] = true
	}
	closed, err := listImplementBeadsForGuard(townRoot, rig, v, "closed")
	if err != nil {
		return nil, err
	}
	var reopened []string
	for _, b := range closed {
		if b.ID == "" {
			continue
		}
		p := filepath.ToSlash(strings.TrimSpace(resolveImplementBeadPath(b.Title, v)))
		if !want[p] {
			continue
		}
		if err := bdUpdateImplementBeadStatus(townRoot, rig, b.ID, "open"); err != nil {
			return reopened, err
		}
		// Reset turn count for reopened bead
		if err := resetBeadTurnCount(townRoot, rig, b.ID); err != nil {
			// Non-fatal
		}
		reopened = append(reopened, b.ID)
	}
	return reopened, nil
}

func bdUpdateImplementBeadStatus(townRoot, rig, beadID, status string) error {
	if bdUpdateImplementBeadStatusHook != nil {
		return bdUpdateImplementBeadStatusHook(townRoot, rig, beadID, status)
	}
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	cmd := exec.Command("bd", "update", beadID, "--status="+status)
	cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
	cmd.Dir = rigDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("bd update %s --status=%s: %w: %s", beadID, status, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// qaFailureRequiresImplementationRework reports whether a QA failure summary should reopen implement beads.
func qaFailureRequiresImplementationRework(summary string) bool {
	lower := strings.ToLower(strings.TrimSpace(summary))
	if lower == "" {
		return false
	}
	if IsQAAgentShellError(summary) {
		return false
	}
	for _, needle := range []string{
		"stub", "unittest", "reopen", "syntax", "import", "failed",
		"404", "405", "/api/", "null not", "not []", "smoke failed", "smoke test failed",
		"curl", "route", "http status", "returned 4", "returned 5",
		"method not allowed", "address already in use", "bd list", "exit status 127",
		"command not found", "verification", "verify failed", "web assets", "not served",
		"imports must appear", "expected declaration", "found db", "setup failed",
		"compile/test failed", "module compile",
		"go.mod", "go.sum", "directory structure", "not in architecture", "sqlite3",
		"dom", "element id", "mismatch", "inconsistent", "ui break", "violates",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	// pytest mention alone is not a failure — "collected 0 items" / "no tests ran" just means
	// the test bead hasn't been implemented yet.
	if strings.Contains(lower, "pytest") {
		if strings.Contains(lower, "collected 0") || strings.Contains(lower, "no tests") ||
			strings.Contains(lower, "not found:") {
			return false
		}
		return true
	}
	return false
}

// ImplementationDiskWorkReady reports nil when active-phase required_files exist and are not stubs.
func ImplementationDiskWorkReady(rigDir string, v WorkflowValidation) error {
	v = v.ForActivePhase()
	var checkFiles []string
	for _, rel := range v.RequiredFiles {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" || IsProjectSetupArtifactPath(rel, v) || IsPlaceholderFile(rel) {
			continue
		}
		path := filepath.Join(rigDir, filepath.FromSlash(rel))
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("missing %s", rel)
		}
		if info.Size() == 0 {
			return fmt.Errorf("empty %s", rel)
		}
		if strings.HasSuffix(strings.ToLower(rel), "/go.mod") || rel == "go.mod" {
			if err := ValidateGoModFile(rigDir, v); err != nil {
				return err
			}
		}
		checkFiles = append(checkFiles, rel)
	}
	if len(checkFiles) > 0 {
		vStub := v
		vStub.RequiredFiles = checkFiles
		if stubs := stubbedRequiredFiles(rigDir, vStub); len(stubs) > 0 {
			return fmt.Errorf("stub or invalid: %s", strings.Join(stubs, ", "))
		}
	}
	return nil
}

// EnsureOpenImplementBeadForRework reopens a closed implement bead when its artifact still
// needs on-disk fixes (e.g. concatenated app.js after a premature bd close).
func EnsureOpenImplementBeadForRework(townRoot, rig, filePath string, v WorkflowValidation) (string, error) {
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	filePath = filepath.ToSlash(strings.TrimSpace(filePath))
	if filePath == "" || !beadImplementationNeedsRework(rigDir, filePath, v) {
		return "", nil
	}
	id, ok := ClosedImplementBeadForPath(townRoot, rig, filePath, v)
	if !ok {
		return "", nil
	}
	if err := bdUpdateImplementBeadStatus(townRoot, rig, id, "open"); err != nil {
		// Best-effort reopen: when bd is unavailable, callers still enforce closed-bead guards.
		if bdUpdateImplementBeadStatusHook != nil {
			return "", err
		}
		return "", nil
	}
	return id, nil
}

// ReopenClosedBeadForRework reopens a closed implement bead that owns the given path.
// Unlike EnsureOpenImplementBeadForRework, this ignores the on-disk file state and
// always reopens when a closed bead owns the path. It is intended for scenarios where
// verify has failed and the LLM needs to edit the source code to fix tests — even if
// the source file is not structurally broken.
func ReopenClosedBeadForRework(townRoot, rig, filePath string, v WorkflowValidation) (string, error) {
	filePath = filepath.ToSlash(strings.TrimSpace(filePath))
	if filePath == "" {
		return "", nil
	}
	id, ok := ClosedImplementBeadForPath(townRoot, rig, filePath, v)
	if !ok {
		return "", nil
	}
	if err := bdUpdateImplementBeadStatus(townRoot, rig, id, "open"); err != nil {
		if bdUpdateImplementBeadStatusHook != nil {
			return "", err
		}
		return "", nil
	}
	return id, nil
}

func beadImplementationNeedsRework(rigDir, beadPath string, v WorkflowValidation) bool {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	if beadPath == "" {
		return true
	}
	// Placeholder files (.gitkeep, .gitignore, .keep) are intentionally small — never rework.
	if IsPlaceholderFile(beadPath) {
		return false
	}
	path := filepath.Join(rigDir, filepath.FromSlash(beadPath))
	info, err := os.Stat(path)
	if err != nil {
		return true
	}
	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil || len(entries) == 0 {
			return true
		}
		return false
	}
	if info.Size() == 0 {
		return true
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	opts := optsForPath(beadPath, StubCheckOptionsFromValidation(v))
	if err := CheckContentNotStub(data, beadPath, opts); err != nil {
		return true
	}
	if err := CheckPythonSourceValid(data, beadPath); err != nil {
		return true
	}
	if strings.HasSuffix(strings.ToLower(beadPath), ".js") {
		if err := CheckJavaScriptFileHealthy(data); err != nil {
			return true
		}
	}
	return false
}

func reopenClosedImplementBeads(townRoot, rig string, v WorkflowValidation) ([]string, error) {
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	eval := newImplementBeadVerifyEvaluator(rigDir, v)
	return reopenClosedImplementBeadsOrdered(townRoot, rig, v, eval)
}

func stubbedRequiredFiles(rigDir string, v WorkflowValidation) []string {
	opts := StubCheckOptionsFromValidation(v)
	var stubbed []string
	for _, rel := range v.RequiredFiles {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" {
			continue
		}
		path := filepath.Join(rigDir, filepath.FromSlash(rel))
		data, err := os.ReadFile(path)
		if err != nil {
			stubbed = append(stubbed, rel+" (missing)")
			continue
		}
		if err := CheckContentNotStub(data, rel, optsForPath(rel, opts)); err != nil {
			stubbed = append(stubbed, rel)
		}
		if err := CheckPythonSourceValid(data, rel); err != nil {
			stubbed = append(stubbed, rel)
		}
	}
	return stubbed
}

func listImplementBeadsByStatus(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error) {
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	args := beads.InjectFlatForListJSON([]string{"list", "--status=" + status, "--json", "--limit=0"})
	cmd := exec.Command("bd", args...)
	cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("bd list %s: %w: %s", status, err, strings.TrimSpace(string(out)))
	}
	out = beads.StripStdoutWarnings(out)
	var rows []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, err
	}
	var result []PlanBead
	for _, r := range rows {
		id := strings.TrimSpace(beads.ExtractIssueID(r.ID))
		title := strings.TrimSpace(r.Title)
		if id == "" {
			continue
		}
		// For listing open/in-progress beads we want implement-like titles, not
		// required_files membership filtering. Required/extra classification is
		// validated elsewhere (plan.md bead validation, plan sync alignment, etc.).
		if !looksLikeOpenImplementBeadTitle(title, v) {
			continue
		}
		result = append(result, PlanBead{ID: id, Title: title})
	}
	return result, nil
}
