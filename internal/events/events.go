// Package events provides event logging for the gt activity feed.
//
// Events are written to ~/gt/.events.jsonl (raw audit log) and later
// curated by the feed daemon into ~/.feed.jsonl (user-facing).
package events

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"github.com/steveyegge/gastown/internal/workspace"
	"github.com/steveyegge/gastown/internal/natsutil"
)

// Event represents an activity event in Gas Town.
type Event struct {
	Timestamp  string                 `json:"ts"`
	Source     string                 `json:"source"`
	Type       string                 `json:"type"`
	Actor      string                 `json:"actor"`
	Payload    map[string]interface{} `json:"payload,omitempty"`
	Visibility string                 `json:"visibility"`
}

// Visibility levels for events.
const (
	VisibilityAudit = "audit" // Only in raw events log
	VisibilityFeed  = "feed"  // Appears in curated feed
	VisibilityBoth  = "both"  // Both audit and feed
)

// Common event types for gt commands.
const (
	TypeSling   = "sling"
	TypeHook    = "hook"
	TypeUnhook  = "unhook"
	TypeHandoff = "handoff"
	TypeDone    = "done"
	TypeMail    = "mail"
	TypeSpawn   = "spawn"
	TypeKill    = "kill"
	TypeNudge   = "nudge"
	TypeBoot    = "boot"
	TypeHalt    = "halt"

	// Session events (for seance discovery)
	TypeSessionStart = "session_start"
	TypeSessionEnd   = "session_end"

	// Session death events (for crash investigation)
	TypeSessionDeath = "session_death" // Feed-visible session termination
	TypeMassDeath    = "mass_death"    // Multiple sessions died in short window

	// Witness patrol events
	TypePatrolStarted   = "patrol_started"
	TypePolecatChecked  = "polecat_checked"
	TypePolecatNudged   = "polecat_nudged"
	TypeEscalationSent   = "escalation_sent"
	TypeEscalationAcked  = "escalation_acked"
	TypeEscalationClosed = "escalation_closed"
	TypePatrolComplete   = "patrol_complete"

	// Merge queue events (emitted by refinery)
	TypeMergeStarted = "merge_started"
	TypeMerged       = "merged"
	TypeMergeFailed  = "merge_failed"
	TypeMergeSkipped = "merge_skipped"

	// Scheduler events
	TypeSchedulerEnqueue        = "scheduler_enqueue"         // Bead scheduled for deferred dispatch
	TypeSchedulerDispatch       = "scheduler_dispatch"        // Bead dispatched from scheduler
	TypeSchedulerDispatchFailed = "scheduler_dispatch_failed" // Bead dispatch failed (requeued)
	TypeSchedulerCloseRetry     = "scheduler_close_retry"     // Context close needed last-resort attempt

	// Orchestrator rig-flow workflow events (feed-visible progression)
	TypeWorkflowStart       = "workflow_start"
	TypeWorkflowTransition  = "workflow_transition"
)

// EventsFile is the name of the raw events log.
const EventsFile = ".events.jsonl"

// Log writes an event to the events log.
// The event is appended to ~/gt/.events.jsonl.
// Returns nil if logging fails (events are best-effort).
func Log(eventType, actor string, payload map[string]interface{}, visibility string) error {
	event := Event{
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		Source:     "gt",
		Type:       eventType,
		Actor:      actor,
		Payload:    payload,
		Visibility: visibility,
	}
	return write(event)
}

// LogAt writes an event to the given town root (for long-running services with explicit GT_ROOT).
func LogAt(townRoot, eventType, actor string, payload map[string]interface{}, visibility string) error {
	event := Event{
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		Source:     "gt",
		Type:       eventType,
		Actor:      actor,
		Payload:    payload,
		Visibility: visibility,
	}
	return writeAt(townRoot, event)
}

// LogFeed is a convenience wrapper for feed-visible events.
func LogFeed(eventType, actor string, payload map[string]interface{}) error {
	return Log(eventType, actor, payload, VisibilityFeed)
}

// LogFeedAt is LogFeed with an explicit town root (orchestrator, daemon).
func LogFeedAt(townRoot, eventType, actor string, payload map[string]interface{}) error {
	return LogAt(townRoot, eventType, actor, payload, VisibilityFeed)
}

// LogAudit is a convenience wrapper for audit-only events.
func LogAudit(eventType, actor string, payload map[string]interface{}) error {
	return Log(eventType, actor, payload, VisibilityAudit)
}

// write appends an event to the events file.
// Uses flock for cross-process synchronization — sync.Mutex only protects
// intra-process goroutines, but multiple gt processes write concurrently.
func write(event Event) error {
	townRoot, err := workspace.FindFromCwd()
	if err != nil || townRoot == "" {
		return nil
	}
	return writeAt(townRoot, event)
}

