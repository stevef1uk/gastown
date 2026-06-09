package orchestrator

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStateTimedOut(t *testing.T) {
	t.Parallel()
	inst := &WorkflowInstance{StateEnteredAt: time.Now().UTC().Add(-31 * time.Minute).Format(time.RFC3339)}
	state := State{Hooks: StateHooks{StateTimeoutSeconds: 1800}}
	if !stateTimedOut(inst, state, time.Now().UTC()) {
		t.Fatal("expected timeout after 31m with 1800s limit")
	}
	inst.StateEnteredAt = time.Now().UTC().Format(time.RFC3339)
	if stateTimedOut(inst, state, time.Now().UTC()) {
		t.Fatal("expected no timeout for fresh entry")
	}
}

func TestIsTimeoutOutcome(t *testing.T) {
	t.Parallel()
	if !IsTimeoutOutcome("timeout") || !IsTimeoutOutcome("TIMEOUT") {
		t.Fatal("expected timeout outcome")
	}
	if IsTimeoutOutcome("failure") || IsTimeoutOutcome("success") {
		t.Fatal("unexpected timeout classification")
	}
}

func TestTransition_sameStateTimeout_preservesStateEnteredAt(t *testing.T) {
	t.Parallel()
	tpl := &WorkflowTemplate{
		InitialState: "implementation",
		States: map[string]State{
			"implementation": {
				Transitions: map[string]Transition{
					"timeout": {To: "implementation"},
					"failure": {To: "implementation"},
				},
			},
		},
	}
	inst := &WorkflowInstance{CurrentState: "implementation", StateEnteredAt: "2000-01-01T00:00:00Z"}
	before := inst.StateEnteredAt
	for _, outcome := range []string{"timeout", "failure"} {
		if _, err := inst.Transition(tpl, outcome); err != nil {
			t.Fatal(err)
		}
		if inst.StateEnteredAt != before {
			t.Fatalf("StateEnteredAt should not refresh on same-state %q rework", outcome)
		}
	}
}

func TestTransition_crossState_refreshesStateEnteredAt(t *testing.T) {
	t.Parallel()
	tpl := &WorkflowTemplate{
		InitialState: "project_setup",
		States: map[string]State{
			"project_setup": {
				Transitions: map[string]Transition{
					"success": {To: "implementation"},
				},
			},
			"implementation": {},
		},
	}
	inst := &WorkflowInstance{CurrentState: "project_setup", StateEnteredAt: "2000-01-01T00:00:00Z"}
	before := inst.StateEnteredAt
	if _, err := inst.Transition(tpl, "success"); err != nil {
		t.Fatal(err)
	}
	if inst.StateEnteredAt == before {
		t.Fatal("StateEnteredAt should refresh on cross-state transition")
	}
}

func TestAcceptsOutcome_timeout(t *testing.T) {
	t.Parallel()
	st := State{Transitions: map[string]Transition{"timeout": {To: "planning"}}}
	if !st.AcceptsOutcome("timeout") {
		t.Fatal("expected timeout outcome accepted")
	}
}

func planningTimeoutTestTemplate() *WorkflowTemplate {
	return &WorkflowTemplate{
		ID:           "rig-flow",
		InitialState: "planning",
		States: map[string]State{
			"planning": {
				Role:         "planner",
				Instructions: "write plan",
				Hooks: StateHooks{
					StateTimeoutSeconds: 60,
					// No on_timeout hooks — avoids bd in manager unit tests.
				},
				Transitions: map[string]Transition{
					"success": {To: "plan_review"},
					"failure": {To: "planning"},
					"timeout": {To: "planning"},
				},
			},
			"plan_review": {Role: "qa", Instructions: "review"},
		},
	}
}

func TestManager_applyStateTimeout_transitionsAndPendingRework(t *testing.T) {
	m, wfID := loadTestManager(t, planningTimeoutTestTemplate())
	inst := m.instances[wfID]
	inst.StateEnteredAt = time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)

	next, err := m.applyStateTimeout(wfID)
	if err != nil {
		t.Fatal(err)
	}
	if next != "planning" {
		t.Fatalf("next state = %q, want planning", next)
	}
	if inst.CurrentState != "planning" {
		t.Fatalf("current_state = %q", inst.CurrentState)
	}
	if inst.PendingRework == nil {
		t.Fatal("expected PendingRework after timeout")
	}
	if inst.PendingRework.Outcome != "timeout" {
		t.Fatalf("rework outcome = %q", inst.PendingRework.Outcome)
	}
	if inst.PendingRework.FromState != "planning" {
		t.Fatalf("rework from_state = %q", inst.PendingRework.FromState)
	}
	if !strings.Contains(inst.PendingRework.Summary, "timed out") {
		t.Fatalf("summary = %q", inst.PendingRework.Summary)
	}
	entered, ok := parseStateEnteredAt(inst.StateEnteredAt)
	if !ok || time.Since(entered) > 5*time.Second {
		t.Fatalf("StateEnteredAt should be recent after timeout transition, got %q", inst.StateEnteredAt)
	}
}

