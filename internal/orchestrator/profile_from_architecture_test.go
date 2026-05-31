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
