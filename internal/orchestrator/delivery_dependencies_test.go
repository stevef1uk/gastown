package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func dependencyFixture() WorkflowValidation {
	return WorkflowValidation{
		LayoutRoot: "app", BeadTitleContains: "Implement ", ActivePhaseIDField: "foundation",
		DeliveryPhases: []DeliveryPhase{
			{ID: "foundation", RequiredFiles: []string{"app/frontend/page.tsx"}, QAVerifyCommand: "test -f app/frontend/page.tsx"},
			{ID: "client", RequiredFiles: []string{"app/frontend/package.json"}, DependsOn: []string{"foundation"}},
		},
	}
}

func TestDeliveryDependencyProblems(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*WorkflowValidation)
		want   string
	}{
		{"valid", func(v *WorkflowValidation) {
			v.DeliveryPhases[0].RequiredFiles = append(v.DeliveryPhases[0].RequiredFiles, "app/frontend/package.json")
		}, ""},
		{"implicit frontend prerequisite", func(v *WorkflowValidation) {}, "assigned to future phase"},
		{"relative verification input", func(v *WorkflowValidation) {
			v.DeliveryPhases[0].QAVerifyCommand = "cd app/frontend && test -f package.json"
		}, "verification requires"},
		{"implicit package build input", func(v *WorkflowValidation) { v.DeliveryPhases[0].QAVerifyCommand = "cd app/frontend && npm run build" }, "verification requires"},
		{"future provider", func(v *WorkflowValidation) {
			v.DeliveryPhases[0].FileDependencies = map[string][]string{"app/frontend/page.tsx": {"app/frontend/package.json"}}
		}, "assigned to future phase"},
		{"future verification input", func(v *WorkflowValidation) { v.DeliveryPhases[0].QAVerifyCommand = "test -f app/frontend/package.json" }, "verification requires"},
		{"unknown provider", func(v *WorkflowValidation) {
			v.DeliveryPhases[0].FileDependencies = map[string][]string{"app/frontend/page.tsx": {"app/missing.ts"}}
		}, "unowned project file"},
		{"nonowned consumer", func(v *WorkflowValidation) {
			v.DeliveryPhases[0].FileDependencies = map[string][]string{"app/unknown.ts": {"app/frontend/package.json"}}
		}, "non-owned file"},
		{"forward phase", func(v *WorkflowValidation) {
			v.DeliveryPhases[0].DependsOn = []string{"client"}
			v.DeliveryPhases[1].DependsOn = nil
		}, "runs before its prerequisite"},
		{"phase cycle", func(v *WorkflowValidation) { v.DeliveryPhases[0].DependsOn = []string{"client"} }, "dependency cycle"},
		{"file cycle", func(v *WorkflowValidation) {
			v.DeliveryPhases[0].RequiredFiles = append(v.DeliveryPhases[0].RequiredFiles, "app/frontend/config.ts")
			v.DeliveryPhases[0].FileDependencies = map[string][]string{"app/frontend/page.tsx": {"app/frontend/config.ts"}, "app/frontend/config.ts": {"app/frontend/page.tsx"}}
		}, "file dependency cycle"},
		{"repeated path uses earlier owner", func(v *WorkflowValidation) {
			v.DeliveryPhases[0].RequiredFiles = append(v.DeliveryPhases[0].RequiredFiles, "app/frontend/package.json")
			v.DeliveryPhases[0].FileDependencies = map[string][]string{"app/frontend/page.tsx": {"app/frontend/package.json"}}
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := dependencyFixture()
			tc.change(&v)
			got := strings.Join(DeliveryDependencyProblems(t.TempDir(), v), "\n")
			if tc.want == "" && got != "" || tc.want != "" && !strings.Contains(got, tc.want) {
				t.Fatalf("want %q, got %q", tc.want, got)
			}
		})
	}
}

