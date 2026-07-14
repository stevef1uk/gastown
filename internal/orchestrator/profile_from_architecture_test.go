package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnrichWorkflowValidationFromArchitecture_pythonPingapp(t *testing.T) {
	dir := t.TempDir()
	arch := `# Architecture
- ` + "`pingapp/requirements.txt`" + `
- ` + "`pingapp/main.py`" + `
- ` + "`pingapp/test_main.py`" + `
`
	if err := os.WriteFile(filepath.Join(dir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		QAVerifyCommand: "python3 -m pytest -v pingapp/test_main.py",
	}
	got := EnrichWorkflowValidationFromArchitecture(v, dir)
	if !WorkflowUsesPython(got) {
		t.Fatal("expected python workflow")
	}
	if got.LayoutRoot != "pingapp" {
		t.Fatalf("layout_root = %q, want pingapp", got.LayoutRoot)
	}
	if got.RequirementsFilePath() != "pingapp/requirements.txt" {
		t.Fatalf("requirements = %q", got.RequirementsFilePath())
	}
	if !strings.Contains(got.ProjectSetupVerifyHint(), "import pytest") {
		t.Fatalf("verify hint = %q", got.ProjectSetupVerifyHint())
	}
}

func TestEnrichWorkflowValidation_prefersSpecOverBadProfile(t *testing.T) {
	dir := t.TempDir()
	spec := `# PingApp

## Layout

` + "```" + `
pingapp/
├── requirements.txt
├── main.py
└── test_main.py
` + "```" + `

## HTTP API
| GET | /ping |
`
	arch := `# Architecture
- ./requirements.txt
- ./main.py
`
	if err := os.WriteFile(filepath.Join(dir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	// Bad spec-index profile (flat layout) — SPEC must win.
	v := WorkflowValidation{
		LayoutRoot:        ".",
		BeadTitleContains: "Implement ./",
		RequiredFiles:     []string{"requirements.txt", "main.py", "test_main.py"},
		QAVerifyCommand:   "cd . && pytest",
	}
	got := EnrichWorkflowValidationFromArchitecture(v, dir)
	if got.LayoutRoot != "pingapp" {
		t.Fatalf("layout_root = %q, want pingapp from SPEC", got.LayoutRoot)
	}
	if got.RequirementsFilePath() != "pingapp/requirements.txt" {
		t.Fatalf("requirements = %q", got.RequirementsFilePath())
	}
	if !strings.Contains(got.BeadTitleContains, "pingapp") {
		t.Fatalf("bead_title_contains = %q", got.BeadTitleContains)
	}
}

func TestParseSpecLayoutTree(t *testing.T) {
	spec := "## Layout\n\n```\npingapp/\n├── requirements.txt\n├── main.py\n└── test_main.py\n```\n"
	got := parseSpecLayoutTree(spec)
	if len(got) != 3 {
		t.Fatalf("paths = %v", got)
	}
	if got[0] != "pingapp/requirements.txt" {
		t.Fatalf("first = %q", got[0])
	}
}

func TestFormatProjectSetupStackBlock_pythonForbidsGo(t *testing.T) {
	v := WorkflowValidation{
		QAVerifyCommand: "python3 -m pytest -v pingapp/test_main.py",
		RequiredFiles:   []string{"pingapp/main.py", "pingapp/requirements.txt"},
		LayoutRoot:      "pingapp",
	}
	v = SanitizeRigFlowProfile(v)
	block := FormatProjectSetupStackBlock(v)
	if !strings.Contains(block, "Python") || !strings.Contains(block, "go mod") {
		t.Fatalf("expected python-only block with go forbidden: %s", block)
	}
}

func TestFormatProjectSetupStackBlock_nodejsPhase(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot:         ".",
		BeadTitleContains:  "Implement finally/",
		QAVerifyCommand:    "cd backend && pytest && cd ../frontend && npm test",
		TestRunner:         "pytest",
		ActivePhaseIDField: "frontend-ui",
		DeliveryPhases: []DeliveryPhase{
			{
				ID:              "frontend-ui",
				RequiredFiles:   []string{"frontend/components/Watchlist.tsx"},
				QAVerifyCommand: "cd frontend && npm test",
			},
		},
	}
	block := FormatProjectSetupStackBlock(v)
	if !strings.Contains(block, "Node.js") {
		t.Fatalf("expected Node.js stack block, got: %s", block)
	}
	if !strings.Contains(block, "cd frontend && npm install") {
		t.Fatalf("expected Node verify command in block, got: %s", block)
	}
	if strings.Contains(block, "Python") {
		t.Fatalf("Node.js block should not mention Python, got: %s", block)
	}
}

func TestProjectSetupFailureHint_nodejs(t *testing.T) {
	v := WorkflowValidation{
		QAVerifyCommand: "cd frontend && npm test",
		RequiredFiles:   []string{"frontend/app.tsx"},
	}
	hint := ProjectSetupFailureHint(v)
	if !strings.Contains(hint, "npm install") {
		t.Fatalf("expected npm install in failure hint, got: %s", hint)
	}
	if strings.Contains(hint, "Python") {
		t.Fatalf("Node.js failure hint should not mention Python, got: %s", hint)
	}
}