func writeAt(townRoot string, event Event) error {
	if townRoot == "" {
		return nil
	}

	eventsPath := filepath.Join(townRoot, EventsFile)

	// Marshal event to JSON
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshaling event: %w", err)
	}
	data = append(data, '\n')

	// Acquire cross-process file lock
	fl := flock.New(eventsPath + ".lock")
	if err := fl.Lock(); err != nil {
		return fmt.Errorf("acquiring events file lock: %w", err)
	}
	defer fl.Unlock() //nolint:errcheck // best-effort unlock

	f, err := os.OpenFile(eventsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644) //nolint:gosec // G302: events file is non-sensitive operational data
	if err != nil {
		return fmt.Errorf("opening events file: %w", err)
	}

	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("writing event: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("closing events file: %w", err)
	}

	// Shadow publish to NATS for real-time dashboard updates.
	// This is best-effort and should not block or fail the command.
	publishToNats(event)

	return nil
}

func publishToNats(event Event) {
	// Map Event to natsutil.ActivityEvent
	rig, _ := event.Payload["rig"].(string)
	if rig == "" {
		rig = "town" // fallback for global events
	}
	agent := event.Actor
	status := event.Type

	ae := natsutil.ActivityEvent{
		RigID:   rig,
		AgentID: agent,
		Status:  status,
	}
	if event.Timestamp != "" {
		if ts, err := time.Parse(time.RFC3339, event.Timestamp); err == nil {
			ae.Timestamp = ts
		}
	}
	if ae.Timestamp.IsZero() {
		ae.Timestamp = time.Now()
	}

	if b, err := json.Marshal(event.Payload); err == nil {
		ae.Payload = string(b)
	}

	// Connect and publish (short-lived connection for CLI commands)
	url := os.Getenv("GT_NATS_URL")
	nc, err := natsutil.NewClient(url)
	if err != nil {
		return
	}
	defer nc.Close()
	_ = nc.PublishActivity(ae)
}

// Payload helpers for common event structures.

// SlingPayload creates a payload for sling events.
func SlingPayload(beadID, target string) map[string]interface{} {
	return map[string]interface{}{
		"bead":   beadID,
		"target": target,
	}
}

// HookPayload creates a payload for hook events.
func HookPayload(beadID string) map[string]interface{} {
	return map[string]interface{}{
		"bead": beadID,
	}
}

// HandoffPayload creates a payload for handoff events.
func HandoffPayload(subject string, toSession bool) map[string]interface{} {
	p := map[string]interface{}{
		"to_session": toSession,
	}
	if subject != "" {
		p["subject"] = subject
	}
	return p
}

// DonePayload creates a payload for done events.
func DonePayload(beadID, branch string) map[string]interface{} {
	return map[string]interface{}{
		"bead":   beadID,
		"branch": branch,
	}
}

// MailPayload creates a payload for mail events.
func MailPayload(to, subject string) map[string]interface{} {
	return map[string]interface{}{
		"to":      to,
		"subject": subject,
	}
}

// SpawnPayload creates a payload for spawn events.
func SpawnPayload(rig, polecat string) map[string]interface{} {
	return map[string]interface{}{
		"rig":     rig,
		"polecat": polecat,
	}
}

// BootPayload creates a payload for rig boot events.
func BootPayload(rig string, agents []string) map[string]interface{} {
	return map[string]interface{}{
		"rig":    rig,
		"agents": agents,
	}
}

// MergePayload creates a payload for merge queue events.
// mrID: merge request ID
// worker: polecat name that submitted the work
// branch: source branch being merged
// reason: failure reason (for merge_failed/merge_skipped events)
func MergePayload(mrID, worker, branch, reason string) map[string]interface{} {
	p := map[string]interface{}{
		"mr":     mrID,
		"worker": worker,
		"branch": branch,
	}
	if reason != "" {
		p["reason"] = reason
	}
	return p
}

// PatrolPayload creates a payload for patrol start/complete events.
func PatrolPayload(rig string, polecatCount int, message string) map[string]interface{} {
	p := map[string]interface{}{
		"rig":           rig,
		"polecat_count": polecatCount,
	}
	if message != "" {
		p["message"] = message
	}
	return p
}

// PolecatCheckPayload creates a payload for polecat check events.
func PolecatCheckPayload(rig, polecat, status, issue string) map[string]interface{} {
	p := map[string]interface{}{
		"rig":     rig,
		"polecat": polecat,
		"status":  status,
	}
	if issue != "" {
		p["issue"] = issue
	}
	return p
}

