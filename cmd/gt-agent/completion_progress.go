package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

type completionRejectionProgress struct {
	fingerprint string
	count       int
}

// Bound rejected completion claims independently of tool turns. Re-running a
// successful smoke command or listing closed beads is not artifact progress.
// The budget is configured by the workflow, and content changes (including
// shell-based edits) give the agent a fresh repair budget.
func (r *stateRunner) completionRejectionLimitReached() bool {
	if r.hooks.NoProgressLimit <= 0 {
		return false
	}
	hash := sha256.New()
	profile, err := json.Marshal(r.v)
	if err != nil {
		return false
	}
	hash.Write(profile)
	files := append([]string{"SPEC.md", "architecture.md", "plan.md", ".gastown/workflow-profile.json"}, r.v.PhasedActiveAndPastRequiredFiles()...)
	files = append(files, r.hooks.ProgressFiles...)
	rigDir := rigMayorRigDir(r.townRoot, r.rig)
	for _, file := range files {
		path := orchestrator.ResolveRequiredFileOnDisk(rigDir, file, r.v.LayoutRoot)
		data, err := os.ReadFile(filepath.Clean(path))
		if err != nil && !os.IsNotExist(err) {
			return false // An I/O outage is not evidence of unchanged artifacts.
		}
		fmt.Fprintf(hash, "\x00%s\x00%t\x00", file, os.IsNotExist(err))
		hash.Write(data)
	}
	fingerprint := fmt.Sprintf("%x", hash.Sum(nil))
	if r.completionRejections.fingerprint != fingerprint {
		r.completionRejections = completionRejectionProgress{fingerprint: fingerprint}
	}
	r.completionRejections.count++
	return r.completionRejections.count >= r.hooks.NoProgressLimit
}
