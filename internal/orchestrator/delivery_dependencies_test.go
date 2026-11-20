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

func TestPlanDependenciesIgnoreParentheticalExplanations(t *testing.T) {
	files := []string{"taskboard/main.py", "taskboard/templates/index.html", "taskboard/static/app.js"}
	for _, tc := range []struct {
		name        string
		dependsOn   string
		wantCycle   bool
		wantDepends string
	}{
		{"none with negated path", "`<none>` (the template must not depend on `taskboard/main.py`)", false, ""},
		{"dependency with negated path", "`taskboard/static/app.js` (not `taskboard/main.py`)", false, "taskboard/static/app.js"},
		{"dependency after annotation", "`taskboard/static/app.js` (phase 1), `te-main` (phase 2)", true, "taskboard/static/app.js,taskboard/main.py"},
		{"nested annotation", "`taskboard/static/app.js` (not `taskboard/main.py` (phase 1)), `te-main`", true, "taskboard/static/app.js,taskboard/main.py"},
		{"unclosed annotation preserves dependencies", "`taskboard/static/app.js` (phase 1, `te-main`", true, "taskboard/static/app.js,taskboard/main.py"},
		{"real cycle", "`taskboard/main.py`", true, "taskboard/main.py"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := "### te-main: taskboard/main.py\n- Depends on: `taskboard/templates/index.html`\n" +
				"### te-template: taskboard/templates/index.html\n- Depends on: " + tc.dependsOn + "\n"
			edges := planFileDependencies(doc, files)
			got := strings.Join(edges["taskboard/templates/index.html"], ",")
			if got != tc.wantDepends {
				t.Errorf("template dependencies = %q, want %q", got, tc.wantDepends)
			}
			_, err := dependencyBuildOrder(files, edges)
			if (err != nil) != tc.wantCycle {
				t.Errorf("cycle error = %v, want cycle %t", err, tc.wantCycle)
			}
		})
	}
}

