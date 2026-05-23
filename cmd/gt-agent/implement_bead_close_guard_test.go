package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestValidateImplementationBeadClose_rejectsMissingArtifact(t *testing.T) {
	dir := t.TempDir()
	town := filepath.Join(dir, "gt")
	rig := "mockrig"
	mayor := filepath.Join(town, rig, "mayor", "rig")
	layout := filepath.Join(mayor, "linkshelf", "internal", "api")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	v := orchestrator.WorkflowValidation{
		LayoutRoot:                 "linkshelf",
		BeadTitleContains:          "Implement linkshelf/",
		MinImplementationFileBytes: 10,
		MinSubstantiveLines:        2,
	}
	// No bd in unit test — path comes from title when ID is unknown to bd.
	beadPath := orchestrator.NormalizeBeadPathForLayout(
		"linkshelf/internal/api/handlers_test.go", v.LayoutRoot)
	if err := orchestrator.ValidateBeadArtifactOnDisk(mayor, beadPath, v); err == nil {
		t.Fatal("expected missing handlers_test.go")
	}
}

func TestValidateImplementationBeadClose_requiresCorrelatedTestFile(t *testing.T) {
	dir := t.TempDir()
	town := filepath.Join(dir, "gt")
	rig := "mockrig"
	mayor := filepath.Join(town, rig, "mayor", "rig")
	apiDir := filepath.Join(mayor, "linkshelf", "internal", "api")
	if err := os.MkdirAll(apiDir, 0755); err != nil {
		t.Fatal(err)
	}
	body := "package api\n\nimport \"net/http\"\n\nfunc F() http.Handler { return nil }\n"
	if err := os.WriteFile(filepath.Join(apiDir, "handlers.go"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	v := orchestrator.WorkflowValidation{
		LayoutRoot:                 "linkshelf",
		BeadTitleContains:          "Implement linkshelf/",
		MinImplementationFileBytes: 10,
		MinSubstantiveLines:        2,
	}
	beadPath := orchestrator.NormalizeBeadPathForLayout("linkshelf/internal/api/handlers.go", v.LayoutRoot)
	if err := orchestrator.ValidateBeadArtifactOnDisk(mayor, beadPath, v); err != nil {
		t.Fatalf("handlers.go should exist: %v", err)
	}
	testPath := orchestrator.CorrelatedTestPathForSource(beadPath, v.LayoutRoot)
	err := orchestrator.ValidateBeadArtifactOnDisk(mayor, testPath, v)
	if err == nil || !strings.Contains(err.Error(), "handlers_test.go") {
		t.Fatalf("want correlated test reject, got %v", err)
	}
}
