package orchestrator

// MaybeAutoStartWorkflow starts the configured default workflow when auto_start is enabled
// and no active instance exists for the same template and rig.
func MaybeAutoStartWorkflow(townRoot, rig string) (string, error) {
	cfg := LoadTownOrchestratorSettings(townRoot)
	if !cfg.AutoStart || cfg.DefaultWorkflow == "" {
		return "", nil
	}
	running, _, err := IsRunning(townRoot)
	if err != nil {
		return "", err
	}
	if !running {
		return "", nil
	}

	statuses, err := GetWorkflowStatuses(townRoot, "")
	if err != nil {
		return "", err
	}
	for _, s := range statuses {
		if s.Status == "completed" || s.Status == "failed" {
			continue
		}
		if s.TemplateID != cfg.DefaultWorkflow {
			continue
		}
		if rig != "" && s.Variables["rig"] != rig {
			continue
		}
		return "", nil
	}

	vars := map[string]string{}
	if rig != "" {
		vars["rig"] = rig
	}
	return StartWorkflow(townRoot, cfg.DefaultWorkflow, vars)
}
