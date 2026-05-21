package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// QA review milestones persisted across gt-agent restarts within the same
// workflow qa_review step. Cleared when the FSM leaves qa_review.
const (
	qaMilestoneClosedBeads = "closed_beads_listed"
	qaMilestoneOpenBeads   = "open_beads_listed"
	qaMilestoneSpecRead    = "spec_read"
	qaMilestoneArchRead    = "arch_read"
	qaMilestoneFileAudit   = "file_audit"
	qaMilestoneUnittest    = "unittest"
	qaMilestoneRuntimeSmoke = "runtime_smoke"
)

// QAReviewProgress records which verification steps already succeeded this cycle.
type QAReviewProgress struct {
	WorkflowID string            `json:"workflow_id"`
	State      string            `json:"state"`
	Rig        string            `json:"rig"`
	UpdatedAt  time.Time         `json:"updated_at"`
	Completed  map[string]bool   `json:"completed"`
}

func qaReviewProgressPath(townRoot, rig string) string {
	if townRoot == "" || rig == "" {
		return ""
	}
	return filepath.Join(townRoot, rig, "qa", "qa-review-progress.json")
}

func newQAReviewProgress(workflowID, state, rig string) *QAReviewProgress {
	return &QAReviewProgress{
		WorkflowID: workflowID,
		State:      state,
		Rig:        rig,
		UpdatedAt:  time.Now().UTC(),
		Completed:  map[string]bool{},
	}
}

