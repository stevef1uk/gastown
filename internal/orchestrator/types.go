package orchestrator

import (
	"fmt"
	"strings"
)

// WorkflowTemplate defines a deterministic FSM for a Gas Town workflow.
type WorkflowTemplate struct {
	ID           string             `yaml:"id"`
	Description  string             `yaml:"description"`
	InitialState string             `yaml:"initial_state"`
	Validation   WorkflowValidation `yaml:"validation"`
	States       map[string]State   `yaml:"states"`
}

// State represents a node in the workflow FSM.
type State struct {
	Role         string                `yaml:"role"`
	PromptFile   string                `yaml:"prompt_file"` // relative to {townRoot}/orchestrator/
	Instructions string                `yaml:"instructions"`
	Hooks        StateHooks            `yaml:"hooks"`
	Transitions  map[string]Transition `yaml:"transitions"`
}

// Transition defines a state change based on task outcome.
type Transition struct {
	To string `yaml:"to"`
}

// WorkflowRework captures why a step failed so the next FSM state/agent can fix it.
type WorkflowRework struct {
	FromState string `json:"from_state"`
	Outcome   string `json:"outcome"`
	Summary   string `json:"summary"`
	Feedback  string `json:"feedback,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
}

// WorkflowInstance is an active execution of a WorkflowTemplate.
type WorkflowInstance struct {
	ID            string            `json:"id"`
	TemplateID    string            `json:"template_id"`
	CurrentState  string            `json:"current_state"`
	Variables     map[string]string `json:"variables"`
	Status        string            `json:"status"` // "running", "paused", "completed", "failed"
	PendingRework *WorkflowRework   `json:"pending_rework,omitempty"`
}

// IsFailureOutcome reports whether complete_task outcome means rework/failure.
func IsFailureOutcome(outcome string) bool {
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case "fail", "failure":
		return true
	default:
		return false
	}
}

// AllowedOutcomes returns transition keys for the state (valid complete_task outcomes).
func (s State) AllowedOutcomes() []string {
	if len(s.Transitions) == 0 {
		return nil
	}
	out := make([]string, 0, len(s.Transitions))
	for k := range s.Transitions {
		out = append(out, k)
	}
	return out
}

// AcceptsOutcome reports whether the outcome is a valid transition key for this state.
func (s State) AcceptsOutcome(outcome string) bool {
	if _, ok := s.Transitions[outcome]; ok {
		return true
	}
	if outcome == "fail" {
		_, hasFailure := s.Transitions["failure"]
		_, hasFail := s.Transitions["fail"]
		return hasFailure || hasFail
	}
	return false
}

// GetCurrentTask returns the role and instructions for the current state.
func (wi *WorkflowInstance) GetCurrentTask(tpl *WorkflowTemplate) (State, error) {
	if tpl == nil {
		return State{}, nil
	}
	state, ok := tpl.States[wi.CurrentState]
	if !ok {
		return State{}, nil
	}
	return state, nil
}

// Transition moves the instance to a new state based on outcome.
func (wi *WorkflowInstance) Transition(tpl *WorkflowTemplate, outcome string) (string, error) {
	state, ok := tpl.States[wi.CurrentState]
	if !ok {
		return "", nil
	}

	trans, ok := state.Transitions[outcome]
	if !ok && outcome == "fail" {
		if t, ok2 := state.Transitions["failure"]; ok2 {
			trans = t
			ok = true
		}
	}
	if !ok {
		if t, ok2 := state.Transitions["default"]; ok2 {
			trans = t
			ok = true
		} else {
			return wi.CurrentState, fmt.Errorf("outcome %q has no transition from state %q (allowed: %s)",
				outcome, wi.CurrentState, strings.Join(state.AllowedOutcomes(), ", "))
		}
	}

	wi.CurrentState = trans.To
	if wi.CurrentState == "completed" || wi.CurrentState == "failed" {
		wi.Status = wi.CurrentState
	} else if wi.Status == "" {
		wi.Status = "running"
	}
	return wi.CurrentState, nil
}
