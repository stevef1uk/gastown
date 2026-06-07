package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

const (
	implMilestoneVerifyPrefix = "verify:"
	implMilestoneClosedPrefix = "closed:"
)

// ImplementationProgress records per-bead verify/close checkpoints across gt-agent restarts.
type ImplementationProgress struct {
	WorkflowID           string          `json:"workflow_id"`
	State                string          `json:"state"`
	Rig                  string          `json:"rig"`
	UpdatedAt            time.Time       `json:"updated_at"`
	ActiveBead           string          `json:"active_bead,omitempty"`
	ActiveBeadPath       string          `json:"active_bead_path,omitempty"`
	Completed            map[string]bool   `json:"completed"`
	LastVerifyFailBead   string          `json:"last_verify_fail_bead,omitempty"`
	LastVerifyFailPath   string          `json:"last_verify_fail_path,omitempty"`
	LastVerifyFailPaths  []string        `json:"last_verify_fail_paths,omitempty"`
	LastVerifyFailOutput string          `json:"last_verify_fail_output,omitempty"`
}

func implementationProgressPath(townRoot, rig string) string {
	if townRoot == "" || rig == "" {
		return ""
	}
	return filepath.Join(townRoot, rig, "qa", "implementation-progress.json")
}

func newImplementationProgress(workflowID, state, rig string) *ImplementationProgress {
	return &ImplementationProgress{
		WorkflowID: workflowID,
		State:      state,
		Rig:        rig,
		UpdatedAt:  time.Now().UTC(),
		Completed:  map[string]bool{},
	}
}

func loadImplementationProgress(townRoot, rig, workflowID, state string) *ImplementationProgress {
	path := implementationProgressPath(townRoot, rig)
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var p ImplementationProgress
	if err := json.Unmarshal(data, &p); err != nil {
		return nil
	}
	if p.WorkflowID != workflowID || p.State != state || p.Rig != rig {
		return nil
	}
	if p.Completed == nil {
		p.Completed = map[string]bool{}
	}
	return &p
}

