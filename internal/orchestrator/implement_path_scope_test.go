package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateImplementWritePath_allowsGoModActiveBeadWritingLaterPhaseFile(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	layout := "pingapp"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")

	goModRel := layout + "/go.mod"
	mainRel := layout + "/cmd/server/main.go"
	testRel := layout + "/cmd/server/main_test.go"

	for _, rel := range []string{goModRel, mainRel, testRel} {
		abs := filepath.Join(rigDir, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte("package main\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	v := WorkflowValidation{
		LayoutRoot:        layout,
		BeadTitleContains: "Implement",
		QAVerifyCommand:   "cd pingapp && go test ./...",
		RequiredFiles:     []string{mainRel, testRel},
		DeliveryPhases: []DeliveryPhase{
			{ID: "core", RequiredFiles: []string{goModRel}},
			{ID: "integration-test", RequiredFiles: []string{mainRel, testRel}},
		},
		ActivePhaseIDField: "integration-test",
	}

	setListImplementBeadsByStatusHook(t, dir, rig, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "in_progress" || status == "open" {
			return []PlanBead{{
				ID:    "te-gomod",
				Title: "Implement pingapp/go.mod per architecture",
			}}, nil
		}
		return nil, nil
	})

	err := ValidateImplementWritePath(dir, rig, "te-gomod", mainRel, v, false, "", nil)
	if err != nil {
		t.Fatalf("expected main.go write allowed from go.mod bead (later-phase union file), got: %v", err)
	}
}

func TestValidateImplementWritePath_incrementalEditAllowed(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	rel := "linkshelf/internal/store/store.go"
	abs := filepath.Join(dir, rig, "mayor", "rig", rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		t.Fatal(err)
	}
	body := strings.TrimSpace(`
package store

import "errors"

type Store struct{}

func (s *Store) AddLink(url string) error {
	if url == "" {
		return errors.New("empty url")
	}
	return nil
}
`) + "\n" + strings.Repeat("// padding padding padding\n", 200)
	if err := os.WriteFile(abs, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	v := DefaultWorkflowValidation()
	v.LayoutRoot = "linkshelf"
	v.RequiredFiles = []string{rel}
	setListImplementBeadsByStatusHook(t, dir, rig, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "open" || status == "in_progress" {
			return []PlanBead{{
				ID:    "te-store",
				Title: "Implement linkshelf/internal/store/store.go per architecture",
			}}, nil
		}
		return nil, nil
	})
	if !PreferIncrementalEdit(dir, rig, rel, v) {
		t.Fatal("fixture should prefer incremental edit")
	}
	if err := ValidateImplementWritePath(dir, rig, "te-store", rel, v, false, "", nil); err != nil {
		t.Fatalf("partial: %v", err)
	}
	if err := ValidateImplementWritePath(dir, rig, "te-store", rel, v, true, "", nil); err == nil {
		t.Fatal("expected full WRITE rejection on substantive file")
	}
}