func TestPlanDependenciesOrderProvidersBeforeConsumers(t *testing.T) {
	files := []string{"app/a.py", "app/b.py", "app/c.py"}
	doc := "### sm-a: app/a.py\n- Depends on: `sm-b`\n### sm-b: app/b.py\n- Depends on: `app/c.py`\n### sm-c: app/c.py\n- Depends on: <none>\n"
	got, err := dependencyBuildOrder(files, planFileDependencies(doc, files))
	if err != nil || strings.Join(got, ",") != "app/c.py,app/b.py,app/a.py" {
		t.Fatalf("order=%v err=%v", got, err)
	}
	stable, err := dependencyBuildOrder(files, nil)
	if err != nil || strings.Join(stable, ",") != strings.Join(files, ",") {
		t.Fatalf("unstable: %v %v", stable, err)
	}
}

func TestNextBeadHonorsDeclaredDependencyAndMissingPhaseManifest(t *testing.T) {
	town, rig := t.TempDir(), "sample"
	dir := filepath.Join(town, rig, "mayor", "rig")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	v := dependencyFixture()
	v.DeliveryPhases = v.DeliveryPhases[:1]
	v.DeliveryPhases[0].RequiredFiles = []string{"app/frontend/page.tsx", "app/frontend/package.json"}
	v.DeliveryPhases[0].FileDependencies = map[string][]string{"app/frontend/page.tsx": {"app/frontend/package.json"}}
	v = v.ForActivePhase()
	setListImplementBeadsByStatusHook(t, town, rig, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "open" {
			return []PlanBead{{ID: "sm-page", Title: "Implement app/frontend/page.tsx per architecture"}, {ID: "sm-package", Title: "Implement app/frontend/package.json per architecture"}}, nil
		}
		return nil, nil
	})
	head, err := NextOpenImplementBead(town, rig, v)
	if err != nil || head == nil || head.ID != "sm-package" {
		t.Fatalf("head=%v err=%v", head, err)
	}
}

func TestDependencyBlockRequiresFutureOwnedProviderAndEvidence(t *testing.T) {
	v := dependencyFixture()
	valid := DependencyBlock{Consumer: "app/frontend/page.tsx", Dependency: "app/frontend/package.json", Evidence: "npm run build: ENOENT package.json"}
	if err := ValidateDependencyBlock(v, valid); err != nil {
		t.Fatal(err)
	}
	for _, block := range []DependencyBlock{
		{Consumer: valid.Consumer, Dependency: valid.Dependency},
		{Consumer: "app/other.ts", Dependency: valid.Dependency, Evidence: valid.Evidence},
		{Consumer: valid.Consumer, Dependency: valid.Consumer, Evidence: valid.Evidence},
		{Consumer: valid.Consumer, Dependency: "react", Evidence: valid.Evidence},
	} {
		if err := ValidateDependencyBlock(v, block); err == nil {
			t.Fatalf("accepted invalid report: %+v", block)
		}
	}
}

func TestDependencyRepairRoutesAndPreservesCompletedWork(t *testing.T) {
	for _, outcome := range []string{"success", "failure", "dependency_blocked"} {
		t.Run(outcome, func(t *testing.T) {
			town, rig := t.TempDir(), "sample"
			dir := filepath.Join(town, rig, "mayor", "rig")
			if err := os.MkdirAll(filepath.Join(dir, "app"), 0755); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(dir, "app", "keep.txt")
			if err := os.WriteFile(file, []byte("completed work"), 0644); err != nil {
				t.Fatal(err)
			}
			v := dependencyFixture()
			if outcome != "dependency_blocked" {
				v.DeliveryPhases[0].FileDependencies = map[string][]string{"app/frontend/page.tsx": {"app/frontend/package.json"}}
			}
			m := NewManager(town)
			m.LoadTemplate(&WorkflowTemplate{ID: "dependency-test", InitialState: "worker", Validation: v, States: map[string]State{
				"worker": {Role: "polecat", Hooks: StateHooks{DependencyReview: true}, Transitions: map[string]Transition{"success": {To: "done"}, "failure": {To: "worker"}, "dependency_blocked": {To: "repair"}}},
				"repair": {Role: "architect"}, "done": {},
			}})
			id, err := m.StartWorkflow("dependency-test", map[string]string{"rig": rig})
			if err != nil {
				t.Fatal(err)
			}
			report, _ := json.Marshal(DependencyBlock{Consumer: "app/frontend/page.tsx", Dependency: "app/frontend/package.json", Evidence: "npm run build: missing manifest"})
			next, err := m.CompleteTask(id, outcome, "", string(report), "captured build output", nil)
			if err != nil || next != "repair" {
				t.Fatalf("next=%s err=%v", next, err)
			}
			rework := m.instances[id].PendingRework
			if rework == nil || rework.Outcome != "dependency_blocked" || !strings.Contains(rework.Feedback, "app/frontend/package.json") || !strings.Contains(rework.Feedback, "Preserve existing implementation") {
				t.Fatalf("lost dependency evidence: %+v", rework)
			}
			data, err := os.ReadFile(file)
			if err != nil || string(data) != "completed work" {
				t.Fatalf("completed file changed: %q %v", data, err)
			}
		})
	}
}