func TestPlanDependenciesDetectCycleAfterAnnotation(t *testing.T) {
	files := []string{"taskboard/main.py", "taskboard/templates/index.html", "taskboard/static/app.js"}
	doc := "### te-template: taskboard/templates/index.html\n" +
		"- Depends on: taskboard/main.py (Phase 1), taskboard/static/app.js (same phase extension)\n" +
		"### te-js: taskboard/static/app.js\n- Depends on: taskboard/templates/index.html\n"
	edges := planFileDependencies(doc, files)
	_, err := dependencyBuildOrder(files, edges)
	if err == nil || !strings.Contains(err.Error(), "file dependency cycle") {
		t.Fatalf("expected file dependency cycle, got %v (edges %v)", err, edges)
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

func TestPlanDependencyRepairRoutesToPlanner(t *testing.T) {
	for _, state := range []string{"planning", "plan_review", "project_setup", "implementation"} {
		t.Run(state, func(t *testing.T) {
			town, rig := t.TempDir(), "sample"
			dir := filepath.Join(town, rig, "mayor", "rig")
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			plan := "### sm-a: app/a.txt\n- Depends on: app/b.txt\n### sm-b: app/b.txt\n- Depends on: app/a.txt\n"
			if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte(plan), 0644); err != nil {
				t.Fatal(err)
			}
			v := WorkflowValidation{LayoutRoot: "app", ActivePhaseIDField: "one", DeliveryPhases: []DeliveryPhase{
				{ID: "one", RequiredFiles: []string{"app/a.txt", "app/b.txt"}},
			}}
			v.RequiredFiles = v.UnionRequiredFiles()
			m := NewManager(town)
			m.LoadTemplate(&WorkflowTemplate{ID: "plan-dependency-test", InitialState: state, Validation: v, States: map[string]State{
				state: {Hooks: StateHooks{DependencyReview: true}, Transitions: map[string]Transition{
					"success": {To: "done"}, "dependency_blocked": {To: "design"}, "plan_dependency_blocked": {To: "planning"},
				}},
				"done": {}, "design": {},
			}})
			id, err := m.StartWorkflow("plan-dependency-test", map[string]string{"rig": rig})
			if err != nil {
				t.Fatal(err)
			}
			next, err := m.CompleteTask(id, "success", "", "approved", "", nil)
			if err != nil || next != "planning" {
				t.Fatalf("next=%q err=%v", next, err)
			}
			rework := m.instances[id].PendingRework
			if rework == nil || rework.Outcome != "plan_dependency_blocked" || !strings.Contains(rework.Feedback, "app/a.txt") || !strings.Contains(rework.Feedback, "plan.md") {
				t.Fatalf("lost plan dependency guidance: %+v", rework)
			}
			if err := ValidatePlanningPhaseGate(town, rig, "planning", v); err == nil || !strings.Contains(err.Error(), "file dependency cycle") {
				t.Fatalf("planning gate accepted cycle: %v", err)
			}
		})
	}
}

func TestPlanDependencyRepairKeepsCanonicalDefectsWithArchitect(t *testing.T) {
	town, rig := t.TempDir(), "sample"
	dir := filepath.Join(town, rig, "mayor", "rig")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{LayoutRoot: "app", ActivePhaseIDField: "one", DeliveryPhases: []DeliveryPhase{{
		ID: "one", RequiredFiles: []string{"app/a.txt", "app/b.txt"},
		FileDependencies: map[string][]string{"app/a.txt": {"app/b.txt"}, "app/b.txt": {"app/a.txt"}},
	}}}
	if problems := DeliveryPlanDependencyProblems(dir, v); len(problems) != 0 {
		t.Fatalf("canonical cycle classified as plan defect: %v", problems)
	}
	m := NewManager(town)
	m.LoadTemplate(&WorkflowTemplate{ID: "canonical-dependency-test", InitialState: "plan_review", Validation: v, States: map[string]State{
		"plan_review": {Hooks: StateHooks{DependencyReview: true}, Transitions: map[string]Transition{
			"success": {To: "done"}, "dependency_blocked": {To: "design"}, "plan_dependency_blocked": {To: "planning"},
		}},
		"done": {}, "design": {}, "planning": {},
	}})
	id, err := m.StartWorkflow("canonical-dependency-test", map[string]string{"rig": rig})
	if err != nil {
		t.Fatal(err)
	}
	next, err := m.CompleteTask(id, "success", "", "approved", "", nil)
	if err != nil || next != "design" || m.instances[id].PendingRework == nil || m.instances[id].PendingRework.Outcome != "dependency_blocked" {
		t.Fatalf("canonical defect: next=%q rework=%+v err=%v", next, m.instances[id].PendingRework, err)
	}
}

func TestPlanDependencyRepairDetectsCombinedEdges(t *testing.T) {
	dir := t.TempDir()
	v := WorkflowValidation{ActivePhaseIDField: "one", DeliveryPhases: []DeliveryPhase{{
		ID: "one", RequiredFiles: []string{"app/a.txt", "app/b.txt"},
		FileDependencies: map[string][]string{"app/a.txt": {"app/b.txt"}},
	}}}
	plan := "### sm-b: app/b.txt\n- Depends on: app/a.txt\n"
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte(plan), 0644); err != nil {
		t.Fatal(err)
	}
	if problems := DeliveryPlanDependencyProblems(dir, v); len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "file dependency cycle") {
		t.Fatalf("combined plan and canonical cycle missed: %v", problems)
	}
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte("### sm-b: app/b.txt\n- Depends on: <none>\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if problems := DeliveryPlanDependencyProblems(dir, v); len(problems) != 0 {
		t.Fatalf("acyclic plan incorrectly rejected: %v", problems)
	}
}

func TestPlanDependencyRepairDetectsFuturePhasePrerequisite(t *testing.T) {
	dir := t.TempDir()
	v := WorkflowValidation{ActivePhaseIDField: "one", DeliveryPhases: []DeliveryPhase{
		{ID: "one", RequiredFiles: []string{"app/a.txt"}},
		{ID: "two", RequiredFiles: []string{"app/b.txt"}},
	}}
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte("### sm-a: app/a.txt\n- Depends on: app/b.txt\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if problems := DeliveryPlanDependencyProblems(dir, v); len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), "future phase") {
		t.Fatalf("plan's future-phase prerequisite missed: %v", problems)
	}
}

