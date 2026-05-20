package orchestrator

import (
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

func TestTransition_resetsStateEnteredAt(t *testing.T) {
	t.Parallel()
	tpl := &WorkflowTemplate{
		InitialState: "planning",
		States: map[string]State{
			"planning": {
				Transitions: map[string]Transition{
					"timeout": {To: "planning"},
				},
			},
		},
	}
	inst := &WorkflowInstance{CurrentState: "planning", StateEnteredAt: "2000-01-01T00:00:00Z"}
	before := inst.StateEnteredAt
	if _, err := inst.Transition(tpl, "timeout"); err != nil {
		t.Fatal(err)
	}
	if inst.StateEnteredAt == before {
		t.Fatal("StateEnteredAt should refresh on transition")
	}
}

func TestAcceptsOutcome_timeout(t *testing.T) {
	t.Parallel()
	st := State{Transitions: map[string]Transition{"timeout": {To: "planning"}}}
	if !st.AcceptsOutcome("timeout") {
		t.Fatal("expected timeout outcome accepted")
	}
}
