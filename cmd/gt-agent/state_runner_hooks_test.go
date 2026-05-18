package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func loadRigFlowStateHooks(t *testing.T, state string) orchestrator.StateHooks {
	t.Helper()
	hooks, err := orchestrator.RigFlowStateHooks(state)
	if err != nil {
		t.Fatal(err)
	}
	return hooks
}

func rigFlowTask(t *testing.T, state string, v orchestrator.WorkflowValidation) *orchestrator.Task {
	t.Helper()
	return &orchestrator.Task{
		State:      state,
		TemplateID: "rig-flow",
		Validation: v,
		Hooks:      loadRigFlowStateHooks(t, state),
	}
}

// TestStateRunner_rigFlowHooksRejectForbiddenCommands proves cmd_guard from YAML blocks commands.
func TestStateRunner_rigFlowHooksRejectForbiddenCommands(t *testing.T) {
	t.Parallel()
	design := rigFlowTask(t, "design", orchestrator.DefaultWorkflowValidation())
	r := newStateRunner(design, t.TempDir(), "myrig")
	if err := r.validateCommand("python3 -m pip install -r requirements.txt"); err == nil {
		t.Fatal("design guard should reject pip install")
	}
	if err := r.validateCommand("git -C myrig/mayor/rig commit -m x"); err == nil {
		t.Fatal("design guard should reject git commit")
	}

	planning := rigFlowTask(t, "planning", orchestrator.DefaultWorkflowValidation())
	r = newStateRunner(planning, t.TempDir(), "myrig")
	if err := r.validateCommand("python3 -m pytest -q"); err == nil {
		t.Fatal("planning guard should reject python")
	}

	impl := rigFlowTask(t, "implementation", orchestrator.WorkflowValidation{
		RequiredFiles:   []string{"backend/requirements.txt"},
		QAVerifyCommand: "cd backend && python3 -m pytest -q",
	})
	r = newStateRunner(impl, t.TempDir(), "myrig")
	if err := r.validateCommand("python3 -m pip install -r backend/requirements.txt"); err == nil {
		t.Fatal("implementation guard should reject pip (project_setup installs deps)")
	}
}

// TestStateRunner_rigFlowHooksEnvFromYAML proves env hooks configure BEADS_DIR and venv mode.
func TestStateRunner_rigFlowHooksEnvFromYAML(t *testing.T) {
	town := t.TempDir()
	rigDir := filepath.Join(town, "myrig", "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(town, "myrig", ".beads"), 0755)

	planning := rigFlowTask(t, "planning", orchestrator.DefaultWorkflowValidation())
	env := newStateRunner(planning, town, "myrig").commandEnv([]string{"GT_ROOT=" + town})
	if got := envLookup(env, "BEADS_DIR"); !strings.HasSuffix(got, "myrig/.beads") {
		t.Fatalf("planning BEADS_DIR = %q", got)
	}

	setup := rigFlowTask(t, "project_setup", orchestrator.WorkflowValidation{
		RequiredFiles: []string{"backend/requirements.txt"},
	})
	if setup.Hooks.Env.PythonVenv != "create" {
		t.Fatalf("yaml python_venv = %q", setup.Hooks.Env.PythonVenv)
	}

	impl := rigFlowTask(t, "implementation", orchestrator.WorkflowValidation{
		RequiredFiles: []string{"backend/requirements.txt"},
	})
	if impl.Hooks.Env.PythonVenv != "activate" {
		t.Fatalf("yaml python_venv = %q", impl.Hooks.Env.PythonVenv)
	}
}

// TestStateRunner_rigFlowHooksTrackAndArtifacts proves track + artifacts hooks gate success.
func TestStateRunner_rigFlowHooksTrackAndArtifacts(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "myrig", "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	archPath := filepath.Join(rigDir, "architecture.md")

	minBytes := orchestrator.MinArtifactBytesFloor
	design := rigFlowTask(t, "design", orchestrator.WorkflowValidation{MinArchitectureBytes: minBytes})
	r := newStateRunner(design, dir, "myrig")
	if err := r.validateArtifacts("success"); err == nil {
		t.Fatal("expected error without arch write this run")
	}
	r.track.designArchWritten = true
	if err := os.WriteFile(archPath, make([]byte, minBytes), 0644); err != nil {
		t.Fatal(err)
	}
	if err := r.validateArtifacts("success"); err != nil {
		t.Fatalf("expected success after track+file: %v", err)
	}

	planPath := filepath.Join(rigDir, "plan.md")
	planning := rigFlowTask(t, "planning", orchestrator.WorkflowValidation{MinPlanBytes: minBytes})
	r = newStateRunner(planning, dir, "myrig")
	if err := os.WriteFile(planPath, make([]byte, minBytes), 0644); err != nil {
		t.Fatal(err)
	}
	r.track.beadCreateOK = true
	if err := r.validateArtifacts("success"); err != nil {
		t.Fatalf("planning with plan+beadCreate: %v", err)
	}
}

// TestStateRunner_rigFlowAutoVerifyHooksMatchCommands proves YAML auto_verify when clauses match intent.
func TestStateRunner_rigFlowAutoVerifyHooksMatchCommands(t *testing.T) {
	t.Parallel()
	setup := rigFlowTask(t, "project_setup", orchestrator.WorkflowValidation{
		QAVerifyCommand: "cd linkshelf && go test ./...",
	})
	r := newStateRunner(setup, t.TempDir(), "myrig")
	if !r.autoVerifyMatches("cd myrig/mayor/rig/linkshelf && go mod tidy", "go_mod_tidy") {
		t.Fatal("expected go_mod_tidy match")
	}
	if r.verifyCommand("go_with_tidy") == "" {
		t.Fatal("expected go verify command for Go profile")
	}

	pySetup := rigFlowTask(t, "project_setup", orchestrator.WorkflowValidation{
		RequiredFiles:   []string{"backend/requirements.txt"},
		QAVerifyCommand: "cd backend && python3 -m pytest -q",
	})
	r = newStateRunner(pySetup, t.TempDir(), "myrig")
	if !r.autoVerifyMatches("python3 -m pip install -r backend/requirements.txt", "pip_install") {
		t.Fatal("expected pip_install match")
	}
	if r.verifyCommand("python") == "" {
		t.Fatal("expected python verify for Python profile")
	}
}

// TestStateRunner_customYAMLHooksDriveBehavior proves a novel FSM state (not rig-flow) works via hooks alone.
func TestStateRunner_customYAMLHooksDriveBehavior(t *testing.T) {
	task := &orchestrator.Task{
		Hooks: orchestrator.StateHooks{
			CmdGuard:  "design",
			Track:     "design",
			Artifacts: "design",
			AutoVerify: []orchestrator.AutoVerifyHook{
				{When: "go_mod_tidy", Verify: "go_with_tidy"},
			},
		},
		Validation: orchestrator.WorkflowValidation{
			QAVerifyCommand:      "cd pkg && go test ./...",
			MinArchitectureBytes: 10,
		},
	}
	r := newStateRunner(task, t.TempDir(), "rig1")
	if err := r.validateCommand("mkdir -p rig1/mayor/rig/backend"); err == nil {
		t.Fatal("custom design guard should reject mkdir backend")
	}
	if !r.autoVerifyMatches("go mod tidy", "go_mod_tidy") {
		t.Fatal("custom auto_verify when should match")
	}
}