func saveImplementationProgress(townRoot, rig string, p *ImplementationProgress) error {
	if p == nil {
		return nil
	}
	path := implementationProgressPath(townRoot, rig)
	if path == "" {
		return nil
	}
	p.UpdatedAt = time.Now().UTC()
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func clearImplementationProgress(townRoot, rig string) {
	path := implementationProgressPath(townRoot, rig)
	if path == "" {
		return
	}
	_ = os.Remove(path)
}

func (p *ImplementationProgress) mark(key string) bool {
	if p == nil || key == "" {
		return false
	}
	if p.Completed == nil {
		p.Completed = map[string]bool{}
	}
	if p.Completed[key] {
		return false
	}
	p.Completed[key] = true
	return true
}

func (p *ImplementationProgress) done(key string) bool {
	return p != nil && p.Completed != nil && p.Completed[key]
}

func implVerifyKey(beadID string) string {
	return implMilestoneVerifyPrefix + strings.TrimSpace(beadID)
}

func implClosedKey(beadID string) string {
	return implMilestoneClosedPrefix + strings.TrimSpace(beadID)
}

func (r *stateRunner) initImplementationProgress() string {
	if r.task == nil || r.task.State != "implementation" || !strings.EqualFold(strings.TrimSpace(r.hooks.Track), "implementation") {
		return ""
	}
	existing := loadImplementationProgress(r.townRoot, r.rig, r.task.WorkflowID, r.task.State)
	if existing != nil {
		r.implProgress = existing
	} else {
		r.implProgress = newImplementationProgress(r.task.WorkflowID, r.task.State, r.rig)
	}
	r.scrubStaleImplementationProgressMilestones()
	r.applyImplementationProgressToTrack()
	r.clearStaleImplementationVerifyFailureOnResume()
	r.clearStaleImplementationVerifyMilestoneOnResume()
	block := r.formatImplementationProgressBlock()
	if nudge := r.formatActiveBeadReadyToCloseBlock(); nudge != "" {
		if block != "" {
			block += "\n\n" + nudge
		} else {
			block = nudge
		}
	}
	return block
}

// formatActiveBeadReadyToCloseBlock nudges the polecat when the active bead file already exists
// and package tests pass but bd close was never run (common after timeout/retry).
func (r *stateRunner) formatActiveBeadReadyToCloseBlock() string {
	if r.track == nil || r.implProgress == nil {
		return ""
	}
	beadID := strings.TrimSpace(r.track.activeBead)
	beadPath := strings.TrimSpace(r.activeImplementBeadPath())
	if beadID == "" || beadPath == "" {
		return ""
	}
	if r.implProgress.done(implClosedKey(beadID)) {
		return ""
	}
	if !r.implementBeadCloseArtifactsReady() {
		return ""
	}
	rigDir := rigMayorRigDir(r.townRoot, r.rig)
	// Trust persisted verify milestone when the artifact is still on disk — avoids
	// re-running a slow first-time CGO sqlite build on every turn just to nudge bd close.
	if !r.implProgress.done(implVerifyKey(beadID)) && orchestrator.WorkflowUsesGo(r.v) && strings.HasSuffix(beadPath, ".go") {
		verifyCmd := orchestrator.GoCompileVerifyCommandForBead(r.v, rigDir, beadPath)
		if verifyCmd == "" {
			return ""
		}
		if fixed, ok := rewriteUnittestToWorkdir(verifyCmd, r.rig, r.v); ok {
			verifyCmd = fixed
		}
		out, err := r.runShellCommand(verifyCmd, r.workDir(), "", r.commandEnv(os.Environ()))
		if err != nil || goToolOutputLooksFailed(verifyCmd, string(out)) {
			return ""
		}
	}
	var b strings.Builder
	b.WriteString("## Active bead looks complete on disk\n")
	b.WriteString(fmt.Sprintf("**%s** (`%s`) already satisfies the profile on disk", beadID, beadPath))
	if orchestrator.WorkflowUsesGo(r.v) {
		b.WriteString(" and Verify is green")
	}
	b.WriteString(fmt.Sprintf(".\n\nDo **not** re-EDIT this file for formatting. Run:\n`CMD: export BEADS_DIR=$GT_ROOT/%s/.beads && cd %s/mayor/rig && bd close %s`\n",
		r.rig, r.rig, beadID))
	return b.String()
}

// implementBeadCloseArtifactsReady reports whether the active bead's production file and any
// required correlated *_test.go (same-bead, not a separate implement bead) exist on disk.
func (r *stateRunner) implementBeadCloseArtifactsReady() bool {
	if r == nil || r.track == nil {
		return false
	}
	beadPath := strings.TrimSpace(r.activeImplementBeadPath())
	if beadPath == "" {
		return false
	}
	rigDir := rigMayorRigDir(r.townRoot, r.rig)
	if err := orchestrator.ValidateBeadArtifactOnDisk(rigDir, beadPath, r.v); err != nil {
		return false
	}
	if testPath := orchestrator.CorrelatedTestPathForSource(beadPath, r.v); testPath != "" {
		if !orchestrator.TestPathListedInRequired(beadPath, r.v) {
			if err := orchestrator.ValidateBeadArtifactOnDisk(rigDir, testPath, r.v); err != nil {
				return false
			}
		}
	}
	return true
}

// rejectRedundantImplementEditAfterVerify blocks cosmetic EDIT/WRITE churn once verify is green
// in this session. Persisted verify milestones alone must not block fixes (bd close still requires
// a green Verify run this session).
func (r *stateRunner) rejectRedundantImplementEditAfterVerify(relPath string) error {
	if r == nil || r.track == nil {
		return nil
	}
	beadID := strings.TrimSpace(r.track.activeBead)
	if beadID == "" {
		return nil
	}
	if r.implProgress != nil && r.implProgress.done(implClosedKey(beadID)) {
		return nil
	}
	if !r.track.verifyOK {
		return nil
	}
	if !r.implementBeadCloseArtifactsReady() {
		return nil
	}
	layout := strings.Trim(strings.TrimSpace(r.v.LayoutRoot), "/")
	rel := orchestrator.NormalizeBeadPathForLayout(relPath, layout)
	active := strings.TrimSpace(r.activeImplementBeadPath())
	if active == "" {
		return nil
	}
	corrTest := orchestrator.CorrelatedTestPathForSource(active, r.v)
	if rel != active && (corrTest == "" || rel != corrTest) {
		return nil
	}
	return fmt.Errorf("verify already green for active bead %s — run `bd close %s` instead of editing %s", beadID, beadID, rel)
}

// formatImplementBeadCloseNudge returns a per-turn reminder after green verify.
func (r *stateRunner) formatImplementBeadCloseNudge() string {
	if r == nil || r.track == nil {
		return ""
	}
	beadID := strings.TrimSpace(r.track.activeBead)
	beadPath := strings.TrimSpace(r.activeImplementBeadPath())
	if beadID == "" || beadPath == "" {
		return ""
	}
	if r.implProgress != nil && r.implProgress.done(implClosedKey(beadID)) {
		return ""
	}
	verifyGreen := r.track.verifyOK
	if !verifyGreen || !r.implementBeadCloseArtifactsReady() {
		return ""
	}
	return fmt.Sprintf("\n**Verify green** for active bead **%s** (`%s`). Stop editing — close the bead:\n`CMD: export BEADS_DIR=$GT_ROOT/%s/.beads && cd %s/mayor/rig && bd close %s`\n\n",
		beadID, beadPath, r.rig, r.rig, beadID)
}

// clearStaleImplementationVerifyFailureOnResume drops persisted verify-fail state when the
// active bead's verify command is green again (e.g. after manual fixes or a prior flaky run).
func (r *stateRunner) clearStaleImplementationVerifyFailureOnResume() {
	if r.implProgress == nil || r.track == nil || r.track.activeBead == "" {
		return
	}
	if r.implProgress.LastVerifyFailBead == "" || r.implProgress.LastVerifyFailBead != r.track.activeBead {
		return
	}
	if !orchestrator.WorkflowUsesGo(r.v) {
		return
	}
	beadPath := r.activeImplementBeadPath()
	if beadPath == "" {
		beadPath = r.implProgress.LastVerifyFailPath
	}
	if beadPath == "" {
		return
	}
	mayorDir := rigMayorRigDir(r.townRoot, r.rig)
	verifyCmd := orchestrator.GoCompileVerifyCommandForBead(r.v, mayorDir, beadPath)
	if verifyCmd == "" {
		return
	}
	if fixed, ok := rewriteUnittestToWorkdir(verifyCmd, r.rig, r.v); ok {
		verifyCmd = fixed
	}
	out, err := r.runShellCommand(verifyCmd, r.workDir(), "", r.commandEnv(os.Environ()))
	if err != nil || goToolOutputLooksFailed(verifyCmd, string(out)) {
		return
	}
	r.implProgress.LastVerifyFailBead = ""
	r.implProgress.LastVerifyFailPath = ""
	r.implProgress.LastVerifyFailPaths = nil
	r.implProgress.LastVerifyFailOutput = ""
	r.track.verifyOK = true
	r.implProgress.mark(implVerifyKey(r.track.activeBead))
	if saveErr := saveImplementationProgress(r.townRoot, r.rig, r.implProgress); saveErr != nil {
		orchestratedFprintfStderr("[gt-agent] implementation progress save: %v\n", saveErr)
	} else {
		orchestratedPrintf("[gt-agent] cleared stale verify-fail for bead %s (verify green on resume)\n", r.track.activeBead)
	}
}

// clearStaleImplementationVerifyMilestoneOnResume drops a persisted verify checkpoint when the
// active bead's verify command is red again (e.g. broken *_test.go after a prior flaky green run).
func (r *stateRunner) clearStaleImplementationVerifyMilestoneOnResume() {
	if r.implProgress == nil || r.track == nil || r.track.activeBead == "" {
		return
	}
	if !r.implProgress.done(implVerifyKey(r.track.activeBead)) {
		return
	}
	if r.track.verifyOK {
		return
	}
	out, failed := r.runActiveBeadVerifyCommand()
	if !failed {
		r.track.verifyOK = true
		if saveErr := saveImplementationProgress(r.townRoot, r.rig, r.implProgress); saveErr != nil {
			orchestratedFprintfStderr("[gt-agent] implementation progress save: %v\n", saveErr)
		}
		return
	}
	r.clearPersistedVerifyMilestone(r.track.activeBead)
	if orchestrator.WorkflowUsesGo(r.v) {
		r.noteImplementationVerifyFailure(r.activeBeadVerifyCommand(), out)
		return
	}
	if saveErr := saveImplementationProgress(r.townRoot, r.rig, r.implProgress); saveErr != nil {
		orchestratedFprintfStderr("[gt-agent] implementation progress save: %v\n", saveErr)
	} else {
		orchestratedPrintf("[gt-agent] cleared stale verify milestone for bead %s (verify red on resume)\n", r.track.activeBead)
	}
}

func (r *stateRunner) activeBeadVerifyCommand() string {
	beadPath := strings.TrimSpace(r.activeImplementBeadPath())
	if beadPath == "" {
		return ""
	}
	mayorDir := rigMayorRigDir(r.townRoot, r.rig)
	verifyCmd := orchestrator.GoCompileVerifyCommandForBead(r.v, mayorDir, beadPath)
	if verifyCmd == "" {
		verifyCmd = orchestrator.ImplementationVerifyCommandForBead(r.v, mayorDir, beadPath)
	}
	if verifyCmd == "" {
		return ""
	}
	if fixed, ok := rewriteUnittestToWorkdir(verifyCmd, r.rig, r.v); ok {
		verifyCmd = fixed
	}
	return verifyCmd
}

func (r *stateRunner) runActiveBeadVerifyCommand() (string, bool) {
	verifyCmd := r.activeBeadVerifyCommand()
	if verifyCmd == "" {
		return "", false
	}
	out, err := r.runShellCommand(verifyCmd, r.workDir(), "", r.commandEnv(os.Environ()))
	outStr := string(out)
	if err != nil || goToolOutputLooksFailed(verifyCmd, outStr) {
		return outStr, true
	}
	return outStr, false
}

func (r *stateRunner) clearPersistedVerifyMilestone(beadID string) {
	beadID = strings.TrimSpace(beadID)
	if r.implProgress == nil || beadID == "" || r.implProgress.Completed == nil {
		return
	}
	if !r.implProgress.Completed[implVerifyKey(beadID)] {
		return
	}
	delete(r.implProgress.Completed, implVerifyKey(beadID))
	if saveErr := saveImplementationProgress(r.townRoot, r.rig, r.implProgress); saveErr != nil {
		orchestratedFprintfStderr("[gt-agent] implementation progress save: %v\n", saveErr)
	}
}

func (r *stateRunner) clearPersistedVerifyOnFailedVerifyCmd(cmd string) {
	if r == nil || r.track == nil || r.track.activeBead == "" {
		return
	}
	if !isImplementationVerifyCommandAttempt(cmd, r.townRoot, r.rig, r.track.activeBead, r.track.activeBeadPath, r.v) {
		return
	}
	r.clearPersistedVerifyMilestone(r.track.activeBead)
}

// scrubStaleImplementationProgressMilestones drops closed/verify checkpoints when the bead file is gone
// (e.g. after ResetImplementationPhase removed in_progress/open artifacts).
func (r *stateRunner) scrubStaleImplementationProgressMilestones() {
	if r.implProgress == nil || r.implProgress.Completed == nil {
		return
	}
	rigDir := rigMayorRigDir(r.townRoot, r.rig)
	changed := false
	for key := range r.implProgress.Completed {
		var beadID string
		switch {
		case strings.HasPrefix(key, implMilestoneClosedPrefix):
			beadID = strings.TrimPrefix(key, implMilestoneClosedPrefix)
		case strings.HasPrefix(key, implMilestoneVerifyPrefix):
			beadID = strings.TrimPrefix(key, implMilestoneVerifyPrefix)
		default:
			continue
		}
		if beadID == "" {
			continue
		}
		path := orchestrator.ImplementBeadPathForID(r.townRoot, r.rig, beadID, r.v)
		if path == "" {
			continue
		}
		if err := orchestrator.ValidateBeadArtifactOnDisk(rigDir, path, r.v); err != nil {
			delete(r.implProgress.Completed, key)
			changed = true
			if r.implProgress.ActiveBead == beadID {
				r.implProgress.ActiveBead = ""
				r.implProgress.ActiveBeadPath = ""
			}
		}
	}
	if changed {
		_ = saveImplementationProgress(r.townRoot, r.rig, r.implProgress)
	}
}

func (r *stateRunner) applyImplementationProgressToTrack() {
	if r.implProgress == nil || r.track == nil {
		return
	}
	// Align with queue head before restoring persisted active bead (avoids stale active after restart).
	r.reconcileActiveImplementBeadWithQueue()
	if r.track.activeBead == "" && r.implProgress.ActiveBead != "" {
		next, err := orchestrator.NextOpenImplementBead(r.townRoot, r.rig, r.v)
		if err == nil && next != nil && next.ID == r.implProgress.ActiveBead {
			r.track.activeBead = r.implProgress.ActiveBead
			if r.track.activeBeadPath == "" && r.implProgress.ActiveBeadPath != "" {
				r.track.activeBeadPath = r.implProgress.ActiveBeadPath
			}
		}
	}
	if r.track.activeBeadPath == "" && r.implProgress.ActiveBeadPath != "" &&
		r.track.activeBead != "" && r.track.activeBead == r.implProgress.ActiveBead {
		r.track.activeBeadPath = r.implProgress.ActiveBeadPath
	}
	// verifyOK is set only by a green Verify in this session (post-write / auto-verify / clearStale on resume).
}

func (r *stateRunner) persistImplementationProgress(cmd string) {
	if r.implProgress == nil || r.track == nil {
		return
	}
	changed := false
	if r.track.activeBead != "" {
		if r.implProgress.ActiveBead != r.track.activeBead {
			r.implProgress.ActiveBead = r.track.activeBead
			r.implProgress.LastVerifyFailBead = ""
			r.implProgress.LastVerifyFailPath = ""
			r.implProgress.LastVerifyFailPaths = nil
			r.implProgress.LastVerifyFailOutput = ""
			changed = true
		}
	}
	if p := r.activeImplementBeadPath(); p != "" {
		if r.implProgress.ActiveBeadPath != p {
			r.implProgress.ActiveBeadPath = p
			changed = true
		}
	}
	if r.track.verifyOK && r.track.activeBead != "" {
		if r.implProgress.mark(implVerifyKey(r.track.activeBead)) {
			changed = true
		}
		r.implProgress.LastVerifyFailBead = ""
		r.implProgress.LastVerifyFailPath = ""
		r.implProgress.LastVerifyFailPaths = nil
		r.implProgress.LastVerifyFailOutput = ""
	}
	if isBeadCloseCommand(cmd) && cmd != "" {
		if id := extractBeadIDFromBdClose(cmd); id != "" {
			if r.implProgress.mark(implClosedKey(id)) {
				changed = true
			}
		}
	}
	if !changed {
		return
	}
	if err := saveImplementationProgress(r.townRoot, r.rig, r.implProgress); err != nil {
		orchestratedFprintfStderr("[gt-agent] implementation progress save: %v\n", err)
	}
}

func (r *stateRunner) noteImplementationVerifyFailure(cmd, cmdOutput string) {
	if r.implProgress == nil || r.track == nil || r.track.activeBead == "" {
		return
	}
	if !orchestrator.WorkflowUsesGo(r.v) || !goToolOutputLooksFailed(cmd, cmdOutput) {
		return
	}
	activePath := r.activeImplementBeadPath()
	paths := orchestrator.CompileErrorPathsIncludingClosedDeps(
		r.townRoot, r.rig, activePath,
		extractGoSourcePathsFromOutput(cmdOutput, r.v.LayoutRoot, r.v.RequiredFiles, rigMayorRigDir(r.townRoot, r.rig)),
		cmdOutput, r.v,
	)
	r.implProgress.LastVerifyFailBead = r.track.activeBead
	r.implProgress.LastVerifyFailPath = activePath
	r.implProgress.LastVerifyFailPaths = paths
	r.implProgress.LastVerifyFailOutput = truncateForProgress(cmdOutput, 12000)
	r.track.lastVerifyOutput = cmdOutput
	if r.implProgress.Completed != nil && r.track.activeBead != "" {
		delete(r.implProgress.Completed, implVerifyKey(r.track.activeBead))
	}
	r.track.verifyOK = false
	if err := saveImplementationProgress(r.townRoot, r.rig, r.implProgress); err != nil {
		orchestratedFprintfStderr("[gt-agent] implementation progress save: %v\n", err)
	}
}

func (r *stateRunner) formatImplementationProgressBlock() string {
	if r.implProgress == nil {
		return ""
	}
	var b strings.Builder
	hasCheckpoints := false
	for key := range r.implProgress.Completed {
		if strings.HasPrefix(key, implMilestoneVerifyPrefix) || strings.HasPrefix(key, implMilestoneClosedPrefix) {
			hasCheckpoints = true
			break
		}
	}
	hasResume := r.implProgress.LastVerifyFailBead != "" && len(r.implProgress.LastVerifyFailPaths) > 0
	if !hasCheckpoints && !hasResume {
		return ""
	}
	b.WriteString("## Implementation progress (persisted — resume without repeating green work)\n")
	b.WriteString(fmt.Sprintf("Workflow %s resumed `implementation`. Checkpoints from an earlier gt-agent run:\n\n", r.implProgress.WorkflowID))

	for key := range r.implProgress.Completed {
		if !strings.HasPrefix(key, implMilestoneVerifyPrefix) {
			continue
		}
		id := strings.TrimPrefix(key, implMilestoneVerifyPrefix)
		b.WriteString(fmt.Sprintf("- ✓ Verify passed for bead **%s** in a prior run — re-run **Verify** after any edit to that file.\n", id))
	}
	rigDir := rigMayorRigDir(r.townRoot, r.rig)
	for key := range r.implProgress.Completed {
		if !strings.HasPrefix(key, implMilestoneClosedPrefix) {
			continue
		}
		id := strings.TrimPrefix(key, implMilestoneClosedPrefix)
		path := orchestrator.ImplementBeadPathForID(r.townRoot, r.rig, id, r.v)
		if path != "" {
			if err := orchestrator.ValidateBeadArtifactOnDisk(rigDir, path, r.v); err != nil {
				continue
			}
		}
		b.WriteString(fmt.Sprintf("- ✓ Bead **%s** was closed in a prior run.\n", id))
	}

	if r.track != nil && r.track.activeBead != "" && r.track.verifyOK {
		if next, err := orchestrator.NextOpenImplementBead(r.townRoot, r.rig, r.v); err == nil && next != nil && next.ID != "" && next.ID == r.track.activeBead {
			b.WriteString(fmt.Sprintf("\nActive bead **%s** (`%s`) has green Verify this session — run `bd close %s` (do not re-EDIT for formatting).\n",
				r.track.activeBead, r.activeImplementBeadPath(), r.track.activeBead))
		} else if next != nil && next.ID != "" && next.ID != r.track.activeBead {
			b.WriteString(fmt.Sprintf("\nPersisted active bead **%s** is not the queue head — **Next bead** is **%s**. Run `CMD: bd update %s --status=in_progress` (gt-agent cleared stale in_progress lock).\n",
				r.track.activeBead, next.ID, next.ID))
		}
	}

	if hasResume && r.track != nil && r.implProgress.LastVerifyFailBead == r.track.activeBead {
		failOut := r.implProgress.LastVerifyFailOutput
		if failOut == "" && r.track != nil {
			failOut = r.track.lastVerifyOutput
		}
		if hint := orchestrator.FormatClosedDependencyCompileHints(
			r.townRoot, r.rig, r.implProgress.LastVerifyFailPath, r.implProgress.LastVerifyFailPaths, failOut, r.v,
		); hint != "" {
			b.WriteString("\n")
			b.WriteString(hint)
			b.WriteString("\n")
		}
		if hint := orchestrator.FormatSamePackageTestAPIHint(r.implProgress.LastVerifyFailPath, rigMayorRigDir(r.townRoot, r.rig), failOut, r.v); hint != "" {
			b.WriteString("\n")
			b.WriteString(hint)
			b.WriteString("\n")
		}
		if hint := orchestrator.FormatGoTestFailureHints(
			r.townRoot, r.rig, r.implProgress.LastVerifyFailPath, failOut, r.implProgress.LastVerifyFailPaths, r.v,
		); hint != "" {
			b.WriteString("\n")
			b.WriteString(hint)
			b.WriteString("\n")
		}
	}

	b.WriteString(fmt.Sprintf("\nProgress file: `%s/qa/implementation-progress.json` (cleared when leaving implementation).\n", r.implProgress.Rig))
	return b.String()
}

func truncateForProgress(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "\n... (truncated)\n"
}

func clearImplementationProgressIfLeaving(townRoot, rig, fromState, nextState string) {
	if fromState != "implementation" || nextState == "implementation" {
		return
	}
	clearImplementationProgress(townRoot, rig)
	orchestratedPrintf("[gt-agent] cleared implementation-progress for rig %s (left implementation → %s)\n", rig, nextState)
}
