package main

import (
	"github.com/steveyegge/gastown/internal/orchestrator"
)

func taskValidation(task *orchestrator.Task) orchestrator.WorkflowValidation {
	if task == nil {
		return orchestrator.DefaultWorkflowValidation()
	}
	return task.Validation.WithDefaults()
}
