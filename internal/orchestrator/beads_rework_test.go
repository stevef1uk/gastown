package orchestrator

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestReopenClosedImplementBeadsForIDs(t *testing.T) {
	dir := t.TempDir()
	rig := "rig"
	v := WorkflowValidation{
		LayoutRoot:        "app",
		BeadTitleContains: "Implement app/",
		RequiredFiles:     []string{"app/web/index.html", "app/web/app.js"},
	}

	var reopened []string
	prevList := ListImplementBeadsByStatusHook
	ListImplementBeadsByStatusHook = func(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error) {
		if status != "closed" {
			return nil, nil
		}
		return []PlanBead{
			{ID: "xy-a3c", Title: "Implement app/web/index.html per architecture"},
			{ID: "xy-0l6", Title: "Implement app/web/app.js per architecture"},
			{ID: "xy-e4d", Title: "Implement app/internal/api/handlers.go"},
		}, nil
	}
	defer func() { ListImplementBeadsByStatusHook = prevList }()

	prevUpdate := bdUpdateImplementBeadStatusHook
	bdUpdateImplementBeadStatusHook = func(townRoot, rig, id, status string) error {
		if status == "open" {
			reopened = append(reopened, id)
		}
		return nil
	}
	defer func() { bdUpdateImplementBeadStatusHook = prevUpdate }()

	got, err := reopenClosedImplementBeadsForIDs(dir, rig, v, []string{"xy-a3c", "xy-0l6"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || reopened[0] != "xy-a3c" || reopened[1] != "xy-0l6" {
		t.Fatalf("reopened=%v", reopened)
	}
}

func TestQAFailureRequiresImplementationRework_domMismatch(t *testing.T) {
	summary := "Beads te-0l6 and te-a3c have inconsistent DOM element IDs, causing the UI to break"
	if !qaFailureRequiresImplementationRework(summary) {
		t.Fatal("DOM/UI mismatch should trigger implementation rework")
	}
}

func TestExtractKnownRigBeadIDsFromSummary_usesRigPrefix(t *testing.T) {
	known := map[string]bool{"te-0l6": true, "te-a3c": true}
	summary := `app.js (bead te-0l6) references id "link-list" but index.html (te-a3c) defines "links"`
	got := ExtractKnownRigBeadIDsFromSummary(summary, "te", known)
	if len(got) != 2 {
		t.Fatalf("got %v want te-0l6 and te-a3c", got)
	}
	if strings.Contains(strings.Join(got, ","), "link-list") {
		t.Fatalf("must not treat link-list as bead id: %v", got)
	}
}

// TestReopenImplementationBeadsAfterTestFailure_reopensCitedAndStubTestBeads covers
// the tester→polecat bounce: closed implement beads must reopen when the tester
// summary cites them OR when their planned test files are missing/stub on disk.
// Without this reopen the polecat re-enters implementation with an empty queue,
// verify trivially passes, and the workflow loops test_review ↔ implementation.
func TestReopenImplementationBeadsAfterTestFailure_reopensCitedAndStubTestBeads(t *testing.T) {
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd not in PATH")
	}
	dir := t.TempDir()
	rig := "rig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	beadsDir := filepath.Join(dir, rig, ".beads")
	if err := os.MkdirAll(rigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 0700 matches production rig .beads permissions; a looser mode makes bd emit a
	// warning that pollutes command output parsed by RigIssuePrefix in the reopen path.
	if err := os.MkdirAll(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	bdEnv := append(os.Environ(), "BEADS_DIR="+beadsDir)
	runBD := func(args ...string) string {
		cmd := exec.Command("bd", args...)
		cmd.Env = bdEnv
		cmd.Dir = rigDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("bd %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	runBD("init")

	testFile := "linkshelf/internal/api/handlers_test.go"
	e2eFile := "linkshelf/tests/e2e.spec.js"
	if err := os.MkdirAll(filepath.Join(rigDir, "linkshelf", "internal", "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	placeholder := "package api\n\nimport \"testing\"\n\n// Replace with table-driven tests from plan.md acceptance.\nfunc TestPlaceholder(t *testing.T) {\n\tt.Skip(\"implement tests per plan.md acceptance\")\n}\n"
	if err := os.WriteFile(filepath.Join(rigDir, filepath.FromSlash(testFile)), []byte(placeholder), 0o644); err != nil {
		t.Fatal(err)
	}
	testPlan := strings.Join([]string{
		"# TEST_PLAN",
		"",
		"### req-handlers",
		"Level: unit",
		"Test file: " + testFile,
		"Bead ID: te-handlers",
		"",
		"### req-e2e",
		"Level: e2e",
		"Test file: " + e2eFile,
		"Bead ID: te-e2e",
		"",
	}, "\n")
	if err := os.WriteFile(TestPlanPath(rigDir), []byte(testPlan), 0o644); err != nil {
		t.Fatal(err)
	}

	createBead := func(path string) string {
		out := runBD("create", "--title=Implement "+path+" per TEST_PLAN", "--type=task", "--json")
		start := strings.Index(out, "{")
		if start < 0 {
			t.Fatalf("bd create --json produced no JSON object:\n%s", out)
		}
		var resp struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(out[start:]), &resp); err != nil || resp.ID == "" {
			t.Fatalf("bd create --json: %v\n%s", err, out)
		}
		return resp.ID
	}
	handlersID := createBead(testFile)
	e2eID := createBead(e2eFile)
	runBD("close", handlersID)
	runBD("close", e2eID)

	v := WorkflowValidation{
		LayoutRoot:        "linkshelf",
		BeadTitleContains: "Implement ",
		RequiredFiles:     []string{"linkshelf/internal/api/handlers.go"},
	}

	summary := "handlers_test.go is a stub — reopen bead " + handlersID + "; " + e2eFile + " missing entirely (" + e2eID + ")"
	reopened, err := ReopenImplementationBeadsAfterTestFailure(dir, rig, v, summary)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(reopened)
	want := []string{e2eID, handlersID}
	sort.Strings(want)
	if !reflect.DeepEqual(reopened, want) {
		t.Fatalf("reopened=%v want %v", reopened, want)
	}
	var openList struct {
		IDs []string
	}
	out := runBD("list", "--status=open", "--json", "--limit=0")
	var beads []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &beads); err != nil {
		t.Fatalf("bd list --json: %v\n%s", err, out)
	}
	for _, b := range beads {
		openList.IDs = append(openList.IDs, b.ID)
	}
	for _, id := range want {
		found := false
		for _, got := range openList.IDs {
			if got == id {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("bead %s should be open after tester failure rework; open=%v", id, openList.IDs)
		}
	}
}
