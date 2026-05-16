package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestValidateQAArtifacts_rejectsStubs(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "testgt1", "mayor", "rig")
	layout := filepath.Join(rigDir, "defender", "frontend", "game")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"defender/frontend/index.html":     `<html><body>Hello</body></html>`,
		"defender/frontend/game/main.js":   "def start():\n    pass\n",
		"defender/frontend/game/renderer.js": "def render():\n    pass\n",
		"defender/backend/main.py":         "def hello():\n    return 'Hello'\n",
		"defender/backend/requirements.txt": "flask\n",
	}
	for rel, body := range files {
		path := filepath.Join(rigDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	v := orchestrator.WorkflowValidation{
		LayoutRoot: "defender",
		BeadTitleContains: "Implementation defender/",
		RequiredFiles: []string{
			"defender/backend/main.py",
			"defender/backend/requirements.txt",
			"defender/frontend/index.html",
			"defender/frontend/game/main.js",
			"defender/frontend/game/renderer.js",
		},
		QAVerifyCommand: "cd defender/backend && pytest -q",
		TestRunner:      "pytest",
	}
	v = orchestrator.ClampProfileValidation(v)
	err := validateQAArtifacts(dir, "testgt1", "all_passed", false, true, true, v)
	if err == nil {
		t.Fatal("expected QA to reject stub files")
	}
	if !strings.Contains(err.Error(), "stub") {
		t.Fatalf("unexpected error: %v", err)
	}
}
