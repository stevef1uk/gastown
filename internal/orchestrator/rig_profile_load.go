package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const rigProfileDir = ".gastown"
const rigProfileFile = "workflow-profile.json"

type rigProfileEnvelope struct {
	Version     int                 `json:"version"`
	GeneratedAt string              `json:"generated_at"`
	Source      string              `json:"source"`
	Confidence  string              `json:"confidence"`
	Validation  WorkflowValidation  `json:"validation"`
}

// LoadRigWorkflowProfileFile reads {rig}/mayor/rig/.gastown/workflow-profile.json if present.
func LoadRigWorkflowProfileFile(townRoot, rig string) (WorkflowValidation, bool, error) {
	if rig == "" || townRoot == "" {
		return WorkflowValidation{}, false, nil
	}
	path := filepath.Join(townRoot, rig, "mayor", "rig", rigProfileDir, rigProfileFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return WorkflowValidation{}, false, nil
		}
		return WorkflowValidation{}, false, fmt.Errorf("read rig profile %s: %w", path, err)
	}
	var env rigProfileEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return WorkflowValidation{}, false, fmt.Errorf("decode rig profile %s: %w", path, err)
	}
	return env.Validation, true, nil
}
