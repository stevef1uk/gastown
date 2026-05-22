package orchestrator

import "testing"

// setListImplementBeadsByStatusHook installs a per-test bd list stub; safe under t.Parallel().
func setListImplementBeadsByStatusHook(t *testing.T, hook func(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error)) {
	t.Helper()
	listImplementBeadsHookMu.Lock()
	prev := ListImplementBeadsByStatusHook
	ListImplementBeadsByStatusHook = hook
	listImplementBeadsHookMu.Unlock()
	t.Cleanup(func() {
		listImplementBeadsHookMu.Lock()
		ListImplementBeadsByStatusHook = prev
		listImplementBeadsHookMu.Unlock()
	})
}

func replaceListImplementBeadsByStatusHook(hook func(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error)) {
	listImplementBeadsHookMu.Lock()
	ListImplementBeadsByStatusHook = hook
	listImplementBeadsHookMu.Unlock()
}
