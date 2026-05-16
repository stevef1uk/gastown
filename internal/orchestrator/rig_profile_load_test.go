package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRigWorkflowProfileFile_missing(t *testing.T) {
	dir := t.TempDir()
	v, ok, err := LoadRigWorkflowProfileFile(dir, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected no profile")
	}
	if v.BeadTitleContains != "" {
		t.Fatalf("got %+v", v)
	}
}

func TestLoadRigWorkflowProfileFile_readsValidation(t *testing.T) {
	dir := t.TempDir()
	profDir := filepath.Join(dir, "myrig", "mayor", "rig", rigProfileDir)
	if err := os.MkdirAll(profDir, 0755); err != nil {
		t.Fatal(err)
	}
	env := rigProfileEnvelope{
		Version:    1,
		Source:     "llm",
		Validation: WorkflowValidation{BeadTitleContains: "Implement api/", QAVerifyCommand: "pytest -q"},
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(profDir, rigProfileFile)
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	v, ok, err := LoadRigWorkflowProfileFile(dir, "myrig")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected profile")
	}
	if v.BeadTitleContains != "Implement api/" || v.QAVerifyCommand != "pytest -q" {
		t.Fatalf("got %+v", v)
	}
}
