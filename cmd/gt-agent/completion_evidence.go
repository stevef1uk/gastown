package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

type completionReopens struct {
	Fingerprint string `json:"fingerprint"`
	Count       int    `json:"count"`
	Version     int    `json:"version,omitempty"`
}

// Bump when completion reconciliation changes so old no-progress counts cannot
// prevent recovery from behavior fixed by a newer agent.
const completionReopenVersion = 2

// Include the phase contract and prerequisite contents, not timestamps. A
// restart or bead status change alone must not invalidate green verification.
func (r *stateRunner) implementationEvidence() (string, error) {
	v := r.v.ForActivePhase()
	contract, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write(contract)
	paths := map[string]bool{"TEST_PLAN.md": true}
	for _, path := range v.RequiredFiles {
		paths[path] = true
		tests, err := orchestrator.RequiredOwnedTests(rigMayorRigDir(r.townRoot, r.rig), path, v)
		if err != nil {
			return "", err
		}
		for _, test := range tests {
			paths[test] = true
		}
		if test := orchestrator.CorrelatedTestPathForSource(path, v); test != "" {
			paths[test] = true
		}
	}
	for _, phase := range v.DeliveryPhases {
		if phase.ID == v.ActivePhaseID() {
			for _, dependencies := range phase.FileDependencies {
				for _, path := range dependencies {
					paths[path] = true
				}
			}
		}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	for _, path := range ordered {
		abs := filepath.Join(rigMayorRigDir(r.townRoot, r.rig), filepath.FromSlash(path))
		data, err := os.ReadFile(abs)
		if os.IsNotExist(err) {
			_, _ = fmt.Fprintf(hash, "\x00%s\x00missing\x00", path)
			continue
		}
		if err != nil {
			return "", err // No evidence is safer than caching an unreadable input.
		}
		_, _ = fmt.Fprintf(hash, "\x00%s\x00%d\x00", path, len(data))
		_, _ = hash.Write(data)
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func (r *stateRunner) recordImplementationEvidence(id string) {
	if evidence, err := r.implementationEvidence(); err == nil {
		if r.implProgress.Evidence == nil {
			r.implProgress.Evidence = map[string]string{}
		}
		r.implProgress.Evidence[id] = evidence
	}
}

func (r *stateRunner) implementationEvidenceCurrent(id string) bool {
	if r.implProgress == nil || r.implProgress.Evidence[id] == "" {
		return false
	}
	evidence, err := r.implementationEvidence()
	return err == nil && evidence == r.implProgress.Evidence[id]
}

func (r *stateRunner) recordCompletionReopen(id string) {
	if !r.implementationEvidenceCurrent(id) {
		return
	}
	if r.implProgress.ReopenAttempts == nil {
		r.implProgress.ReopenAttempts = map[string]completionReopens{}
	}
	path := orchestrator.ImplementBeadPathForID(r.townRoot, r.rig, id, r.v)
	if path == "" {
		return
	}
	evidence := r.implProgress.Evidence[id]
	reopens := r.implProgress.ReopenAttempts[path]
	if reopens.Fingerprint != evidence || reopens.Version != completionReopenVersion {
		reopens = completionReopens{Fingerprint: evidence, Version: completionReopenVersion}
	}
	reopens.Count++
	r.implProgress.ReopenAttempts[path] = reopens
}

func (r *stateRunner) completionChurnError() error {
	if r.implProgress == nil || r.hooks.NoProgressLimit <= 0 {
		return nil
	}
	evidence, err := r.implementationEvidence()
	if err != nil {
		return nil
	}
	for path, reopens := range r.implProgress.ReopenAttempts {
		if reopens.Version == completionReopenVersion &&
			reopens.Fingerprint == evidence && reopens.Count >= r.hooks.NoProgressLimit {
			return fmt.Errorf("completion reconciliation loop: %s was reopened %d times with unchanged artifacts and contract; inspect the reopen reason before resuming; preserve delivered files", path, reopens.Count)
		}
	}
	return nil
}