func TestDependencyContractChangeInvalidatesOnlyAffectedCompletion(t *testing.T) {
	previous := dependencyFixture()
	previous.CompletedPhaseIDsField = []string{"foundation", "client"}
	next := dependencyFixture()
	next.DeliveryPhases[1].FileDependencies = map[string][]string{"app/frontend/package.json": {"app/frontend/page.tsx"}}
	reconcileDeliveryContractProgress(&next, previous)
	if strings.Join(next.CompletedPhaseIDsField, ",") != "foundation" || next.ActivePhaseIDField != "client" {
		t.Fatalf("progress not reconciled: %+v", next)
	}
}

func TestDependencyReviewWorkflowConfiguration(t *testing.T) {
	for _, name := range []string{"rig-flow", "req-flow"} {
		data, err := os.ReadFile(filepath.Join("town", "templates", name+".yaml"))
		if err != nil {
			t.Fatal(err)
		}
		var tpl WorkflowTemplate
		if err := yaml.Unmarshal(data, &tpl); err != nil {
			t.Fatal(err)
		}
		for _, stateName := range []string{"plan_review", "project_setup", "implementation"} {
			state := tpl.States[stateName]
			if !state.Hooks.DependencyReview || state.Transitions["dependency_blocked"].To != "design" {
				t.Fatalf("%s/%s lacks dependency recovery", name, stateName)
			}
		}
	}
}

func TestDependencyContractRoundTripAndRefinement(t *testing.T) {
	doc := "## Delivery phases\n```yaml\ndelivery_phases:\n  - id: base\n    required_files: [app/a.py, app/b.py]\n    qa_verify_command: python -m compileall app\n    file_dependencies:\n      app/a.py: [app/b.py]\n  - id: refine\n    required_files: [app/b.py]\n    qa_verify_command: python -m compileall app\n    depends_on: [base]\n    file_dependencies:\n      app/b.py: [app/a.py]\n```\n"
	phases, explicit, err := architectureDeliveryContract(doc)
	if err != nil || !explicit || len(phases) != 2 || len(phases[0].FileDependencies["app/a.py"]) != 1 {
		t.Fatalf("contract lost: %v %v %v", phases, explicit, err)
	}
	v := WorkflowValidation{DeliveryPhases: phases, ActivePhaseIDField: "base"}
	if problems := DeliveryDependencyProblems(t.TempDir(), v); len(problems) != 0 {
		t.Fatalf("later refinement created spurious cycle: %v", problems)
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var restored WorkflowValidation
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.DeliveryPhases[0].FileDependencies["app/a.py"][0] != "app/b.py" {
		t.Fatalf("JSON dropped dependencies: %s", encoded)
	}
}

func TestDependencyRepairInvalidatesDependentPhaseCreditOnly(t *testing.T) {
	previous := dependencyFixture()
	previous.DeliveryPhases = append(previous.DeliveryPhases, DeliveryPhase{ID: "independent", RequiredFiles: []string{"app/notes.md"}})
	previous.CompletedPhaseIDsField = []string{"foundation", "client", "independent"}
	next := dependencyFixture()
	next.DeliveryPhases = append(next.DeliveryPhases, previous.DeliveryPhases[2])
	next.DeliveryPhases[0].QAVerifyCommand = "test -s app/frontend/page.tsx"
	reconcileDeliveryContractProgress(&next, previous)
	if strings.Join(next.CompletedPhaseIDsField, ",") != "independent" || next.ActivePhaseIDField != "foundation" {
		t.Fatalf("stale dependent credit or lost independent credit: %+v", next)
	}
}
