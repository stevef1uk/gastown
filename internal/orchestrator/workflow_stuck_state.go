package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const workflowStuckStateFilename = "workflow-stuck-state.json"

type workflowStuckStateSnap struct {
	Rigs map[string]*workflowStuckRigState `json:"rigs"`
}

type workflowStuckRigState struct {
	LastBeadFingerprint string    `json:"last_bead_fingerprint,omitempty"`
	LastRepairAt        time.Time `json:"last_repair_at,omitempty"`
	LastSignals         []string  `json:"last_signals,omitempty"`
}

func workflowStuckStatePath(townRoot string) string {
	return filepath.Join(townRoot, "orchestrator", workflowStuckStateFilename)
}

func loadWorkflowStuckState(townRoot string) *workflowStuckStateSnap {
	path := workflowStuckStatePath(townRoot)
	data, err := os.ReadFile(path)
	if err != nil {
		return &workflowStuckStateSnap{Rigs: map[string]*workflowStuckRigState{}}
	}
	var snap workflowStuckStateSnap
	if err := json.Unmarshal(data, &snap); err != nil || snap.Rigs == nil {
		return &workflowStuckStateSnap{Rigs: map[string]*workflowStuckRigState{}}
	}
	return &snap
}

func saveWorkflowStuckState(townRoot string, snap *workflowStuckStateSnap) error {
	if snap == nil {
		snap = &workflowStuckStateSnap{Rigs: map[string]*workflowStuckRigState{}}
	}
	if snap.Rigs == nil {
		snap.Rigs = map[string]*workflowStuckRigState{}
	}
	path := workflowStuckStatePath(townRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (snap *workflowStuckStateSnap) rigState(rig string) *workflowStuckRigState {
	if snap.Rigs == nil {
		snap.Rigs = map[string]*workflowStuckRigState{}
	}
	st, ok := snap.Rigs[rig]
	if !ok || st == nil {
		st = &workflowStuckRigState{}
		snap.Rigs[rig] = st
	}
	return st
}

func repairCooldownElapsed(st *workflowStuckRigState, cooldown time.Duration, now time.Time) bool {
	if st == nil || st.LastRepairAt.IsZero() {
		return true
	}
	return now.Sub(st.LastRepairAt) >= cooldown
}
