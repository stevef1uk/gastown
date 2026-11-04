package orchestrator

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type samePhaseRecoveryFixture struct {
	town, rig, rigDir, source, runner, test, output string
	v                                               WorkflowValidation
}

func samePhaseRecoveryTestFixture(t *testing.T, language string) samePhaseRecoveryFixture {
	t.Helper()
	var f samePhaseRecoveryFixture
	if language == "go" {
		f.town, f.rig, f.v = compileRewindFixture(t)
		f.source, f.runner = "demo/internal/service/service.go", "demo/cmd/server/main.go"
		f.test = "demo/internal/service/service_test.go"
		f.rigDir = filepath.Join(f.town, f.rig, "mayor", "rig")
		main := filepath.Join(f.rigDir, f.runner)
		data, err := os.ReadFile(main)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(main, []byte(strings.ReplaceAll(string(data), "MissingHandler", "Handler")), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(f.rigDir, f.test), []byte("package service\n\nimport \"testing\"\nfunc TestHandler(t *testing.T) {\n var response MissingType\n _ = response\n}\n"), 0644); err != nil {
			t.Fatal(err)
		}
		build := exec.Command("go", "build", "-mod=readonly", "-o", os.DevNull, "./...")
		build.Dir = filepath.Join(f.rigDir, "demo")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("production must build despite the stale test: %v\n%s", err, out)
		}
		verify := exec.Command("go", "test", "-mod=readonly", "./...")
		verify.Dir = build.Dir
		out, err := verify.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "undefined: MissingType") {
			t.Fatalf("fixture must reproduce a test-only build failure: err=%v\n%s", err, out)
		}
		f.output = string(out)
	} else {
		f.town, f.rig, f.v = pythonRewindFixture(t)
		f.source, f.runner = "demo/service/api.py", "demo/runner.py"
		f.test = "demo/tests/test_api.py"
		f.rigDir = filepath.Join(f.town, f.rig, "mayor", "rig")
		if err := os.WriteFile(filepath.Join(f.rigDir, f.runner), []byte("from service.api import Handler\nprint(Handler({}).history())\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(f.rigDir, f.test), []byte("from service.api import MissingType\n"), 0644); err != nil {
			t.Fatal(err)
		}
		f.v.DeliveryPhases[0].RequiredFiles = append(f.v.DeliveryPhases[0].RequiredFiles, f.runner)
		f.output = pythonRewindOutput(t, f.rigDir, "import runpy; runpy.run_path('tests/test_api.py')")
	}
	f.v.ActivePhaseIDField = "service"
	f.v.RewoundFromPhaseIDField = "browser"
	f.v = f.v.ForActivePhase()
	return f
}

func TestSamePhaseTestFailureSelectsOwningCompanion(t *testing.T) {
	for _, language := range []string{"go", "python"} {
		t.Run(language, func(t *testing.T) {
			f := samePhaseRecoveryTestFixture(t, language)
			paths := samePhaseTestFailurePaths(f.rigDir, f.v, f.output)
			if !slices.Equal(paths, []string{f.test}) || SourcePathForOwnedTest(f.test, f.v) != f.source {
				t.Fatalf("test failure was not mapped to its source owner: paths=%v owner=%q", paths, SourcePathForOwnedTest(f.test, f.v))
			}
			if language == "python" {
				if err := os.WriteFile(filepath.Join(f.rigDir, f.test), []byte("from service.api import Handler\nmissing: MissingType\n"), 0644); err != nil {
					t.Fatal(err)
				}
				output := pythonRewindOutput(t, f.rigDir, "import runpy; runpy.run_path('tests/test_api.py')")
				if paths := samePhaseTestFailurePaths(f.rigDir, f.v, output); !slices.Equal(paths, []string{f.test}) {
					t.Fatalf("unlisted Python test's own NameError was not recognized: %v", paths)
				}
			}
			for _, output := range []string{"all tests passed", "tests might reference MissingType", "ImportError: No module named 'external_framework'"} {
				if paths := samePhaseTestFailurePaths(f.rigDir, f.v, output); len(paths) != 0 {
					t.Fatalf("feedback without a first-party test failure triggered repair: %v", paths)
				}
			}
			f.v.DeliveryPhases[1].RequiredFiles = append(f.v.DeliveryPhases[1].RequiredFiles, f.test)
			if paths := samePhaseTestFailurePaths(f.rigDir, f.v, f.output); len(paths) != 0 {
				t.Fatalf("future-phase test was claimed by current-phase source: %v", paths)
			}
		})
	}
}

