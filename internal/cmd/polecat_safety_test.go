package cmd

import (
	"testing"

	"github.com/steveyegge/gastown/internal/polecat"
)

// TestSafetyResultHasHardBlock_NilResult asserts the helper is nil-safe.
func TestSafetyResultHasHardBlock_NilResult(t *testing.T) {
	if safetyResultHasHardBlock(nil) {
		t.Fatalf("expected nil result to report no hard block")
	}
}

// TestSafetyResultHasHardBlock_ActiveHookBead is the critical case that
// motivated the new --abandon-work flag: the witness's old `gt polecat
// nuke <target> --force` invocation would destroy a polecat that had an
// active, non-closed bead on its hook. With the tightened semantics,
// --force should no longer be sufficient to override this.
func TestSafetyResultHasHardBlock_ActiveHookBead(t *testing.T) {
	r := &SafetyCheckResult{HookBead: "hq-211.1", HookStale: false}
	if !safetyResultHasHardBlock(r) {
		t.Fatalf("active hook bead should be a hard block")
	}
}

// TestSafetyResultHasHardBlock_StaleHookBead verifies that a closed
// (stale) hook bead does NOT trigger the hard block — the polecat
// completed its work and is safe to nuke with --force.
func TestSafetyResultHasHardBlock_StaleHookBead(t *testing.T) {
	r := &SafetyCheckResult{HookBead: "hq-211.1", HookStale: true}
	if safetyResultHasHardBlock(r) {
		t.Fatalf("stale (closed) hook bead should NOT be a hard block")
	}
}

// TestSafetyResultHasHardBlock_OpenMR verifies an open MR bead blocks
// --force. Nuking would delete the remote branch and orphan the
// refinery's pending merge.
func TestSafetyResultHasHardBlock_OpenMR(t *testing.T) {
	r := &SafetyCheckResult{OpenMR: "hq-wisp-mr-abc"}
	if !safetyResultHasHardBlock(r) {
		t.Fatalf("open MR should be a hard block")
	}
}

// TestSafetyResultHasHardBlock_UncommittedChanges verifies real work
// in the worktree triggers a hard block.
func TestSafetyResultHasHardBlock_UncommittedChanges(t *testing.T) {
	r := &SafetyCheckResult{GitState: &GitState{UncommittedFiles: []string{"foo.go"}}}
	if !safetyResultHasHardBlock(r) {
		t.Fatalf("uncommitted files should be a hard block")
	}
}

// TestSafetyResultHasHardBlock_UnpushedCommits verifies committed work
// that hasn't been pushed yet triggers a hard block.
func TestSafetyResultHasHardBlock_UnpushedCommits(t *testing.T) {
	r := &SafetyCheckResult{GitState: &GitState{UnpushedCommits: 2}}
	if !safetyResultHasHardBlock(r) {
		t.Fatalf("unpushed commits should be a hard block")
	}
}

// TestSafetyResultHasHardBlock_CleanupStatusFromBead verifies that the
// agent-bead cleanup status path also surfaces hard blocks. (When the
// agent bead is readable we use cleanup_status; when it's not we fall
// back to direct git state.)
func TestSafetyResultHasHardBlock_CleanupStatusFromBead(t *testing.T) {
	for _, status := range []polecat.CleanupStatus{
		polecat.CleanupUnpushed,
		polecat.CleanupUncommitted,
	} {
		r := &SafetyCheckResult{CleanupStatus: status}
		if !safetyResultHasHardBlock(r) {
			t.Fatalf("cleanup status %q should be a hard block", status)
		}
	}
}

// TestSafetyResultHasHardBlock_SoftBlocks verifies that conditions which
// SHOULD be bypassable with --force are NOT classified as hard blocks.
// In particular, an unknown cleanup status (transient state during
// polecat startup/shutdown) shouldn't strand the operator.
func TestSafetyResultHasHardBlock_SoftBlocks(t *testing.T) {
	cases := []struct {
		name string
		r    *SafetyCheckResult
	}{
		{
			name: "unknown cleanup status",
			r:    &SafetyCheckResult{CleanupStatus: polecat.CleanupUnknown},
		},
		{
			name: "clean polecat with no active bead",
			r:    &SafetyCheckResult{CleanupStatus: polecat.CleanupClean},
		},
		{
			name: "stash exists but nothing else",
			r: &SafetyCheckResult{
				GitState: &GitState{StashCount: 2, Clean: false},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if safetyResultHasHardBlock(tc.r) {
				t.Fatalf("expected %s to be a soft block (bypassable with --force)", tc.name)
			}
		})
	}
}