func TestPlanDependencyRepairBoundsRepeatedPlanningFailures(t *testing.T) {
	town, rig := t.TempDir(), "sample"
	dir := filepath.Join(town, rig, "mayor", "rig")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	plan := "### sm-a: app/a.txt\n- Depends on: app/b.txt\n### sm-b: app/b.txt\n- Depends on: app/a.txt\n"
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte(plan), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{ActivePhaseIDField: "one", DeliveryPhases: []DeliveryPhase{{
		ID: "one", RequiredFiles: []string{"app/a.txt", "app/b.txt"},
	}}}
	m := NewManager(town)
	m.LoadTemplate(&WorkflowTemplate{ID: "bounded-plan-dependency", InitialState: "planning", Validation: v, States: map[string]State{
		"planning": {Hooks: StateHooks{DependencyReview: true, ReviewFailureLimit: 2, ReviewRecoveryState: "review_recovery"}, Transitions: map[string]Transition{
			"success": {To: "done"}, "dependency_blocked": {To: "design"}, "plan_dependency_blocked": {To: "planning"},
		}},
		"review_recovery": {Role: "mayor"}, "done": {}, "design": {},
	}})
	id, err := m.StartWorkflow("bounded-plan-dependency", map[string]string{"rig": rig})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"planning", "review_recovery"} {
		next, err := m.CompleteTask(id, "success", "", "approved", "", nil)
		if err != nil || next != want {
			t.Fatalf("next=%q want=%q err=%v", next, want, err)
		}
	}
	if rework := m.instances[id].PendingRework; rework == nil || !strings.Contains(rework.Feedback, "app/a.txt") {
		t.Fatalf("recovery lost dependency evidence: %+v", rework)
	}
}

func TestPlanningWallTimeoutsReachRecovery(t *testing.T) {
	m := NewManager(t.TempDir())
	m.LoadTemplate(&WorkflowTemplate{ID: "planning-timeout-limit", InitialState: "planning", States: map[string]State{
		"planning":        {Hooks: StateHooks{ReviewFailureLimit: 2, ReviewRecoveryState: "review_recovery"}, Transitions: map[string]Transition{"timeout": {To: "planning"}}},
		"review_recovery": {Role: "mayor", Transitions: map[string]Transition{"planner_rework": {To: "planning"}}},
	}})
	id, err := m.StartWorkflow("planning-timeout-limit", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"planning", "review_recovery"} {
		next, err := m.applyStateTimeout(id)
		if err != nil || next != want {
			t.Fatalf("next=%q want=%q err=%v", next, want, err)
		}
	}
	if rework := m.instances[id].PendingRework; rework == nil || !strings.Contains(rework.Feedback, "Mayor must diagnose") {
		t.Fatalf("timeout recovery lost evidence: %+v", rework)
	}
	if next, err := m.CompleteTask(id, "planner_rework", "", "retry", "", nil); err != nil || next != "planning" {
		t.Fatalf("recovery handoff: next=%q err=%v", next, err)
	}
	for _, want := range []string{"planning", "review_recovery"} {
		next, err := m.applyStateTimeout(id)
		if err != nil || next != want {
			t.Fatalf("after recovery: next=%q want=%q err=%v", next, want, err)
		}
	}
	if inst := m.instances[id]; inst.Status != "paused" || !strings.Contains(inst.PauseReason, "timed out") {
		t.Fatalf("repeated timeouts did not pause: status=%q reason=%q", inst.Status, inst.PauseReason)
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
		for _, stateName := range []string{"planning", "plan_review", "project_setup", "implementation"} {
			state := tpl.States[stateName]
			if !state.Hooks.DependencyReview || state.Transitions["dependency_blocked"].To != "design" || state.Transitions["plan_dependency_blocked"].To != "planning" {
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
