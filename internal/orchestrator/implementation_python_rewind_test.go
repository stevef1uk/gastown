package orchestrator

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const pythonRewindService = `class Handler:
    def __init__(self, settings):
        self.settings = dict(settings)
        self.requests = []

    def dispatch(self, request):
        normalized = {}
        for name, value in request.items():
            normalized[name.lower()] = str(value).strip()
        self.requests.append(normalized)
        return normalized

    def history(self):
        return [dict(request) for request in self.requests]
`

func pythonRewindFixture(t *testing.T) (string, string, WorkflowValidation) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("Python interpreter unavailable")
	}
	town, rig := t.TempDir(), "mockrig"
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	for file, content := range map[string]string{
		"demo/service/api.py":        pythonRewindService,
		"demo/tests/test_browser.py": "import unittest\nfrom service.api import create_handler\n",
	} {
		path := filepath.Join(rigDir, file)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	v := WorkflowValidation{
		LayoutRoot: "demo", TestRunner: "pytest", PythonVenvDir: ".venv", BeadTitleContains: "Implement ",
		MinImplementationFileBytes: 1, MinSubstantiveLines: 1,
		ActivePhaseIDField: "browser", CompletedPhaseIDsField: []string{"service"},
		DeliveryPhases: []DeliveryPhase{
			{ID: "service", RequiredFiles: []string{"demo/service/api.py"}, QAVerifyCommand: "cd demo && python3 -m unittest discover -s tests"},
			{ID: "browser", RequiredFiles: []string{"demo/tests/test_browser.py"}, QAVerifyCommand: "cd demo && python3 -m unittest discover -s tests", DependsOn: []string{"service"}},
		},
	}
	return town, rig, v
}

func pythonRewindOutput(t *testing.T, rigDir, code string) string {
	t.Helper()
	cmd := exec.Command("python3", "-B", "-c", code)
	cmd.Dir = filepath.Join(rigDir, "demo")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("fixture must fail Python verification: %s", output)
	}
	return string(output)
}

func TestCompletedPhasePythonFailure(t *testing.T) {
	for _, tc := range []struct{ name, source, code string }{
		{"missing-imported-symbol", pythonRewindService, "from service.api import create_handler"},
		{"missing-module-attribute", pythonRewindService, "import service.api; service.api.create_handler()"},
		{"syntax-error", "def broken(:\n", "import service.api"},
		{"name-error", pythonRewindService + "\nmissing_dependency()\n", "import service.api"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			town, rig, v := pythonRewindFixture(t)
			rigDir := filepath.Join(town, rig, "mayor", "rig")
			path := filepath.Join(rigDir, "demo/service/api.py")
			if err := os.WriteFile(path, []byte(tc.source), 0644); err != nil {
				t.Fatal(err)
			}
			output := pythonRewindOutput(t, rigDir, tc.code)
			idx, paths, evidence := completedPhasePythonFailure(rigDir, v.ForActivePhase(), output)
			if idx != 0 || !slices.Equal(paths, []string{"demo/service/api.py"}) || evidence != output {
				t.Fatalf("earlier Python dependency not identified: idx=%d paths=%v output=%s", idx, paths, output)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != tc.source {
				t.Fatalf("failure detection changed source: %q err=%v", data, err)
			}
			v.DeliveryPhases[0].RequiredFiles = []string{"demo/other.py"}
			v.DeliveryPhases[1].RequiredFiles = append(v.DeliveryPhases[1].RequiredFiles, "demo/service/api.py")
			if idx, _, _ := completedPhasePythonFailure(rigDir, v, output); idx != -1 {
				t.Fatal("active-phase Python failures must not rewind earlier work")
			}
			v.DeliveryPhases[1].RequiredFiles = []string{"demo/tests/test_browser.py"}
			if idx, _, _ := completedPhasePythonFailure(rigDir, v, output); idx != -1 {
				t.Fatal("unowned Python failures must not reopen unrelated phases")
			}
			v.DeliveryPhases = append(v.DeliveryPhases, DeliveryPhase{ID: "future", RequiredFiles: []string{"demo/service/api.py"}})
			if idx, _, _ := completedPhasePythonFailure(rigDir, v, output); idx != -1 {
				t.Fatal("future-phase Python failures must not rewind completed work")
			}
		})
	}
}

