package orchestrator

import "testing"

func TestTransition_failMapsToFailureNotSuccess(t *testing.T) {
	tpl := &WorkflowTemplate{
		States: map[string]State{
			"design": {
				Transitions: map[string]Transition{
					"success": {To: "planning"},
					"failure": {To: "design"},
				},
			},
			"planning": {
				Transitions: map[string]Transition{
					"success": {To: "implementation"},
					"failure": {To: "planning"},
				},
			},
		},
	}
	inst := &WorkflowInstance{CurrentState: "design"}

	next, err := inst.Transition(tpl, "fail")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next != "design" {
		t.Fatalf("fail on design should stay in design via failure, got %q", next)
	}

	inst.CurrentState = "planning"
	next, err = inst.Transition(tpl, "fail")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if next != "planning" {
		t.Fatalf("fail on planning should stay in planning, got %q", next)
	}
}

func TestIsFailureOutcome_contractMismatch(t *testing.T) {
	if !IsFailureOutcome("contract_mismatch") {
		t.Fatal("contract_mismatch must count as a failure outcome for review budgeting")
	}
	if !IsContractMismatchOutcome("contract_mismatch") {
		t.Fatal("contract_mismatch must be distinguishable from generic failure")
	}
	if IsContractMismatchOutcome("failure") {
		t.Fatal("generic failure must not be treated as a contract mismatch")
	}
}

func TestTransition_unknownOutcomeDoesNotAdvanceToSuccess(t *testing.T) {
	tpl := &WorkflowTemplate{
		States: map[string]State{
			"design": {
				Transitions: map[string]Transition{
					"success": {To: "planning"},
				},
			},
		},
	}
	inst := &WorkflowInstance{CurrentState: "design"}

	_, err := inst.Transition(tpl, "bogus")
	if err == nil {
		t.Fatal("expected error for unknown outcome")
	}
	if inst.CurrentState != "design" {
		t.Fatalf("state should be unchanged, got %q", inst.CurrentState)
	}
}
