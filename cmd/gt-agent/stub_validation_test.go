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
	rig := "mockrig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	layout := filepath.Join(rigDir, "myapp", "frontend", "game")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"myapp/frontend/index.html":       `<html><body>Hello</body></html>`,
		"myapp/frontend/game/main.js":     "def start():\n    pass\n",
		"myapp/frontend/game/renderer.js": "def render():\n    pass\n",
		"myapp/backend/main.py":           "def hello():\n    return 'Hello'\n",
		"myapp/backend/requirements.txt":  "flask\n",
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
		LayoutRoot:        "myapp",
		BeadTitleContains: "Implement myapp/",
		RequiredFiles: []string{
			"myapp/backend/main.py",
			"myapp/backend/requirements.txt",
			"myapp/frontend/index.html",
			"myapp/frontend/game/main.js",
			"myapp/frontend/game/renderer.js",
		},
		QAVerifyCommand: "cd myapp/backend && python3 -m pytest -q",
		TestRunner:      "pytest",
	}
	v = orchestrator.ClampProfileValidation(v)
	err := validateQAArtifacts(dir, rig, "all_passed", false, true, true, false, v)
	if err == nil {
		t.Fatal("expected QA to reject stub files")
	}
	if !strings.Contains(err.Error(), "stub") {
		t.Fatalf("unexpected error: %v", err)
	}
}
