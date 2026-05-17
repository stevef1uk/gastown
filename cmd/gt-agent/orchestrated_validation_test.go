package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestValidateQAArtifacts_customValidation(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "mockrig", "mayor", "rig")
	backend := filepath.Join(rigDir, "backend")
	if err := os.MkdirAll(backend, 0755); err != nil {
		t.Fatal(err)
	}
	v := orchestrator.WorkflowValidation{
		BeadTitleContains: "Implement api/",
		UnittestModule:    "backend.test_api",
		RequiredFiles:     []string{"backend/api.py"},
		MinPlanBytes:        200,
		MinArchitectureBytes: 200,
	}.WithDefaults()
	if err := os.WriteFile(filepath.Join(backend, "api.py"), []byte("ok\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// all_passed with no unittest run should fail
	if err := validateQAArtifacts(dir, "mockrig", "all_passed", false, true, false, v); err == nil {
		t.Fatal("expected unittest required")
	}
}

func TestIsUnittestCommand_customModule(t *testing.T) {
	if !isUnittestCommand("python3 -m unittest backend.test_api -v", "backend.test_api") {
		t.Fatal("expected match")
	}
	if isUnittestCommand("python3 -m unittest backend.test_fizzbuzz", "backend.test_api") {
		t.Fatal("expected no match")
	}
}