// NudgePayload creates a payload for nudge events.
func NudgePayload(rig, target, reason string) map[string]interface{} {
	return map[string]interface{}{
		"rig":    rig,
		"target": target,
		"reason": reason,
	}
}

// EscalationPayload creates a payload for escalation events.
func EscalationPayload(rig, target, to, reason string) map[string]interface{} {
	return map[string]interface{}{
		"rig":    rig,
		"target": target,
		"to":     to,
		"reason": reason,
	}
}

// UnhookPayload creates a payload for unhook events.
func UnhookPayload(beadID string) map[string]interface{} {
	return map[string]interface{}{
		"bead": beadID,
	}
}

// KillPayload creates a payload for kill events.
func KillPayload(rig, target, reason string) map[string]interface{} {
	return map[string]interface{}{
		"rig":    rig,
		"target": target,
		"reason": reason,
	}
}

// HaltPayload creates a payload for halt events.
func HaltPayload(services []string) map[string]interface{} {
	return map[string]interface{}{
		"services": services,
	}
}

// SessionDeathPayload creates a payload for session death events.
// session: tmux session name that died
// agent: Gas Town agent identity (e.g., "gastown/polecats/Toast")
// reason: why the session was killed (e.g., "zombie cleanup", "user request", "doctor fix")
// caller: what initiated the kill (e.g., "daemon", "doctor", "gt down")
func SessionDeathPayload(session, agent, reason, caller string) map[string]interface{} {
	return map[string]interface{}{
		"session": session,
		"agent":   agent,
		"reason":  reason,
		"caller":  caller,
	}
}

// MassDeathPayload creates a payload for mass death events.
// count: number of sessions that died
// window: time window in which deaths occurred (e.g., "5s")
// sessions: list of session names that died
// possibleCause: suspected cause if known
func MassDeathPayload(count int, window string, sessions []string, possibleCause string) map[string]interface{} {
	p := map[string]interface{}{
		"count":    count,
		"window":   window,
		"sessions": sessions,
	}
	if possibleCause != "" {
		p["possible_cause"] = possibleCause
	}
	return p
}

// SessionPayload creates a payload for session start/end events.
// sessionID: Claude Code session UUID
// role: Gas Town role (e.g., "gastown/crew/joe", "deacon")
// topic: What the session is working on
// cwd: Working directory
func SessionPayload(sessionID, role, topic, cwd string) map[string]interface{} {
	p := map[string]interface{}{
		"session_id": sessionID,
		"role":       role,
		"actor_pid":  fmt.Sprintf("%s-%d", role, os.Getpid()),
	}
	if topic != "" {
		p["topic"] = topic
	}
	if cwd != "" {
		p["cwd"] = cwd
	}
	return p
}

// SchedulerEnqueuePayload creates a payload for scheduler enqueue events.
func SchedulerEnqueuePayload(beadID, rig string) map[string]interface{} {
	return map[string]interface{}{
		"bead": beadID,
		"rig":  rig,
	}
}

// SchedulerDispatchPayload creates a payload for scheduler dispatch events.
func SchedulerDispatchPayload(beadID, rig, polecat string) map[string]interface{} {
	return map[string]interface{}{
		"bead":    beadID,
		"rig":     rig,
		"polecat": polecat,
	}
}

// SchedulerDispatchFailedPayload creates a payload for scheduler dispatch failure events.
// WorkflowStartPayload creates a feed payload when a rig-flow instance starts.
func WorkflowStartPayload(workflowID, templateID, state, role, rig string) map[string]interface{} {
	msg := fmt.Sprintf("%s %s started at %s", templateID, workflowID, state)
	if role != "" {
		msg += " (" + role + ")"
	}
	return map[string]interface{}{
		"workflow_id": workflowID,
		"template_id": templateID,
		"to_state":    state,
		"role":        role,
		"rig":         rig,
		"message":     msg,
	}
}

// WorkflowTransitionPayload creates a feed payload for a completed workflow step.
func WorkflowTransitionPayload(workflowID, templateID, fromState, toState, outcome, role, rig string) map[string]interface{} {
	msg := fmt.Sprintf("%s %s: %s → %s", templateID, workflowID, fromState, toState)
	if outcome != "" {
		msg += " (" + outcome + ")"
	}
	if role != "" {
		msg += " [" + role + "]"
	}
	return map[string]interface{}{
		"workflow_id": workflowID,
		"template_id": templateID,
		"from_state":  fromState,
		"to_state":    toState,
		"outcome":     outcome,
		"role":        role,
		"rig":         rig,
		"message":     msg,
	}
}

func SchedulerDispatchFailedPayload(beadID, rig, errMsg string) map[string]interface{} {
	return map[string]interface{}{
		"bead":  beadID,
		"rig":   rig,
		"error": errMsg,
	}
}
