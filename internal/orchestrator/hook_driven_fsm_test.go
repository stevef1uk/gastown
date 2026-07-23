package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hookDrivenSmokeTemplate is a minimal FSM used to prove YAML hooks flow through FetchTask and transitions.
func hookDrivenSmokeTemplate() *WorkflowTemplate {
	return &WorkflowTemplate{
		ID:           "hook-driven-smoke",
		InitialState: "design",
		Validation: WorkflowValidation{
			MinArchitectureBytes: 50,
			MinPlanBytes:         50,
		},
		States: map[string]State{
			"design": {
				Role:         "architect",
				Instructions: "write architecture",
				Hooks: StateHooks{
					CmdGuard:  "design",
					Track:     "design",
					Artifacts: "design",
					RetryHint: "design retry",
				},
				Transitions: map[string]Transition{
					"success": {To: "planning"},
				},
			},
			"planning": {
				Role:         "planner",
				Instructions: "write plan",
				Hooks: StateHooks{
					CmdGuard: "planning",
					Env:      StateEnvHooks{BeadsDir: true},
					Track:    "planning",
					Artifacts: "planning",
					RetryHint: "planning retry",
				},
				Transitions: map[string]Transition{
					"success": {To: "completed"},
				},
			},
			"completed": {
				Role:         "mayor",
				Instructions: "done",
			},
		},
	}
}

func TestRigFlowYAML_allPipelineStatesHaveHooks(t *testing.T) {
	tpl := loadRigFlowTemplate(t)
	want := map[string]string{
		"kickoff":         "",
		"design":          "design",
		"planning":        "planning",
		"plan_review":     "plan_review",
		"project_setup":   "project_setup",
		"implementation":  "implementation",
		"qa_review":       "qa",
	}
	for state, guard := range want {
		st, ok := tpl.States[state]
		if !ok {
			t.Fatalf("missing state %q", state)
		}
		if guard != "" && st.Hooks.CmdGuard != guard {
			t.Fatalf("state %q cmd_guard = %q, want %q", state, st.Hooks.CmdGuard, guard)
		}
		if state != "kickoff" && st.Hooks.Track == "" {
			t.Fatalf("state %q missing track hook", state)
		}
		if state != "kickoff" && st.Hooks.Artifacts == "" {
			t.Fatalf("state %q missing artifacts hook", state)
		}
	}
}

func TestRigFlowYAML_implementationHasStallRecoveryHooks(t *testing.T) {
	t.Setenv(StateTimeoutEnvVar, "")
	tpl := loadRigFlowTemplate(t)
	st, ok := tpl.States["implementation"]
	if !ok {
		t.Fatal("missing implementation state")
	}
	if st.Hooks.StateTimeoutSeconds != 7200 {
		t.Fatalf("state_timeout_seconds = %d, want 7200", st.Hooks.StateTimeoutSeconds)
	}
	if st.Hooks.EffectiveStateTimeoutSeconds() != 7200 {
		t.Fatalf("effective state timeout = %d, want 7200", st.Hooks.EffectiveStateTimeoutSeconds())
	}
	if len(st.Hooks.OnTimeout) != 1 || st.Hooks.OnTimeout[0] != "recover_implementation_stall" {
		t.Fatalf("on_timeout = %v, want [recover_implementation_stall]", st.Hooks.OnTimeout)
	}
	if len(st.Hooks.OnStateTimeout) != 1 || st.Hooks.OnStateTimeout[0] != "recover_implementation_stall" {
		t.Fatalf("on_state_timeout = %v, want [recover_implementation_stall]", st.Hooks.OnStateTimeout)
	}
	if got := st.Hooks.EffectiveOnStateTimeoutHooks(); len(got) != 1 || got[0] != "recover_implementation_stall" {
		t.Fatalf("EffectiveOnStateTimeoutHooks = %v", got)
	}
	if st.Hooks.EffectiveCmdTimeoutSeconds() != 30 {
		t.Fatalf("cmd_timeout_seconds = %d, want 30", st.Hooks.CmdTimeoutSeconds)
	}
	trans, ok := st.Transitions["timeout"]
	if !ok || trans.To != "implementation" {
		t.Fatalf("timeout transition = %+v, want to implementation", st.Transitions["timeout"])
	}
}

func TestRigFlowYAML_qaReviewHasFastFailHooks(t *testing.T) {
	t.Setenv(StateTimeoutEnvVar, "")
	tpl := loadRigFlowTemplate(t)
	st, ok := tpl.States["qa_review"]
	if !ok {
		t.Fatal("missing qa_review state")
	}
	if st.Hooks.EffectiveCmdTimeoutSeconds() != 30 {
		t.Fatalf("cmd_timeout_seconds = %d, want 30", st.Hooks.CmdTimeoutSeconds)
	}
	if st.Hooks.EffectiveStateTimeoutSeconds() != 1800 {
		t.Fatalf("state_timeout_seconds = %d, want 1800", st.Hooks.StateTimeoutSeconds)
	}
	if !st.Hooks.AppendGoCompileContext {
		t.Fatal("qa_review must set append_go_compile_context: true")
	}
	trans, ok := st.Transitions["timeout"]
	if !ok || trans.To != "implementation" {
		t.Fatalf("timeout transition = %+v, want to implementation", st.Transitions["timeout"])
	}
}

