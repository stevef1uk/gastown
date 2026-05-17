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

// ReopenImplementationBeadsAfterQAFailure reopens closed implement beads when QA sends
// polecat back so work can continue without manual bd update.
func ReopenImplementationBeadsAfterQAFailure(townRoot, rig string, v WorkflowValidation, summary string) ([]string, error) {
	if rig == "" || townRoot == "" {
		return nil, nil
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	lower := strings.ToLower(summary)
	qaFailed := strings.Contains(lower, "stub") ||
		strings.Contains(lower, "pytest") ||
		strings.Contains(lower, "unittest") ||
		strings.Contains(lower, "reopen") ||
		strings.Contains(lower, "syntax") ||
		strings.Contains(lower, "import") ||
		strings.Contains(lower, "failed")

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

	closed, err := listImplementBeadsByStatus(townRoot, rig, v, "closed")
	if err != nil {
		return nil, err
	}
	if len(closed) == 0 {
		return nil, nil
	}

	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	workDir := rigDir
	var reopened []string
	for _, b := range closed {
		if b.ID == "" {
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
	prefix := strings.ToLower(strings.TrimSpace(v.BeadTitleContains))
	var result []PlanBead
	for _, r := range rows {
		id := strings.TrimSpace(beads.ExtractIssueID(r.ID))
		title := strings.TrimSpace(r.Title)
		if id == "" || prefix == "" {
			continue
		}
		if !strings.Contains(strings.ToLower(title), prefix) {
			continue
		}
		result = append(result, PlanBead{ID: id, Title: title})
	}
	return result, nil
}
