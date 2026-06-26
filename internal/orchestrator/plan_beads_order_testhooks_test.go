package orchestrator

import "testing"

func hookMapKey(townRoot, rig string) string {
	return townRoot + "\x00" + rig
}

// setListImplementBeadsByStatusHook installs a per-test bd list stub scoped to townRoot/rig.
// Uses the per-(townRoot, rig) key in implementBeadsHookMap so parallel tests don't race.
func setListImplementBeadsByStatusHook(t *testing.T, townRoot, rig string, hook func(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error)) {
	t.Helper()
	key := hookMapKey(townRoot, rig)
	prev, _ := implementBeadsHookMap.Load(key)
	implementBeadsHookMap.Store(key, hook)
	t.Cleanup(func() {
		if prev != nil {
			implementBeadsHookMap.Store(key, prev)
		} else {
			implementBeadsHookMap.Delete(key)
		}
	})
}

func replaceListImplementBeadsByStatusHook(t *testing.T, townRoot, rig string, hook func(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error)) {
	t.Helper()
	key := hookMapKey(townRoot, rig)
	implementBeadsHookMap.Store(key, hook)
	t.Cleanup(func() {
		implementBeadsHookMap.Delete(key)
	})
}
