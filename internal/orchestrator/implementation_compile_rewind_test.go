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

func compileRewindFixture(t *testing.T) (string, string, WorkflowValidation) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("Go toolchain unavailable")
	}
	town, rig := t.TempDir(), "mockrig"
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	files := map[string]string{
		"demo/go.mod":                      "module demo\n\ngo 1.22\n",
		"demo/internal/service/service.go": "package service\n\nimport (\"fmt\"; \"net/http\")\n\nfunc Handler(w http.ResponseWriter, r *http.Request) {\n w.Header().Set(\"Content-Type\", \"text/plain\")\n fmt.Fprintf(w, \"%s %s\", r.Method, r.URL.Path)\n}\n" + strings.Repeat("// The handler responds using the incoming request.\n", 12),
		"demo/cmd/server/main.go":          "package main\n\nimport (\"net/http\"; \"demo/internal/service\")\n\nfunc main() {\n mux := http.NewServeMux()\n mux.HandleFunc(\"/\", service.MissingHandler)\n if err := http.ListenAndServe(\":0\", mux); err != nil {\n panic(err)\n }\n}\n" + strings.Repeat("// The entrypoint wires the service into the HTTP server.\n", 12),
		"demo/tests/e2e.spec.js":           "test('loads the application', async () => {});\n",
	}
	for path, content := range files {
		abs := filepath.Join(rigDir, path)
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	v := WorkflowValidation{
		LayoutRoot: "demo", TestRunner: "go", BeadTitleContains: "Implement ",
		MinImplementationFileBytes: 1, MinSubstantiveLines: 1,
		ActivePhaseIDField: "browser", CompletedPhaseIDsField: []string{"service"},
		DeliveryPhases: []DeliveryPhase{
			{ID: "service", RequiredFiles: []string{"demo/internal/service/service.go", "demo/cmd/server/main.go"}, QAVerifyCommand: "cd demo && go test ./..."},
			{ID: "browser", RequiredFiles: []string{"demo/tests/e2e.spec.js"}, QAVerifyCommand: "cd demo && npx playwright test", DependsOn: []string{"service"}},
		},
	}
	return town, rig, v
}

func TestCompletedPhaseGoCompileFailure(t *testing.T) {
	town, rig, v := compileRewindFixture(t)
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	idx, paths, output := completedPhaseGoCompileFailure(rigDir, v.ForActivePhase())
	if idx != 0 || !slices.Equal(paths, []string{"demo/cmd/server/main.go"}) || !strings.Contains(output, "undefined: service.MissingHandler") {
		t.Fatalf("earlier phase compile failure: idx=%d paths=%v output=%s", idx, paths, output)
	}
	v.DeliveryPhases[0].RequiredFiles = []string{"demo/internal/service/service.go"}
	v.DeliveryPhases[1].RequiredFiles = append(v.DeliveryPhases[1].RequiredFiles, "demo/cmd/server/main.go")
	if idx, _, _ := completedPhaseGoCompileFailure(rigDir, v); idx != -1 {
		t.Fatal("active phase's own compiler error must not rewind an earlier phase")
	}
	v.DeliveryPhases[1].RequiredFiles = []string{"demo/tests/e2e.spec.js"}
	if idx, _, _ := completedPhaseGoCompileFailure(rigDir, v); idx != -1 {
		t.Fatal("unowned compiler paths must not reopen unrelated phases")
	}
	v.DeliveryPhases = append(v.DeliveryPhases, DeliveryPhase{ID: "future", RequiredFiles: []string{"demo/cmd/server/main.go"}})
	if idx, _, _ := completedPhaseGoCompileFailure(rigDir, v); idx != -1 {
		t.Fatal("future-phase compiler paths must not rewind completed work")
	}
	v.DeliveryPhases = v.DeliveryPhases[:2]
	v.DeliveryPhases[0].RequiredFiles = append(v.DeliveryPhases[0].RequiredFiles, "demo/cmd/server/main.go")
	mainPath := filepath.Join(rigDir, "demo/cmd/server/main.go")
	source, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainPath, []byte(strings.ReplaceAll(string(source), "MissingHandler", "Handler")), 0644); err != nil {
		t.Fatal(err)
	}
	if idx, _, _ := completedPhaseGoCompileFailure(rigDir, v); idx != -1 {
		t.Fatal("healthy earlier phases must not be reopened for a browser failure")
	}
	mod, err := os.ReadFile(filepath.Join(rigDir, "demo/go.mod"))
	if err != nil || string(mod) != "module demo\n\ngo 1.22\n" {
		t.Fatalf("compile probe changed the module manifest: %q err=%v", mod, err)
	}
}