func TestManager_FetchTask_triggersWallClockTimeout(t *testing.T) {
	m, wfID := loadTestManager(t, planningTimeoutTestTemplate())
	m.instances[wfID].StateEnteredAt = time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)

	payload, err := m.FetchTask("mockrig/planner")
	if err != nil {
		t.Fatal(err)
	}
	task := payloadToTask(t, payload)
	if task.State != "planning" {
		t.Fatalf("state = %q, want planning", task.State)
	}
	inst := m.instances[wfID]
	if inst.PendingRework == nil || inst.PendingRework.Outcome != "timeout" {
		t.Fatalf("expected timeout rework after fetch, got %+v", inst.PendingRework)
	}
	if task.PendingRework == nil {
		t.Fatal("fetch_task payload should include pending_rework after timeout")
	}
}

func TestManager_CompleteTask_timeout_sameStateSetsPendingRework(t *testing.T) {
	m, wfID := loadTestManager(t, planningTimeoutTestTemplate())
	inst := m.instances[wfID]
	inst.StateEnteredAt = time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	before := inst.StateEnteredAt
	next, err := m.CompleteTask(wfID, "timeout", "", "planning timed out", "reset artifacts")
	if err != nil || next != "planning" {
		t.Fatalf("CompleteTask: next=%q err=%v", next, err)
	}
	if inst.PendingRework == nil {
		t.Fatal("expected PendingRework for same-state timeout")
	}
	if inst.PendingRework.Summary != "planning timed out" {
		t.Fatalf("summary = %q", inst.PendingRework.Summary)
	}
	if inst.StateEnteredAt != before {
		t.Fatalf("same-state turn timeout must not reset StateEnteredAt (got %q, want %q)", inst.StateEnteredAt, before)
	}
}

func TestManager_CompleteTask_failure_sameStateClearsNoRework(t *testing.T) {
	m, wfID := loadTestManager(t, planningTimeoutTestTemplate())
	next, err := m.CompleteTask(wfID, "failure", "mockrig/planner", "bad plan", "validation failed")
	if err != nil || next != "planning" {
		t.Fatalf("CompleteTask: next=%q err=%v", next, err)
	}
	if m.instances[wfID].PendingRework != nil {
		t.Fatalf("same-state failure should not set PendingRework: %+v", m.instances[wfID].PendingRework)
	}
}

func TestRunOnTimeoutHook_unknownStep(t *testing.T) {
	t.Parallel()
	if _, err := RunOnTimeoutHook("nope", t.TempDir(), "mockrig", DefaultWorkflowValidation()); err == nil {
		t.Fatal("expected error for unknown hook")
	}
}

func TestResetPlanningPhase_integration(t *testing.T) {
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd not in PATH")
	}
	townRoot := t.TempDir()
	rig := "mockrig"
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	beadsDir := filepath.Join(townRoot, rig, ".beads")
	for _, d := range []string{rigDir, beadsDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	init := exec.Command("bd", "init")
	init.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
	init.Dir = rigDir
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("bd init: %v\n%s", err, out)
	}
	v := WorkflowValidation{
		BeadTitleContains: "Implement ",
		RequiredFiles:     []string{"Dockerfile", "frontend/package.json"},
		MinPlanBytes:      100,
	}
	create := exec.Command("bd", "create", "--type", "task", "--title", "ImplementDockerfile per architecture")
	create.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
	create.Dir = rigDir
	if out, err := create.CombinedOutput(); err != nil {
		t.Fatalf("bd create: %v\n%s", err, out)
	}
	planPath := filepath.Join(rigDir, "plan.md")
	if err := os.WriteFile(planPath, []byte("### fi-001: fake\n"), 0644); err != nil {
		t.Fatal(err)
	}
	writeMinimalPlanningRigDocs(t, rigDir)

	logLine, err := ResetPlanningPhase(townRoot, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logLine, "deleted") && !strings.Contains(logLine, "recreated") {
		t.Fatalf("expected cleanup log, got %q", logLine)
	}
	if !strings.Contains(logLine, "wrote plan.md") {
		t.Fatalf("expected plan.md rewrite in log, got %q", logLine)
	}
	planData, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatalf("plan.md should exist after reset: %v", err)
	}
	if strings.Contains(string(planData), "fi-001") {
		t.Fatal("stale placeholder fi-001 should be gone from plan.md")
	}
	if int64(len(planData)) < EffectiveMinPlanBytes(rigDir, v) {
		t.Fatalf("plan.md too small: %d bytes", len(planData))
	}
	open, err := listAllOpenBeads(townRoot, rig)
	if err != nil {
		t.Fatal(err)
	}
	var impl []PlanBead
	for _, b := range open {
		if MatchesImplementBeadTitle(b.Title, v) {
			impl = append(impl, b)
		}
	}
	if len(impl) != len(v.RequiredFiles) {
		t.Fatalf("implement beads = %d, want %d: %v", len(impl), len(v.RequiredFiles), impl)
	}
	for _, b := range impl {
		if !strings.HasPrefix(strings.TrimSpace(b.Title), "Implement ") {
			t.Fatalf("non-canonical title: %q", b.Title)
		}
		if strings.Contains(b.Title, "ImplementDockerfile") || strings.Contains(b.Title, "Implementfrontend") {
			t.Fatalf("glued title should be gone: %q", b.Title)
		}
	}
}
