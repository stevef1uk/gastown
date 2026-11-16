package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestOwnedTestCloseGuard_CompileSuccessCannotReplaceApprovedTests(t *testing.T) {
	town, rig := t.TempDir(), "hybrid"
	root := rigMayorRigDir(town, rig)
	write := func(path, body string, mode os.FileMode) {
		t.Helper()
		abs := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(".gastown/workflow-profile.json", `{"version":1,"test_plan_reviewed":true,"test_plan_frozen":true}`, 0644)
	write("TEST_PLAN.md", "### foundation\nTest file: product/backend/test_main.py\nBead ID: b-main\n", 0644)
	write("product/backend/main.py", "def health():\n    return {'status': 'ok'}\n", 0644)
	v := orchestrator.WorkflowValidation{LayoutRoot: "product", BeadTitleContains: "Implement product/", TestRunner: "pytest", RequiredFiles: []string{"product/backend/main.py"}, MinImplementationFileBytes: 1, MinSubstantiveLines: 1}
	previous := orchestrator.ListImplementBeadsByStatusHook
	orchestrator.ListImplementBeadsByStatusHook = func(_, _ string, _ orchestrator.WorkflowValidation, status string) ([]orchestrator.PlanBead, error) {
		if status == "in_progress" {
			return []orchestrator.PlanBead{{ID: "b-main", Title: "Implement product/backend/main.py per architecture"}}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { orchestrator.ListImplementBeadsByStatusHook = previous })
	close := "bd close b-main"
	// This green flag could come from compileall or an unrelated phase command.
	if err := validateImplementationBeadClose(close, town, rig, v, true); err == nil || !strings.Contains(err.Error(), "test_main.py") {
		t.Fatalf("missing approved test passed close: %v", err)
	}
	write("product/backend/test_main.py", "from main import health\n\ndef test_health():\n    assert health()['status'] == 'ok'\n", 0644)
	// A controlled runner checks the exact target and lets the test exercise both
	// failed and successful pytest outcomes without depending on host packages.
	write(".venv/bin/python3", "#!/bin/sh\n[ \"$1\" = -m ] && [ \"$2\" = pytest ] && [ \"$4\" = product/backend/test_main.py ] || exit 99\nexit \"$OWNED_TEST_EXIT\"\n", 0755)
	t.Setenv("OWNED_TEST_EXIT", "1")
	if err := validateImplementationBeadClose(close, town, rig, v, true); err == nil || !strings.Contains(err.Error(), "required tests") {
		t.Fatalf("failed pytest passed close: %v", err)
	}
	t.Setenv("OWNED_TEST_EXIT", "0")
	if err := validateImplementationBeadClose(close, town, rig, v, true); err != nil {
		t.Fatalf("passing required tests rejected: %v", err)
	}
	if err := orchestrator.ValidateImplementWritePath(town, rig, "b-main", "product/backend/test_main.py", v, false, "", nil); err != nil {
		t.Fatalf("source bead cannot write its mandatory companion: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "TEST_PLAN.md")); err != nil {
		t.Fatal(err)
	}
	if err := validateImplementationBeadClose(close, town, rig, v, true); err == nil || !strings.Contains(err.Error(), "TEST_PLAN.md") {
		t.Fatalf("unreadable approved contract bypassed closure: %v", err)
	}
}