func TestCompletedPhaseCompileRewindIgnoresInfrastructureFailures(t *testing.T) {
	for _, message := range []string{
		"cmd/server/main.go:7:14: missing go.sum entry for module providing package external/service",
		"cmd/server/main.go:7:14: compile: version does not match go tool version",
	} {
		t.Run(message, func(t *testing.T) {
			town, rig, v := compileRewindFixture(t)
			bin := t.TempDir()
			invocation := filepath.Join(bin, "invocation")
			script := "#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$GT_COMPILE_REWIND_CALL\"\nprintf '%s\\n' '" + message + "' >&2\nexit 1\n"
			if err := os.WriteFile(filepath.Join(bin, "go"), []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GT_COMPILE_REWIND_CALL", invocation)
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			if idx, paths, _ := completedPhaseGoCompileFailure(filepath.Join(town, rig, "mayor", "rig"), v); idx != -1 || len(paths) != 0 {
				t.Fatalf("infrastructure failure incorrectly reopened source: idx=%d paths=%v", idx, paths)
			}
			args, err := os.ReadFile(invocation)
			if err != nil || strings.TrimSpace(string(args)) != "build -mod=readonly -o "+os.DevNull+" ./..." {
				t.Fatalf("compile probe must not change module metadata or emit binaries: args=%q err=%v", args, err)
			}
		})
	}
}

func TestImplementationFailureRewindsExistingBrokenDependency(t *testing.T) {
	town, rig, v := compileRewindFixture(t)
	rigDir := filepath.Join(town, rig, "mayor", "rig")
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
	status := map[string]string{"mock-service": "closed", "mock-main": "closed", "mock-browser": "open"}
	beads := []PlanBead{
		{ID: "mock-service", Title: "Implement demo/internal/service/service.go per architecture"},
		{ID: "mock-main", Title: "Implement demo/cmd/server/main.go per architecture"},
		{ID: "mock-browser", Title: "Implement demo/tests/e2e.spec.js per architecture"},
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
	tpl := &WorkflowTemplate{ID: "compile-recovery", InitialState: "implementation", States: map[string]State{
		"implementation": {Role: "polecat", Transitions: map[string]Transition{"failure": {To: "planning"}}},
		"planning":       {Role: "planner"},
	}}
	m.LoadTemplate(tpl)
	m.instances["wf-repair"] = &WorkflowInstance{ID: "wf-repair", TemplateID: tpl.ID, CurrentState: "implementation", Status: "running", Variables: map[string]string{"rig": rig}}
	next, err := m.CompleteTask("wf-repair", "failure", rig+"/polecat", "Browser verification cannot start; backend is outside this bead's scope", "", nil)
	if err != nil || next != "implementation" {
		t.Fatalf("must route to dependency repair rather than browser planning: next=%q err=%v", next, err)
	}
	profile, ok, err := LoadRigWorkflowProfileFile(town, rig)
	if err != nil || !ok || profile.ActivePhaseID() != "service" || profile.RewoundFromPhaseIDField != "browser" {
		t.Fatalf("owning phase rewind: active=%q resume=%q err=%v", profile.ActivePhaseID(), profile.RewoundFromPhaseIDField, err)
	}
	if status["mock-service"] != "open" || status["mock-main"] != "open" || status["mock-browser"] != "open" {
		t.Fatalf("owning phase must be reopened without discarding browser work: %v", status)
	}
	rework := m.instances["wf-repair"].PendingRework
	if rework == nil || !strings.Contains(rework.Feedback, "undefined: service.MissingHandler") {
		t.Fatalf("same-state dependency repair lost its compiler feedback: %+v", rework)
	}
}