func TestImplementationFailureRepairsSamePhaseGoAndPythonTests(t *testing.T) {
	for _, language := range []string{"go", "python"} {
		t.Run(language, func(t *testing.T) {
			f := samePhaseRecoveryTestFixture(t, language)
			profileDir := filepath.Join(f.rigDir, ".gastown")
			if err := os.MkdirAll(profileDir, 0755); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(map[string]any{"version": 1, "validation": f.v})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(profileDir, "workflow-profile.json"), data, 0644); err != nil {
				t.Fatal(err)
			}
			status := map[string]string{"mock-owner": "closed", "mock-runner": "in_progress"}
			beads := []PlanBead{{ID: "mock-owner", Title: "Implement " + f.source + " per architecture"}, {ID: "mock-runner", Title: "Implement " + f.runner + " per architecture"}}
			setListImplementBeadsByStatusHook(t, f.town, f.rig, func(_, _ string, _ WorkflowValidation, want string) ([]PlanBead, error) {
				var result []PlanBead
				for _, bead := range beads {
					if status[bead.ID] == want {
						result = append(result, bead)
					}
				}
				return result, nil
			})
			prev := bdUpdateImplementBeadStatusHook
			t.Cleanup(func() { bdUpdateImplementBeadStatusHook = prev })
			bdUpdateImplementBeadStatusHook = func(_, _, id, state string) error { status[id] = state; return nil }
			m := NewManager(f.town)
			tpl := &WorkflowTemplate{ID: "test-repair", InitialState: "implementation", States: map[string]State{
				"implementation": {Role: "polecat", Transitions: map[string]Transition{"failure": {To: "planning"}}},
				"planning":       {Role: "planner"},
			}}
			m.LoadTemplate(tpl)
			m.instances["wf-test"] = &WorkflowInstance{ID: "wf-test", TemplateID: tpl.ID, CurrentState: "implementation", Status: "running", Variables: map[string]string{"rig": f.rig}}
			next, err := m.CompleteTask("wf-test", "failure", f.rig+"/polecat", "Entrypoint is green but another package's tests do not compile", f.output, nil)
			if err != nil || next != "implementation" {
				t.Fatalf("test repair bounced to Planner: next=%q err=%v", next, err)
			}
			if status["mock-owner"] != "in_progress" || status["mock-runner"] != "open" {
				t.Fatalf("repair owner was not selected ahead of the blocked entrypoint: %v", status)
			}
			profile, _, err := LoadRigWorkflowProfileFile(f.town, f.rig)
			if err != nil || profile.ActivePhaseID() != "service" || profile.RewoundFromPhaseIDField != "browser" {
				t.Fatalf("same-phase repair disturbed delivery progress: active=%q resume=%q err=%v", profile.ActivePhaseID(), profile.RewoundFromPhaseIDField, err)
			}
			pending := m.instances["wf-test"].PendingRework
			if pending == nil || !strings.Contains(pending.Feedback, "MissingType") || !strings.Contains(pending.Feedback, f.test) {
				t.Fatalf("same-state test repair lost failure evidence: %+v", pending)
			}
			head, err := NextOpenImplementBead(f.town, f.rig, profile.ForActivePhase())
			if err != nil || head == nil || head.ID != "mock-owner" {
				t.Fatalf("queue skipped the test-repair owner: bead=%+v err=%v", head, err)
			}
			if err := ValidateImplementWritePath(f.town, f.rig, "mock-owner", f.test, profile, false, f.output, nil); err != nil {
				t.Fatalf("owning bead cannot amend its failing test companion: %v", err)
			}
			if err := ValidateImplementReadPath(f.town, f.rig, "mock-owner", f.test, profile, f.output); err != nil {
				t.Fatalf("owning bead cannot inspect its failing test companion: %v", err)
			}
			if err := ValidateImplementWritePath(f.town, f.rig, "mock-owner", "demo/unrelated/other_test.py", profile, false, f.output, nil); err == nil {
				t.Fatal("repair must not grant unrelated test writes")
			}
			if language == "python" {
				verify := PythonImplementationVerifyCommandForBead(profile.ForActivePhase(), f.rigDir, f.source)
				if !strings.Contains(verify, f.test) || strings.Contains(verify, "compileall") {
					t.Fatalf("source verification ignored its existing failing test: %s", verify)
				}
			}
		})
	}
}

func TestOwnedTestCompanionRequiresUniqueCurrentPhaseSource(t *testing.T) {
	v := WorkflowValidation{LayoutRoot: "demo", RequiredFiles: []string{"demo/a/api.py", "demo/b/api.py"}}
	if owner := SourcePathForOwnedTest("demo/tests/test_api.py", v); owner != "" {
		t.Fatalf("ambiguous shared test name must not select a source: %s", owner)
	}
	if owner := SourcePathForOwnedTest("demo/a/api_test.py", v); owner != "demo/a/api.py" {
		t.Fatalf("co-located suffix-style Python test was not recognized: %s", owner)
	}
	if owner := SourcePathForOwnedTest("demo/unrelated/test_api.py", v); owner != "" {
		t.Fatalf("basename alone must not establish ownership: %s", owner)
	}
}