func TestPythonDependencyRewindExcludesEnvironmentFailures(t *testing.T) {
	town, rig, v := pythonRewindFixture(t)
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	for _, module := range []string{"undeclared_external_dependency_for_test", "declared_external_dependency_for_test"} {
		if err := os.WriteFile(filepath.Join(rigDir, "demo/requirements.txt"), []byte("declared_external_dependency_for_test>=1\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(rigDir, "demo/service/api.py"), []byte("import "+module+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
		output := pythonRewindOutput(t, rigDir, "import service.api")
		if idx, _, _ := completedPhasePythonFailure(rigDir, v, output); idx != -1 {
			t.Fatalf("missing third-party module %s reopened source", module)
		}
	}
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(external, "external_library.py"), []byte("def fail():\n    return missing_external_symbol\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "demo/service/api.py"), []byte("import external_library\nexternal_library.fail()\n"), 0644); err != nil {
		t.Fatal(err)
	}
	code := "import sys; sys.path.insert(0, " + strconv.Quote(external) + "); import service.api"
	output := pythonRewindOutput(t, rigDir, code)
	if idx, _, _ := completedPhasePythonFailure(rigDir, v, output); idx != -1 {
		t.Fatal("external terminal traceback frame must not blame an earlier project frame")
	}
	output = pythonRewindOutput(t, rigDir, "import sys; sys.path.insert(0, "+strconv.Quote(external)+"); from external_library import missing_factory")
	if idx, _, _ := completedPhasePythonFailure(rigDir, v, output); idx != -1 {
		t.Fatal("third-party missing symbols must not reopen first-party source")
	}
	for _, output := range []string{"/env/bin/python: No such file or directory", "1 passed", "Browser failed; service.api might be broken"} {
		if idx, _, _ := completedPhasePythonFailure(rigDir, v, output); idx != -1 {
			t.Fatalf("non-traceback feedback reopened source: %s", output)
		}
	}
}

func TestPythonDependencyRewindWithoutPythonActivePhaseOrVenv(t *testing.T) {
	town, rig, v := pythonRewindFixture(t)
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	v.TestRunner = "custom"
	v.PythonVenvDir = "off"
	v.DeliveryPhases[1].RequiredFiles = []string{"demo/tests/e2e.spec.js"}
	v.DeliveryPhases[1].QAVerifyCommand = "cd demo && npx playwright test"
	output := pythonRewindOutput(t, rigDir, "from service.api import create_handler")
	idx, paths, _ := completedPhasePythonFailure(rigDir, v.ForActivePhase(), output)
	if idx != 0 || !slices.Equal(paths, []string{"demo/service/api.py"}) {
		t.Fatalf("browser-phase scope hid its first-party Python dependency: idx=%d paths=%v", idx, paths)
	}
}

func TestPythonDependencyRewindResolvesProvidersConservatively(t *testing.T) {
	town, rig, v := pythonRewindFixture(t)
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	v.DeliveryPhases[0].RequiredFiles = []string{"demo/src/service/api.py"}
	output := "AttributeError: module 'service.api' has no attribute 'create_handler'\n"
	idx, paths, _ := completedPhasePythonFailure(rigDir, v, output)
	if idx != 0 || !slices.Equal(paths, []string{"demo/src/service/api.py"}) {
		t.Fatalf("src-layout provider not mapped to its owning phase: idx=%d paths=%v", idx, paths)
	}
	v.DeliveryPhases[0].RequiredFiles = append(v.DeliveryPhases[0].RequiredFiles, "demo/alternate/service/api.py")
	if idx, _, _ := completedPhasePythonFailure(rigDir, v, output); idx != -1 {
		t.Fatal("ambiguous module providers must not be guessed")
	}
	v.PythonVenvDir = "environments/python"
	v.DeliveryPhases[0].RequiredFiles = []string{"environments/python/internal.py", "demo/.venv/lib/site-packages/service/api.py"}
	for _, output := range []string{
		"File \"environments/python/internal.py\", line 12, in activate\nNameError: broken_virtualenv\n",
		"ImportError: cannot import name 'create_handler' from 'service.api' (" + filepath.Join(rigDir, "demo/.venv/lib/site-packages/service/api.py") + ")\n",
	} {
		if idx, _, _ := completedPhasePythonFailure(rigDir, v, output); idx != -1 {
			t.Fatal("virtualenv files are not first-party source even if listed in a profile")
		}
	}
}

func TestPythonDependencyRewindHandlesPytestAndServerOutput(t *testing.T) {
	town, rig, v := pythonRewindFixture(t)
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	for _, output := range []string{
		"service/api.py:18: in dispatch\nE   NameError: missing_callback is not defined\n",
		"[WebServer] Traceback (most recent call last):\n[WebServer]   File \"service/api.py\", line 18, in dispatch\n[WebServer] NameError: missing_callback is not defined\n",
	} {
		idx, paths, _ := completedPhasePythonFailure(rigDir, v, output)
		if idx != 0 || !slices.Equal(paths, []string{"demo/service/api.py"}) {
			t.Fatalf("verification traceback not recognized: idx=%d paths=%v output=%s", idx, paths, output)
		}
		if idx, _, _ := completedPhasePythonFailure(rigDir, v, output+"RuntimeError: unrelated later failure\n"); idx != -1 {
			t.Fatal("stale recognized exception must not override the final exception")
		}
	}
}

func TestImplementationFailureRewindsExistingPythonDependency(t *testing.T) {
	town, rig, v := pythonRewindFixture(t)
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	output := pythonRewindOutput(t, rigDir, "from service.api import create_handler")
	profileDir := filepath.Join(rigDir, ".gastown")
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"version": 1, "validation": v})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, "workflow-profile.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	status := map[string]string{"mock-service": "closed", "mock-browser": "open"}
	beads := []PlanBead{
		{ID: "mock-service", Title: "Implement demo/service/api.py per architecture"},
		{ID: "mock-browser", Title: "Implement demo/tests/test_browser.py per architecture"},
	}
	setListImplementBeadsByStatusHook(t, town, rig, func(_, _ string, _ WorkflowValidation, want string) ([]PlanBead, error) {
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
	m := NewManager(town)
	tpl := &WorkflowTemplate{ID: "python-recovery", InitialState: "implementation", States: map[string]State{
		"implementation": {Role: "polecat", Transitions: map[string]Transition{"failure": {To: "planning"}}},
		"planning":       {Role: "planner"},
	}}
	m.LoadTemplate(tpl)
	m.instances["wf-python"] = &WorkflowInstance{ID: "wf-python", TemplateID: tpl.ID, CurrentState: "implementation", Status: "running", Variables: map[string]string{"rig": rig}}
	next, err := m.CompleteTask("wf-python", "failure", rig+"/polecat", "Verification cannot import the completed service", "Command: python3 -m unittest discover -s tests\nError: exit status 1\nOutput:\n"+output, nil)
	if err != nil || next != "implementation" {
		t.Fatalf("Python dependency failure returned to planning: next=%q err=%v", next, err)
	}
	profile, ok, err := LoadRigWorkflowProfileFile(town, rig)
	if err != nil || !ok || profile.ActivePhaseID() != "service" || profile.RewoundFromPhaseIDField != "browser" {
		t.Fatalf("Python phase repair lost resume state: active=%q resume=%q err=%v", profile.ActivePhaseID(), profile.RewoundFromPhaseIDField, err)
	}
	if status["mock-service"] != "open" || status["mock-browser"] != "open" {
		t.Fatalf("Python dependency beads not reopened correctly: %v", status)
	}
	rework := m.instances["wf-python"].PendingRework
	if rework == nil || !strings.Contains(rework.Feedback, "ImportError: cannot import name 'create_handler'") {
		t.Fatalf("Python compiler feedback was lost on same-state repair: %+v", rework)
	}
}
