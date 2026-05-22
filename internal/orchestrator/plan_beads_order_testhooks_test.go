package orchestrator

import "testing"

func wrapScopedImplementBeadsHook(townRoot, rig string, hook func(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error)) func(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error) {
	wantTown := townRoot
	wantRig := rig
	return func(tr, r string, v WorkflowValidation, status string) ([]PlanBead, error) {
		if tr != wantTown || r != wantRig {
			return nil, errImplementBeadsUseRealList
		}
		if hook == nil {
			return nil, errImplementBeadsUseRealList
		}
		return hook(tr, r, v, status)
	}
}

// setListImplementBeadsByStatusHook installs a per-test bd list stub scoped to townRoot/rig.
func setListImplementBeadsByStatusHook(t *testing.T, townRoot, rig string, hook func(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error)) {
	t.Helper()
	listImplementBeadsHookMu.Lock()
	prev := ListImplementBeadsByStatusHook
	ListImplementBeadsByStatusHook = wrapScopedImplementBeadsHook(townRoot, rig, hook)
	listImplementBeadsHookMu.Unlock()
	t.Cleanup(func() {
		listImplementBeadsHookMu.Lock()
		ListImplementBeadsByStatusHook = prev
		listImplementBeadsHookMu.Unlock()
	})
}

func replaceListImplementBeadsByStatusHook(t *testing.T, townRoot, rig string, hook func(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error)) {
	t.Helper()
	listImplementBeadsHookMu.Lock()
	ListImplementBeadsByStatusHook = wrapScopedImplementBeadsHook(townRoot, rig, hook)
	listImplementBeadsHookMu.Unlock()
}