func TestRigFlowYAML_planningHasTimeoutHooks(t *testing.T) {
	t.Setenv(StateTimeoutEnvVar, "")
	tpl := loadRigFlowTemplate(t)
	st, ok := tpl.States["planning"]
	if !ok {
		t.Fatal("missing planning state")
	}
	if st.Hooks.EffectiveStateTimeoutSeconds() != 1800 {
		t.Fatalf("state_timeout_seconds = %d, want 1800", st.Hooks.StateTimeoutSeconds)
	}
	if len(st.Hooks.OnTimeout) != 1 || st.Hooks.OnTimeout[0] != "sync_planning_on_timeout" {
		t.Fatalf("on_timeout = %v, want [sync_planning_on_timeout]", st.Hooks.OnTimeout)
	}
	trans, ok := st.Transitions["timeout"]
	if !ok || trans.To != "planning" {
		t.Fatalf("timeout transition = %+v, want to planning", st.Transitions["timeout"])
	}
}

func TestRigFlowYAML_projectSetupHasAutoVerifyAndVenvCreate(t *testing.T) {
	tpl := loadRigFlowTemplate(t)
	h := tpl.States["project_setup"].Hooks
	if h.Env.PythonVenv != "create" {
		t.Fatalf("python_venv = %q, want create", h.Env.PythonVenv)
	}
	if len(h.AutoVerify) < 2 {
		t.Fatalf("expected auto_verify rules, got %d", len(h.AutoVerify))
	}
	var hasGo, hasPip bool
	for _, av := range h.AutoVerify {
		switch av.When {
		case "go_mod_tidy":
			hasGo = av.Verify == "go_setup"
		case "pip_install":
			hasPip = av.Verify == "python_setup"
		}
	}
	if !hasGo || !hasPip {
		t.Fatalf("auto_verify: go=%v pip=%v rules=%v", hasGo, hasPip, h.AutoVerify)
	}
	wantPost := []string{"sync_planning_artifacts", "ensure_http_implementation_config", "close_project_setup_beads"}
	if len(h.PostArtifactSuccess) != len(wantPost) {
		t.Fatalf("post_artifact_success = %v, want %v", h.PostArtifactSuccess, wantPost)
	}
	for i, w := range wantPost {
		if h.PostArtifactSuccess[i] != w {
			t.Fatalf("post_artifact_success = %v, want %v", h.PostArtifactSuccess, wantPost)
		}
	}
	if !h.AutoVerifyOKClearsCmdFailure {
		t.Fatal("expected auto_verify_ok_clears_cmd_failure on project_setup")
	}
}

func TestHookDrivenFSM_fetchTaskDeliversHooksPerState(t *testing.T) {
	dir := t.TempDir()
	promptDir := filepath.Join(dir, "orchestrator", "prompts", "rig-flow")
	if err := os.MkdirAll(promptDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptDir, "design.md"), []byte("# Design {{rig}}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(promptDir, "planning.md"), []byte("# Plan {{rig}}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	tpl := hookDrivenSmokeTemplate()
	design := tpl.States["design"]
	design.PromptFile = "prompts/rig-flow/design.md"
	tpl.States["design"] = design
	planning := tpl.States["planning"]
	planning.PromptFile = "prompts/rig-flow/planning.md"
	tpl.States["planning"] = planning

	m := NewManager(dir)
	m.LoadTemplate(tpl)
	wfID, err := m.StartWorkflow(tpl.ID, map[string]string{"rig": "myrig"})
	if err != nil {
		t.Fatal(err)
	}

	payload, err := m.FetchTask("myrig/architect")
	if err != nil {
		t.Fatal(err)
	}
	if payload["state"] != "design" {
		t.Fatalf("state = %v", payload["state"])
	}
	task := payloadToTask(t, payload)
	if task.Hooks.CmdGuard != "design" || task.Hooks.Track != "design" {
		t.Fatalf("design hooks: %+v", task.Hooks)
	}
	if !strings.Contains(task.Hooks.RetryHint, "design retry") {
		t.Fatalf("retry_hint: %q", task.Hooks.RetryHint)
	}

	next, err := m.CompleteTask(wfID, "success", "myrig/architect", "arch ok", "")
	if err != nil || next != "planning" {
		t.Fatalf("transition: next=%q err=%v", next, err)
	}

	payload, err = m.FetchTask("planner")
	if err != nil {
		t.Fatal(err)
	}
	if payload["state"] != "planning" {
		t.Fatalf("state = %v", payload["state"])
	}
	task = payloadToTask(t, payload)
	if task.Hooks.CmdGuard != "planning" {
		t.Fatalf("planning cmd_guard = %q", task.Hooks.CmdGuard)
	}
	if !task.Hooks.Env.BeadsDir {
		t.Fatal("planning should set env.beads_dir")
	}
	if strings.Contains(task.Hooks.RetryHint, "design retry") {
		t.Fatal("planning task should not carry design retry hint")
	}
}

func TestHookDrivenFSM_completeTaskAdvancesWithHooksUnchangedInTemplate(t *testing.T) {
	m, wfID := loadTestManager(t, hookDrivenSmokeTemplate())

	_, _ = m.CompleteTask(wfID, "success", "mockrig/architect", "", "")
	payload, err := m.FetchTask("planner")
	if err != nil {
		t.Fatal(err)
	}
	task := payloadToTask(t, payload)
	if task.WorkflowID != wfID {
		t.Fatalf("workflow_id = %q", task.WorkflowID)
	}
	if task.Hooks.CmdGuard != "planning" {
		t.Fatalf("got planning hooks on transition: %+v", task.Hooks)
	}
}
