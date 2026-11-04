package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanReviewTemplatesRequireExplicitVerdict(t *testing.T) {
	for _, load := range []func() (*WorkflowTemplate, error){LoadRigFlowTemplate, LoadReqFlowTemplate} {
		tpl, err := load()
		if err != nil {
			t.Fatal(err)
		}
		if !tpl.States["plan_review"].Hooks.RequireExplicitOutcome {
			t.Errorf("%s plan review can bypass the QA verdict", tpl.ID)
		}
	}
}

func TestPlanningMixedStackPhaseVerification(t *testing.T) {
	for _, tc := range []struct {
		id     string
		files  []string
		verify string
	}{
		{"browser-ui", []string{"demo/web/index.html", "demo/web/app.js", "demo/web/style.css", "demo/tests/e2e.spec.js"}, "test -s demo/web/index.html && node --check demo/web/app.js"},
		{"browser-tests", []string{"demo/package.json", "demo/playwright.config.js", "demo/tests/e2e.spec.js"}, "cd demo && npx playwright test"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			town, rig := t.TempDir(), "mockrig"
			rigDir := filepath.Join(town, rig, "mayor", "rig")
			if err := os.MkdirAll(rigDir, 0755); err != nil {
				t.Fatal(err)
			}
			v := WorkflowValidation{
				LayoutRoot: "demo", TestRunner: "go", BeadTitleContains: "Implement ", MinPlanBytes: 10,
				QAVerifyCommand:    "cd demo && go test ./...",
				ActivePhaseIDField: tc.id,
				DeliveryPhases: []DeliveryPhase{
					{ID: "backend", RequiredFiles: []string{"demo/cmd/server/main.go"}, QAVerifyCommand: "cd demo && go test ./..."},
					{ID: tc.id, RequiredFiles: tc.files, QAVerifyCommand: tc.verify},
				},
			}
			var beads []PlanBead
			for i, path := range tc.files {
				beads = append(beads, PlanBead{ID: "mock-" + string(rune('a'+i)), Title: "Implement " + path + " per architecture"})
				if got := ImplementationVerifyCommandForBead(v, rigDir, path); got != tc.verify {
					t.Errorf("%s verify = %q; want phase command %q", path, got, tc.verify)
				}
			}
			setListImplementBeadsByStatusHook(t, town, rig, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
				if status == "open" {
					return beads, nil
				}
				return nil, nil
			})
			if _, err := WritePlanningPlanMD(town, rig, v); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(rigDir, "plan.md"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), "- Verify: `"+tc.verify+"`") || strings.Contains(string(data), "go build") {
				t.Fatalf("plan replaced phase verification with Go compilation:\n%s", data)
			}
		})
	}
}

func TestPlanningSyncPreservesReviewedAcceptance(t *testing.T) {
	town, rig := t.TempDir(), "mockrig"
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	beadsDir := filepath.Join(town, rig, ".beads")
	binDir := t.TempDir()
	for _, dir := range []string{rigDir, beadsDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	bead := PlanBead{ID: "mock-abc", Title: "Implement demo/internal/store/schema.go per architecture"}
	rows, err := json.Marshal([]PlanBead{bead})
	if err != nil {
		t.Fatal(err)
	}
	stub := fmt.Sprintf("#!/bin/sh\ncase \"$*\" in\n*closed*|*in_progress*) printf '[]\\n';;\n*) printf '%%s\\n' '%s';;\nesac\n", rows)
	if err := os.WriteFile(filepath.Join(binDir, "bd"), []byte(stub), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("BEADS_DIR", beadsDir)
	v := WorkflowValidation{
		LayoutRoot: "demo", BeadTitleContains: "Implement ", MinPlanBytes: 10,
		RequiredFiles:   []string{"demo/internal/store/schema.go"},
		QAVerifyCommand: "cd demo && go test ./...",
	}
	writeMinimalPlanningRigDocs(t, rigDir)
	plan := "# Implementation plan\n\n## Bead map\n\n### mock-abc: demo/internal/store/schema.go\n" +
		"- Verify: `cd demo && go test ./...`\n- Acceptance: Initializing the schema twice preserves existing records.\n"
	plan = padPlanningPlanMD(plan, EffectiveMinPlanBytes(rigDir, v))
	planPath := filepath.Join(rigDir, "plan.md")
	if err := os.WriteFile(planPath, []byte(plan), 0644); err != nil {
		t.Fatal(err)
	}
	if !RequiresExactImplementPaths(v) || !BeadsDatabaseReady(town, rig) || PlanningPlanMDNeedsRefresh(town, rig, v) {
		t.Fatal("fixture must be a valid nested-path plan with available beads")
	}
	if _, err := RunPreRunHook("sync_planning_artifacts", town, rig, v); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != plan {
		t.Fatalf("planning sync erased reviewed acceptance:\n%s", data)
	}
	profile, err := json.Marshal(map[string]any{"version": 1, "validation": v})
	if err != nil {
		t.Fatal(err)
	}
	profileDir := filepath.Join(rigDir, ".gastown")
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profileDir, "workflow-profile.json"), profile, 0644); err != nil {
		t.Fatal(err)
	}
	m := NewManager(town)
	tpl := &WorkflowTemplate{
		ID: "preserve-plan", InitialState: "planning",
		States: map[string]State{
			"planning":       {Role: "planner", Transitions: map[string]Transition{"success": {To: "plan_review"}}},
			"plan_review":    {Role: "qa", Transitions: map[string]Transition{"success": {To: "project_setup"}}},
			"project_setup":  {Role: "polecat", Transitions: map[string]Transition{"success": {To: "implementation"}}},
			"implementation": {Role: "polecat"},
		},
	}
	m.LoadTemplate(tpl)
	m.instances["wf-preserve"] = &WorkflowInstance{
		ID: "wf-preserve", TemplateID: tpl.ID, CurrentState: "planning", Status: "running",
		Variables: map[string]string{"rig": rig},
	}
	for _, step := range []struct{ role, next string }{
		{"planner", "plan_review"}, {"qa", "project_setup"}, {"polecat", "implementation"},
	} {
		next, err := m.CompleteTask("wf-preserve", "success", rig+"/"+step.role, "reviewed plan", "", nil)
		if err != nil || next != step.next {
			t.Fatalf("%s handoff: next=%q err=%v", step.role, next, err)
		}
		data, err := os.ReadFile(planPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != plan {
			t.Fatalf("%s handoff erased reviewed acceptance:\n%s", step.role, data)
		}
	}
}
