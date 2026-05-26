package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestCountOpenMatchingBeads_plannerTitleWithoutLayoutPrefix(t *testing.T) {
	orchestrator.ListImplementBeadsByStatusHook = func(_, _ string, _ orchestrator.WorkflowValidation, status string) ([]orchestrator.PlanBead, error) {
		switch status {
		case "in_progress":
			return []orchestrator.PlanBead{{
				ID:    "te-s73",
				Title: "Implement internal/store/schema.go per architecture",
			}}, nil
		case "open":
			return []orchestrator.PlanBead{{
				ID:    "te-4wi",
				Title: "Implement internal/store/store.go per architecture",
			}}, nil
		default:
			return nil, nil
		}
	}
	defer func() { orchestrator.ListImplementBeadsByStatusHook = nil }()

	v := orchestrator.WorkflowValidation{
		LayoutRoot:        "linkshelf",
		BeadTitleContains:   "Implement linkshelf/",
		RequiredFiles:       []string{"linkshelf/internal/store/schema.go", "linkshelf/internal/store/store.go"},
	}
	n, err := countOpenMatchingBeads("", "", v)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("got %d open/in_progress implement beads, want 2", n)
	}
}

func TestCountOpenMatchingBeads_ignoresAgentBeads(t *testing.T) {
	orchestrator.ListImplementBeadsByStatusHook = func(_, _ string, _ orchestrator.WorkflowValidation, status string) ([]orchestrator.PlanBead, error) {
		if status != "open" {
			return nil, nil
		}
		return []orchestrator.PlanBead{
			{ID: "te-qa", Title: "QA for testgt3 - verifies work"},
			{ID: "te-4wi", Title: "Implement internal/store/store.go per architecture"},
		}, nil
	}
	defer func() { orchestrator.ListImplementBeadsByStatusHook = nil }()

	v := orchestrator.WorkflowValidation{
		LayoutRoot:        "linkshelf",
		BeadTitleContains:   "Implement linkshelf/",
		RequiredFiles:       []string{"linkshelf/internal/store/store.go"},
	}
	n, err := countOpenMatchingBeads("", "", v)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("got %d, want 1 implement bead", n)
	}
}

func TestValidateQAArtifacts_allPassedRejectsOpenPlannerTitles(t *testing.T) {
	town := t.TempDir()
	rig := "testgt3"
	schema := filepath.Join(town, rig, "mayor", "rig", "linkshelf", "internal", "store", "schema.go")
	if err := os.MkdirAll(filepath.Dir(schema), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(schema, []byte("package store\n\n// schema holds link metadata.\ntype Schema struct{}\n\nfunc InitSchema() error { return nil }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	countOpenMatchingBeadsHook = func(_, _ string, _ orchestrator.WorkflowValidation) (int, error) {
		return 3, nil
	}
	defer func() { countOpenMatchingBeadsHook = nil }()

	v := orchestrator.WorkflowValidation{
		LayoutRoot:        "linkshelf",
		BeadTitleContains:   "Implement linkshelf/",
		RequiredFiles:       []string{"linkshelf/internal/store/schema.go"},
		QAVerifyCommand:     "cd linkshelf && go test ./internal/store/...",
	}
	err := validateQAArtifacts(town, rig, "all_passed", false, true, true, false, v)
	if err == nil || !strings.Contains(err.Error(), "open implement bead") {
		t.Fatalf("want all_passed blocked, got %v", err)
	}
}
