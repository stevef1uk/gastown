package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
)

// EnsureImplementBeadsAvailable reopens closed implement beads only when polecat work is incomplete
// (missing/stub files). It does not reopen when all implement beads are closed and disk work is ready.
func EnsureImplementBeadsAvailable(townRoot, rig string, v WorkflowValidation) ([]string, error) {
	open, err := listImplementBeadsByStatus(townRoot, rig, v, "open")
	if err != nil {
		return nil, err
	}
	if len(open) > 0 {
		return nil, nil
	}
	inProgress, err := listImplementBeadsByStatus(townRoot, rig, v, "in_progress")
	if err != nil {
		return nil, err
	}
	if len(inProgress) > 0 {
		return nil, nil
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	if ImplementationDiskWorkReady(rigDir, v) == nil {
		if err := ImplementationModuleCompileOK(rigDir, v); err != nil {
			return reopenClosedImplementBeads(townRoot, rig, v)
		}
		return nil, nil
	}
	return reopenClosedImplementBeads(townRoot, rig, v)
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

	open, err := listImplementBeadsByStatus(townRoot, rig, v, "open")
	if err != nil {
		return nil, err
	}
	if len(open) > 0 && len(stubFiles) == 0 {
		// Open implement work exists and only tests failed — leave beads as-is.
		return nil, nil
	}

	return reopenClosedImplementBeads(townRoot, rig, v)
}

// qaFailureRequiresImplementationRework reports whether a QA failure summary should reopen implement beads.
func qaFailureRequiresImplementationRework(summary string) bool {
	lower := strings.ToLower(strings.TrimSpace(summary))
	if lower == "" {
		return false
	}
	for _, needle := range []string{
		"stub", "pytest", "unittest", "reopen", "syntax", "import", "failed",
		"404", "405", "/api/", "null not", "not []", "smoke failed", "smoke test failed",
		"curl", "route", "http status", "returned 4", "returned 5",
		"method not allowed", "address already in use", "bd list", "exit status 127",
		"command not found", "verification", "verify failed", "web assets", "not served",
		"imports must appear", "expected declaration", "found db", "setup failed",
		"compile/test failed", "module compile",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

// ImplementationDiskWorkReady reports nil when active-phase required_files exist and are not stubs.
func ImplementationDiskWorkReady(rigDir string, v WorkflowValidation) error {
	v = v.ForActivePhase()
	for _, rel := range v.RequiredFiles {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" {
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
	}
	var polecatRequired []string
	for _, rel := range v.RequiredFiles {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" || IsProjectSetupArtifactPath(rel, v) {
			continue
		}
		polecatRequired = append(polecatRequired, rel)
	}
	if len(polecatRequired) > 0 {
		vStub := v
		vStub.RequiredFiles = polecatRequired
		if stubs := stubbedRequiredFiles(rigDir, vStub); len(stubs) > 0 {
			return fmt.Errorf("stub or invalid: %s", strings.Join(stubs, ", "))
		}
	}
	return nil
}

func beadImplementationNeedsRework(rigDir, beadPath string, v WorkflowValidation) bool {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	if beadPath == "" {
		return true
	}
	path := filepath.Join(rigDir, filepath.FromSlash(beadPath))
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		return true
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	opts := StubCheckOptionsFromValidation(v)
	if err := CheckContentNotStub(data, beadPath, opts); err != nil {
		return true
	}
	if err := CheckPythonSourceValid(data, beadPath); err != nil {
		return true
	}
	return false
}

func reopenClosedImplementBeads(townRoot, rig string, v WorkflowValidation) ([]string, error) {
	closed, err := listImplementBeadsByStatus(townRoot, rig, v, "closed")
	if err != nil {
		return nil, err
	}
	if len(closed) == 0 {
		return nil, nil
	}
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	workDir := rigDir
	var reopened []string
	for _, b := range closed {
		if b.ID == "" {
			continue
		}
		p := resolveImplementBeadPath(b.Title, v)
		if IsProjectSetupArtifactPath(p, v) {
			continue
		}
		if !beadImplementationNeedsRework(rigDir, p, v) {
			continue
		}
		cmd := exec.Command("bd", "update", b.ID, "--status=open")
		cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
		cmd.Dir = workDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return reopened, fmt.Errorf("bd update %s --status=open: %w: %s", b.ID, err, strings.TrimSpace(string(out)))
		}
		reopened = append(reopened, b.ID)
	}
	return reopened, nil
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
		if err := CheckContentNotStub(data, rel, opts); err != nil {
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
		if !MatchesImplementBeadTitle(title, v) {
			continue
		}
		result = append(result, PlanBead{ID: id, Title: title})
	}
	return result, nil
}
