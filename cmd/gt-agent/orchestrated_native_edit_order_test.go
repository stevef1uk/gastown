package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestProcessOrchestratedTools_inProgressBeforeNativeEdit(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	mayor := filepath.Join(dir, rig, "mayor", "rig")
	schemaDir := filepath.Join(mayor, "linkshelf/internal/store")
	if err := os.MkdirAll(schemaDir, 0755); err != nil {
		t.Fatal(err)
	}
	schemaFile := filepath.Join(schemaDir, "schema.go")
	if err := os.WriteFile(schemaFile, []byte("package store\n\nvar schemaVersion = 1\n"), 0644); err != nil {
		t.Fatal(err)
	}

	v := linkshelfImplementValidation("linkshelf/internal/store/schema.go")
	task := implementationTask(t, "wf-order", v.RequiredFiles...)
	r := newStateRunner(task, dir, rig)
	r.hooks.NativeEditTools = true

	orchestrator.ListImplementBeadsByStatusHook = func(_, _ string, _ orchestrator.WorkflowValidation, status string) ([]orchestrator.PlanBead, error) {
		if status == "open" || status == "in_progress" {
			return []orchestrator.PlanBead{{
				ID:    "te-phq",
				Title: "Implement linkshelf/internal/store/schema.go per architecture",
			}}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { orchestrator.ListImplementBeadsByStatusHook = nil })

	// CMD appears after EDIT in the message; pre-native must run bd update before applying EDIT.
	response := `EDIT: linkshelf/internal/store/schema.go
<<<<<<< SEARCH
var schemaVersion = 1
=======
var schemaVersion = 2
>>>>>>> REPLACE
CMD: export BEADS_DIR=x && cd mockrig/mayor/rig && bd update te-phq --status=in_progress
`
	var combined strings.Builder
	_, hadSuccess, _ := r.processOrchestratedTools(response, "sess", &combined)
	if r.track.activeBead != "te-phq" {
		t.Fatalf("activeBead = %q", r.track.activeBead)
	}
	if !hadSuccess {
		t.Fatalf("expected successful EDIT, feedback:\n%s", combined.String())
	}
	data, err := os.ReadFile(schemaFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "schemaVersion = 2") {
		t.Fatalf("EDIT not applied: %s", data)
	}
}
