package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestRewriteQALayoutVerifyCommand_backwardsCD(t *testing.T) {
	v := orchestrator.WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go mod download",
	}
	cmd := "cd linkshelf && cd testgt3/mayor/rig && go mod download"
	fixed, ok := rewriteQALayoutVerifyCommand(cmd, "testgt3", v)
	if !ok {
		t.Fatal("expected rewrite")
	}
	if fixed != "cd testgt3/mayor/rig/linkshelf && go mod download" {
		t.Fatalf("got %q", fixed)
	}
}

func TestRewriteQALayoutVerifyCommand_layoutFromTownRoot(t *testing.T) {
	v := orchestrator.WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go mod download",
	}
	cmd := "cd linkshelf && go mod download"
	fixed, ok := rewriteQALayoutVerifyCommand(cmd, "testgt3", v)
	if !ok {
		t.Fatal("expected rewrite")
	}
	if fixed != "cd testgt3/mayor/rig/linkshelf && go mod download" {
		t.Fatalf("got %q", fixed)
	}
}

func TestPendingQAReworkShouldNotBlockPolecat_shellError(t *testing.T) {
	dir := t.TempDir()
	rig := "rig"
	writeLinkshelfGoModOnly(t, dir, rig)
	v := orchestrator.WorkflowValidation{
		LayoutRoot:        "linkshelf",
		BeadTitleContains: "Implement linkshelf/",
		RequiredFiles:     []string{"linkshelf/go.mod"},
		QAVerifyCommand:   "cd linkshelf && go mod download",
		DeliveryPhases: []orchestrator.DeliveryPhase{
			{ID: "go-module", RequiredFiles: []string{"linkshelf/go.mod"}, QAVerifyCommand: "cd linkshelf && go mod download"},
		},
		ActivePhaseIDField: "go-module",
	}
	r := &stateRunner{
		townRoot: dir,
		rig:      rig,
		v:        v,
		task: &orchestrator.Task{
			State: "implementation",
			PendingRework: &orchestrator.WorkflowRework{
				FromState: "qa_review",
				Summary:   "exit status 2, can't cd to linkshelf",
				Feedback:  "Closed implement beads from QA:\n✓ te-qrb",
			},
		},
		track: &cmdTracker{},
	}
	if !r.pendingQAReworkShouldNotBlockPolecat() {
		t.Fatal("spurious QA shell error with green phase verify should not block polecat")
	}
}

func writeLinkshelfGoModOnly(t *testing.T, townRoot, rig string) string {
	t.Helper()
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	modDir := filepath.Join(rigDir, "linkshelf")
	if err := os.MkdirAll(modDir, 0755); err != nil {
		t.Fatal(err)
	}
	body := "module linkshelf\n\ngo 1.22\n\nrequire github.com/mattn/go-sqlite3 v1.14.22\n"
	if err := os.WriteFile(filepath.Join(modDir, "go.mod"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return rigDir
}
