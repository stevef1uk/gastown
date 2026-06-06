package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func writePhasedLinkshelfProfile(t *testing.T, townRoot, rig string) {
	t.Helper()
	dir := filepath.Join(townRoot, rig, "mayor", "rig", ".gastown")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	v := orchestrator.WorkflowValidation{
		LayoutRoot:         "linkshelf",
		BeadTitleContains:  "Implement linkshelf/",
		QAVerifyCommand:    "cd linkshelf && go test ./...",
		ActivePhaseIDField: "backend-core",
		RequiredFiles: []string{
			"linkshelf/go.mod",
			"linkshelf/internal/store/schema.go",
			"linkshelf/internal/store/store.go",
			"linkshelf/cmd/server/main.go",
			"linkshelf/web/index.html",
		},
		DeliveryPhases: []orchestrator.DeliveryPhase{
			{
				ID: "backend-core",
				RequiredFiles: []string{
					"linkshelf/go.mod",
					"linkshelf/internal/store/schema.go",
					"linkshelf/internal/store/store.go",
				},
				QAVerifyCommand: "cd linkshelf && go test ./internal/store",
			},
			{
				ID: "server-setup",
				RequiredFiles: []string{
					"linkshelf/cmd/server/main.go",
					"linkshelf/web/index.html",
				},
			},
		},
	}
	if err := orchestrator.WriteRigWorkflowProfile(townRoot, rig, v, "test", "high"); err != nil {
		t.Fatal(err)
	}
}

func TestTaskValidation_phasedBackendCoreSkipsQASmoke(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "testgt3"
	writePhasedLinkshelfProfile(t, dir, rig)

	// Simulate fetch_task payload missing delivery_phases (common stale JSON).
	task := &orchestrator.Task{
		Rig: rig,
		Validation: orchestrator.WorkflowValidation{
			LayoutRoot:        "linkshelf",
			BeadTitleContains: "Implement linkshelf/",
			QAVerifyCommand:   "cd linkshelf && go test ./...",
			RequiredFiles: []string{
				"linkshelf/go.mod",
				"linkshelf/internal/store/schema.go",
				"linkshelf/internal/store/store.go",
				"linkshelf/cmd/server/main.go",
				"linkshelf/web/index.html",
			},
		},
	}
	v := taskValidation(dir, task)
	if orchestrator.WorkflowNeedsQARuntimeSmoke(dir, rig, v) {
		t.Fatal("backend-core QA must not require runtime smoke")
	}
}

func TestValidateQACommand_phasedBackendCoreRejectsBareCurl(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "testgt3"
	writePhasedLinkshelfProfile(t, dir, rig)
	v := taskValidation(dir, &orchestrator.Task{Rig: rig})

	err := validateQACommand("curl -s http://127.0.0.1:8080/api/links", rig, dir, v)
	if err == nil {
		t.Fatal("expected bare curl to be rejected during backend-core QA")
	}
	if err.Error() == "" || err.Error() == "Go runtime smoke must include go run ./cmd/server in the same CMD as curl — bare curl with no server running always fails" {
		t.Fatalf("want no-runtime-smoke rejection, got: %v", err)
	}
}