func loadQAReviewProgress(townRoot, rig, workflowID, state string) *QAReviewProgress {
	path := qaReviewProgressPath(townRoot, rig)
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var p QAReviewProgress
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

func saveQAReviewProgress(townRoot, rig string, p *QAReviewProgress) error {
	if p == nil {
		return nil
	}
	path := qaReviewProgressPath(townRoot, rig)
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

func clearQAReviewProgress(townRoot, rig string) {
	path := qaReviewProgressPath(townRoot, rig)
	if path == "" {
		return
	}
	_ = os.Remove(path)
}

func (p *QAReviewProgress) mark(key string) bool {
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

func (p *QAReviewProgress) done(key string) bool {
	return p != nil && p.Completed != nil && p.Completed[key]
}

func qaMilestonesFromReadCommand(cmd string) []string {
	lower := strings.ToLower(cmd)
	var keys []string
	if strings.Contains(lower, "spec.md") {
		keys = append(keys, qaMilestoneSpecRead)
	}
	if strings.Contains(lower, "architecture.md") {
		keys = append(keys, qaMilestoneArchRead)
	}
	if strings.Contains(lower, "find ") && (strings.Contains(lower, "wc ") || strings.Contains(lower, "-exec wc")) {
		keys = append(keys, qaMilestoneFileAudit)
	}
	return keys
}

func (r *stateRunner) initQAReviewProgress() string {
	if r.task == nil || r.task.State != "qa_review" || !strings.EqualFold(strings.TrimSpace(r.hooks.Track), "qa") {
		return ""
	}
	existing := loadQAReviewProgress(r.townRoot, r.rig, r.task.WorkflowID, r.task.State)
	if existing != nil {
		r.qaProgress = existing
	} else {
		r.qaProgress = newQAReviewProgress(r.task.WorkflowID, r.task.State, r.rig)
	}
	r.applyQAProgressToTrack()
	return formatQAReviewProgressBlock(r.qaProgress, r.rig, r.v.UnittestCommandHint())
}

func (r *stateRunner) applyQAProgressToTrack() {
	if r.qaProgress == nil || r.track == nil {
		return
	}
	if r.qaProgress.done(qaMilestoneClosedBeads) {
		r.track.bdListClosedOK = true
	}
	if r.qaProgress.done(qaMilestoneOpenBeads) {
		r.track.listOpenOK = true
	}
	if r.qaProgress.done(qaMilestoneUnittest) {
		r.track.unittestOK = true
	}
	if r.qaProgress.done(qaMilestoneRuntimeSmoke) {
		r.track.qaSmokeOK = true
	}
}

func (r *stateRunner) persistQAReviewProgress(cmd string) {
	if r.qaProgress == nil || r.track == nil || cmd == "" {
		return
	}
	changed := false
	if r.track.bdListClosedOK {
		changed = r.qaProgress.mark(qaMilestoneClosedBeads) || changed
	}
	if r.track.listOpenOK {
		changed = r.qaProgress.mark(qaMilestoneOpenBeads) || changed
	}
	if r.track.unittestOK {
		changed = r.qaProgress.mark(qaMilestoneUnittest) || changed
	}
	if r.track.qaSmokeOK {
		changed = r.qaProgress.mark(qaMilestoneRuntimeSmoke) || changed
	}
	for _, key := range qaMilestonesFromReadCommand(cmd) {
		if r.qaProgress.mark(key) {
			changed = true
		}
	}
	if !changed {
		return
	}
	if err := saveQAReviewProgress(r.townRoot, r.rig, r.qaProgress); err != nil {
		orchestratedFprintfStderr("[gt-agent] qa progress save: %v\n", err)
	}
}

func formatQAReviewProgressBlock(p *QAReviewProgress, rig, unittestHint string) string {
	if p == nil || len(p.Completed) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## QA review progress (persisted — do not repeat completed steps)\n")
	b.WriteString(fmt.Sprintf("Workflow %s is resuming `qa_review`. These checks already succeeded in an earlier run:\n\n", p.WorkflowID))
	add := func(key, skip string) {
		if p.done(key) {
			b.WriteString("- ✓ ")
			b.WriteString(skip)
			b.WriteString("\n")
		}
	}
	add(qaMilestoneClosedBeads, "`bd list --status=closed` for implement beads")
	add(qaMilestoneOpenBeads, "`bd list --status=open` for implement beads")
	add(qaMilestoneSpecRead, "read `SPEC.md`")
	add(qaMilestoneArchRead, "read `architecture.md`")
	add(qaMilestoneFileAudit, "`find`/`wc` file size audit under the layout root")
	if unittestHint != "" {
		add(qaMilestoneUnittest, fmt.Sprintf("verification: `%s`", unittestHint))
	} else {
		add(qaMilestoneUnittest, "unit/integration verification command")
	}
	add(qaMilestoneRuntimeSmoke, "runtime smoke (`go run` + curl for static assets and API)")

	var todo []string
	if !p.done(qaMilestoneClosedBeads) {
		todo = append(todo, "list closed implement beads")
	}
	if !p.done(qaMilestoneOpenBeads) {
		todo = append(todo, "list open implement beads")
	}
	if !p.done(qaMilestoneSpecRead) {
		todo = append(todo, "read SPEC.md")
	}
	if !p.done(qaMilestoneArchRead) {
		todo = append(todo, "read architecture.md")
	}
	if !p.done(qaMilestoneFileAudit) {
		todo = append(todo, "audit file sizes under layout root")
	}
	if !p.done(qaMilestoneUnittest) {
		todo = append(todo, "run profile verification")
	}
	if !p.done(qaMilestoneRuntimeSmoke) {
		todo = append(todo, "runtime smoke when web+server profile applies")
	}
	if len(todo) > 0 {
		b.WriteString("\nStill required this cycle:\n")
		for _, item := range todo {
			b.WriteString("- ")
			b.WriteString(item)
			b.WriteString("\n")
		}
	}
	b.WriteString("\nOnly run CMD lines for **remaining** items (or one targeted re-check if you suspect regression). ")
	b.WriteString("When everything is satisfied, reply with JSON only.\n")
	b.WriteString(fmt.Sprintf("Work from town root; rig path `%s/mayor/rig/`.\n", rig))
	return b.String()
}

// clearQAReviewProgressIfLeaving clears persisted QA checkpoints when the workflow
// leaves qa_review (success, failure to implementation, timeout, etc.).
func clearQAReviewProgressIfLeaving(townRoot, rig, fromState, nextState string) {
	if fromState != "qa_review" || nextState == "qa_review" {
		return
	}
	clearQAReviewProgress(townRoot, rig)
	orchestratedPrintf("[gt-agent] cleared qa-review-progress for rig %s (left qa_review → %s)\n", rig, nextState)
}
