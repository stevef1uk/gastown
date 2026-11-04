package orchestrator

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

func TestImplementationPreRunRepairsGeneratedProjectIgnores(t *testing.T) {
	for _, load := range []func() (*WorkflowTemplate, error){LoadRigFlowTemplate, LoadReqFlowTemplate} {
		tpl, err := load()
		if err != nil {
			t.Fatal(err)
		}
		t.Run(tpl.ID, func(t *testing.T) {
			if !slices.Contains(tpl.States["implementation"].Hooks.PreRun, "repair_project_git_ignores") {
				t.Fatal("workflow omits existing-rig ignore repair")
			}
			town, rig := t.TempDir(), "sample"
			dir := filepath.Join(town, rig, "mayor", "rig")
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
				t.Fatalf("git init: %v: %s", err, out)
			}
			if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("# Gas Town mayor/rig (rig-flow checkpoints)\ntests/\npackage.json\npackage-lock.json\nnode_modules/\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := RunPreRunHook("repair_project_git_ignores", town, rig, WorkflowValidation{}); err != nil {
				t.Fatal(err)
			}
			for _, file := range []string{"app/tests/e2e.spec.js", "backend/tests/test_service.py", "package.json", "app/package-lock.json"} {
				cmd := exec.Command("git", "-C", dir, "check-ignore", "--", file)
				if out, err := cmd.CombinedOutput(); err == nil {
					t.Fatalf("pre-run still ignores project source %s: %s", file, out)
				}
			}
		})
	}
}
