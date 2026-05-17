package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkflowValidation_RequirementsFilePath(t *testing.T) {
	v := WorkflowValidation{RequiredFiles: []string{"pkg/main.py", "pkg/requirements.txt"}}
	if got := v.RequirementsFilePath(); got != "pkg/requirements.txt" {
		t.Fatalf("got %q", got)
	}
	if v.RequirementsFilePath() == "" && len(v.RequiredFiles) == 0 {
		return
	}
}

func TestClampProfileValidation(t *testing.T) {
	tests := []struct {
		name string
		in   WorkflowValidation
		want WorkflowValidation
	}{
		{
			name: "absurd plan from llm",
			in:   WorkflowValidation{MinPlanBytes: 17496, MinArchitectureBytes: 50000},
			want: WorkflowValidation{MinPlanBytes: DefaultMinPlanBytes, MinArchitectureBytes: DefaultMinArchitectureBytes},
		},
		{
			name: "zero uses defaults",
			in:   WorkflowValidation{},
			want: WorkflowValidation{MinPlanBytes: DefaultMinPlanBytes, MinArchitectureBytes: DefaultMinArchitectureBytes},
		},
		{
			name: "in range kept",
			in:   WorkflowValidation{MinPlanBytes: 3000, MinArchitectureBytes: 6000},
			want: WorkflowValidation{MinPlanBytes: 3000, MinArchitectureBytes: 6000},
		},
		{
			name: "below floor raised",
			in:   WorkflowValidation{MinPlanBytes: 50},
			want: WorkflowValidation{MinPlanBytes: MinArtifactBytesFloor, MinArchitectureBytes: DefaultMinArchitectureBytes},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ClampProfileValidation(tc.in)
			if got.MinPlanBytes != tc.want.MinPlanBytes || got.MinArchitectureBytes != tc.want.MinArchitectureBytes {
				t.Fatalf("ClampProfileValidation() = plan %d arch %d, want plan %d arch %d",
					got.MinPlanBytes, got.MinArchitectureBytes, tc.want.MinPlanBytes, tc.want.MinArchitectureBytes)
			}
		})
	}
}

func TestDefaultWorkflowValidation(t *testing.T) {
	v := DefaultWorkflowValidation()
	if v.BeadTitleContains != "Implement " {
		t.Fatalf("bead prefix: %q", v.BeadTitleContains)
	}
	if len(v.RequiredFiles) != 0 {
		t.Fatalf("required files should be empty until profile loaded: %v", v.RequiredFiles)
	}
}

func TestWithDefaults_partial(t *testing.T) {
	v := WorkflowValidation{BeadTitleContains: "Implement api/"}.WithDefaults()
	if v.BeadTitleContains != "Implement api/" {
		t.Fatalf("got %q", v.BeadTitleContains)
	}
	if v.UnittestModule != "" {
		t.Fatalf("expected empty unittest when unset and no QA command: %q", v.UnittestModule)
	}
}

func TestPromptVars_includesUnittestCommandHint(t *testing.T) {
	v := WorkflowValidation{QAVerifyCommand: "pytest -q"}.WithDefaults()
	vars := v.PromptVars()
	if vars["unittest_command_hint"] != "python3 -m pytest -q" {
		t.Fatalf("hint: %q", vars["unittest_command_hint"])
	}
	v2 := WorkflowValidation{UnittestModule: "pkg.t"}.WithDefaults()
	if h := v2.PromptVars()["unittest_command_hint"]; h != "python3 -m unittest pkg.t" {
		t.Fatalf("hint: %q", h)
	}
}

func TestUsesPythonVenv(t *testing.T) {
	v := WorkflowValidation{RequiredFiles: []string{"backend/requirements.txt"}}
	if !v.UsesPythonVenv() {
		t.Fatal("expected venv for requirements.txt project")
	}
	if v.PythonVenvRelDir() != ".venv" {
		t.Fatalf("dir: %q", v.PythonVenvRelDir())
	}
	off := WorkflowValidation{PythonVenvDir: "off", RequiredFiles: []string{"a.py"}}
	if off.UsesPythonVenv() {
		t.Fatal("off should disable venv")
	}
}

func TestForbiddenRigRootBasenames(t *testing.T) {
	v := WorkflowValidation{
		RequiredFiles: []string{"backend/api.py", "backend/main.py"},
	}.WithDefaults()
	bases := v.ForbiddenRigRootBasenames()
	if len(bases) != 2 {
		t.Fatalf("got %v", bases)
	}
}

func TestBuildTaskPayload_includesValidation(t *testing.T) {
	dir := t.TempDir()
	promptDir := filepath.Join(dir, "orchestrator", "prompts", "rig-flow")
	if err := os.MkdirAll(promptDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptDir, "kickoff.md"), []byte("rig {{rig}} prefix {{bead_title_contains}}"), 0644); err != nil {
		t.Fatal(err)
	}
	tpl := &WorkflowTemplate{
		ID:           "rig-flow",
		InitialState: "kickoff",
		Validation: WorkflowValidation{
			BeadTitleContains: "Implement service/",
			UnittestModule:    "backend.test_service",
		},
		States: map[string]State{
			"kickoff": {Role: "mayor", PromptFile: "prompts/rig-flow/kickoff.md"},
		},
	}
	m := NewManager(dir)
	m.LoadTemplate(tpl)
	inst := &WorkflowInstance{
		ID: "wf-1", TemplateID: "rig-flow", CurrentState: "kickoff",
		Variables: map[string]string{"rig": "myrig"}, Status: "running",
	}
	state, _ := inst.GetCurrentTask(tpl)
	payload, err := m.BuildTaskPayload(inst, tpl, state)
	if err != nil {
		t.Fatal(err)
	}
	val, ok := payload["validation"].(WorkflowValidation)
	if !ok {
		t.Fatalf("validation type %T", payload["validation"])
	}
	if val.BeadTitleContains != "Implement service/" {
		t.Fatalf("got %+v", val)
	}
	sp := payload["system_prompt"].(string)
	if !strings.Contains(sp, "Implement service/") {
		t.Fatalf("prompt missing prefix: %q", sp)
	}
}

func TestLoadTemplatesFromDir_readsValidation(t *testing.T) {
	dir := t.TempDir()
	tmplDir := filepath.Join(dir, "orchestrator", "templates")
	if err := os.MkdirAll(tmplDir, 0755); err != nil {
		t.Fatal(err)
	}
	yaml := `id: custom-flow
initial_state: kickoff
validation:
  bead_title_contains: "Build feature/"
  unittest_module: pkg.test_feature
  required_files:
    - pkg/feature.py
states:
  kickoff:
    role: mayor
    instructions: go
`
	if err := os.WriteFile(filepath.Join(tmplDir, "custom-flow.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	m := NewManager(dir)
	if err := m.LoadTemplatesFromDir(tmplDir); err != nil {
		t.Fatal(err)
	}
	tpl := m.templates["custom-flow"]
	if tpl == nil {
		t.Fatal("template not loaded")
	}
	if tpl.Validation.BeadTitleContains != "Build feature/" {
		t.Fatalf("got %+v", tpl.Validation)
	}
}
