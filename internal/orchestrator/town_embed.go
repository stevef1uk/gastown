package orchestrator

import (
	_ "embed"
	"fmt"

	"gopkg.in/yaml.v3"
)

//go:embed town/templates/rig-flow.yaml
var rigFlowTemplateYAML []byte

// LoadRigFlowTemplate parses the bundled rig-flow workflow template from gastown source.
func LoadRigFlowTemplate() (*WorkflowTemplate, error) {
	var tpl WorkflowTemplate
	if err := yaml.Unmarshal(rigFlowTemplateYAML, &tpl); err != nil {
		return nil, fmt.Errorf("parse rig-flow.yaml: %w", err)
	}
	if tpl.ID == "" {
		return nil, fmt.Errorf("rig-flow.yaml: missing id")
	}
	return &tpl, nil
}

// RigFlowStateHooks returns the hooks block for a rig-flow state (for tests and tooling).
func RigFlowStateHooks(state string) (StateHooks, error) {
	tpl, err := LoadRigFlowTemplate()
	if err != nil {
		return StateHooks{}, err
	}
	st, ok := tpl.States[state]
	if !ok {
		return StateHooks{}, fmt.Errorf("rig-flow: unknown state %q", state)
	}
	return st.Hooks, nil
}
