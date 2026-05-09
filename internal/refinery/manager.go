package refinery

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/constants"
	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/nudge"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/tmux"
	"github.com/steveyegge/gastown/internal/util"
)

// Common errors
var (
	ErrNotRunning     = errors.New("refinery not running")
	ErrAlreadyRunning = errors.New("refinery already running")
	ErrNoQueue        = errors.New("no items in queue")
)

// Manager handles refinery lifecycle and queue operations.
type Manager struct {
	rig     *rig.Rig
	workDir string
	output  io.Writer // Output destination for user-facing messages
}

type scoredIssue struct {
	issue *beads.Issue
	score float64
}

// NewManager creates a new refinery manager for a rig.
func NewManager(r *rig.Rig) *Manager {
	return &Manager{
		rig:     r,
		workDir: r.Path,
		output:  os.Stdout,
	}
}

// SetOutput sets the output writer for user-facing messages.
// This is useful for testing or redirecting output.
func (m *Manager) SetOutput(w io.Writer) {
	m.output = w
}

// SessionName returns the tmux session name for this refinery.
func (m *Manager) SessionName() string {
	return session.RefinerySessionName(session.PrefixFor(m.rig.Name), m.rig.Name)
}

// IsRunning checks if the refinery session is active and healthy.
func (m *Manager) IsRunning() (bool, error) {
	sp := session.GetDefaultProvider(m.townRoot())
	return sp.Exists(context.Background(), m.SessionName())
}

// IsHealthy checks if the refinery is running and has been active recently.
func (m *Manager) IsHealthy(maxInactivity time.Duration) constants.ZombieStatus {
	sp := session.GetDefaultProvider(m.townRoot())
	if tp, ok := sp.(*session.TmuxProvider); ok {
		return tp.Tmux().CheckSessionHealth(m.SessionName(), maxInactivity)
	}
	if running, _ := sp.Exists(context.Background(), m.SessionName()); running {
		return constants.SessionHealthy
	}
	return constants.SessionDead
}

// Status returns information about the refinery session.
func (m *Manager) Status() (*tmux.SessionInfo, error) {
	sp := session.GetDefaultProvider(m.townRoot())
	sessionID := m.SessionName()
	ctx := context.Background()

	running, err := sp.Exists(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("checking session: %w", err)
	}
	if !running {
		return nil, ErrNotRunning
	}

	if tp, ok := sp.(*session.TmuxProvider); ok {
		return tp.Tmux().GetSessionInfo(sessionID)
	}
	return &tmux.SessionInfo{Name: sessionID}, nil
}

// Start starts the refinery.
// If foreground is true, returns an error (foreground mode deprecated).
// Otherwise, spawns a Claude agent in a session to process the merge queue.
func (m *Manager) Start(foreground bool, agentOverride string) error {
	if foreground {
		return fmt.Errorf("foreground mode is deprecated; use background mode (remove --foreground flag)")
	}

	townRoot := m.townRoot()
	sp := session.GetDefaultProvider(townRoot)
	sessionID := m.SessionName()
	ctx := context.Background()

	// Check if session already exists
	if running, _ := sp.Exists(ctx, sessionID); running {
		return ErrAlreadyRunning
	}

	// Working directory is the refinery worktree
	refineryRigDir := filepath.Join(m.rig.Path, "refinery", "rig")
	if _, err := os.Stat(refineryRigDir); os.IsNotExist(err) {
		bareRepoPath := filepath.Join(m.rig.Path, ".repo.git")
		_, bareErr := os.Stat(bareRepoPath)
		standardGitPath := filepath.Join(m.rig.Path, ".git")
		_, standardGitErr := os.Stat(standardGitPath)
		if os.IsNotExist(bareErr) && standardGitErr == nil {
			refineryRigDir = filepath.Join(m.rig.Path, "mayor", "rig")
		} else if repairErr := m.repairRefineryWorktree(refineryRigDir); repairErr != nil {
			_, _ = fmt.Fprintf(m.output, "⚠ Could not repair refinery worktree: %v (falling back to mayor/rig)\n", repairErr)
			refineryRigDir = filepath.Join(m.rig.Path, "mayor", "rig")
		}
	}

	// Resolve CLAUDE_CONFIG_DIR from accounts.json
	accountsPath := constants.MayorAccountsPath(townRoot)
	runtimeConfigDir, _, _ := config.ResolveAccountConfigDir(accountsPath, "")
	if runtimeConfigDir == "" {
		runtimeConfigDir = os.Getenv("CLAUDE_CONFIG_DIR")
	}

	// Ensure .gitignore has required Gas Town patterns
	if err := rig.EnsureGitignorePatterns(refineryRigDir); err != nil {
		style.PrintWarning("could not update refinery .gitignore: %v", err)
	}

	// Use unified session lifecycle
	var theme *tmux.Theme
	if _, isTmux := sp.(*session.TmuxProvider); isTmux {
		theme = tmux.ResolveSessionTheme(townRoot, m.rig.Name, "refinery", "")
	}

	extraEnv := map[string]string{"GT_REFINERY": "1"}

	_, err := session.StartSession(ctx, sp, session.SessionConfig{
		SessionID:        sessionID,
		WorkDir:          refineryRigDir,
		Role:             "refinery",
		RigName:          m.rig.Name,
		RigPath:          m.rig.Path,
		TownRoot:         townRoot,
		RuntimeConfigDir: runtimeConfigDir,
		AgentOverride:    agentOverride,
		ExtraEnv:         extraEnv,
		Theme:            theme,
		WaitForAgent:     true,
		WaitFatal:        true,
		AutoRespawn:      true,
		AcceptBypass:     true,
		Beacon: session.BeaconConfig{
			Recipient: session.BeaconRecipient("refinery", "", m.rig.Name),
			Sender:    "deacon",
			Topic:     "patrol",
		},
	})
	if err != nil {
		return err
	}

	// Start nudge-queue poller only for tmux
	if _, isTmux := sp.(*session.TmuxProvider); isTmux {
		if _, pollerErr := nudge.StartPoller(townRoot, sessionID); pollerErr != nil {
			log.Printf("warning: could not start nudge poller for %s: %v", sessionID, pollerErr)
		}
	}

	// Generate a run ID for logging/telemetry
	runID := uuid.New().String()

	// Stream refinery's Claude Code JSONL conversation log to VictoriaLogs (opt-in).
	if os.Getenv("GT_LOG_AGENT_OUTPUT") == "true" && os.Getenv("GT_OTEL_LOGS_URL") != "" {
		if err := session.ActivateAgentLogging(sessionID, refineryRigDir, runID); err != nil {
			log.Printf("warning: agent log watcher setup failed for %s: %v", sessionID, err)
		}
	}

	// Record the agent instantiation event (GASTA root span).
	runtimeConfig := config.ResolveRoleAgentConfig("refinery", townRoot, m.rig.Path)
	session.RecordAgentInstantiateFromDir(context.Background(), runID, runtimeConfig.ResolvedAgent,
		"refinery", "refinery", sessionID, m.rig.Name, townRoot, "", refineryRigDir)

	return nil
}

// repairRefineryWorktree recreates a missing refinery/rig worktree from the
// shared bare repo (.repo.git). The refinery worktree is created during
// `gt rig add` but can be lost if `git worktree prune` runs, the directory
// is deleted, or the .git file becomes corrupted. This self-heals on startup
// instead of requiring manual intervention.
func (m *Manager) repairRefineryWorktree(refineryRigDir string) error {
	bareRepoPath := filepath.Join(m.rig.Path, ".repo.git")
	if _, err := os.Stat(bareRepoPath); os.IsNotExist(err) {
		return fmt.Errorf("bare repo not found at %s", bareRepoPath)
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(refineryRigDir), 0755); err != nil {
		return fmt.Errorf("creating refinery dir: %w", err)
	}

	// Prune stale worktree entries so git doesn't reject the add
	bareGit := git.NewGitWithDir(bareRepoPath, "")
	_ = bareGit.WorktreePrune()

	// Create worktree on the rig's default branch
	defaultBranch := m.rig.DefaultBranch()
	if err := bareGit.WorktreeAddExisting(refineryRigDir, defaultBranch); err != nil {
		return fmt.Errorf("git worktree add: %w", err)
	}

	// Configure hooks path (matches rig add behavior)
	refineryGit := git.NewGit(refineryRigDir)
	if err := refineryGit.ConfigureHooksPath(); err != nil {
		// Non-fatal: worktree is usable without hooks
		_, _ = fmt.Fprintf(m.output, "⚠ Could not configure hooks for repaired worktree: %v\n", err)
	}

	_, _ = fmt.Fprintf(m.output, "✓ Auto-repaired missing refinery worktree at %s\n", refineryRigDir)
	return nil
}

// Stop stops the refinery.
func (m *Manager) Stop() error {
	sp := session.GetDefaultProvider(m.townRoot())
	sessionID := m.SessionName()
	ctx := context.Background()

	// Check if session exists
	running, _ := sp.Exists(ctx, sessionID)
	if !running {
		return ErrNotRunning
	}

	// Stop the session via provider
	return sp.Stop(ctx, sessionID, false)
}

// townRoot returns the town root directory.
func (m *Manager) townRoot() string {
	return filepath.Dir(m.rig.Path)
}

// Queue returns the current merge queue.
// Uses beads merge-request issues as the source of truth (not git branches).
// ZFC-compliant: beads is the source of truth, no state file.
func (m *Manager) Queue() ([]QueueItem, error) {
	// Query beads for open merge-request issues
	// BeadsPath() returns the git-synced beads location
	b := beads.New(m.rig.BeadsPath())
	issues, err := b.ListMergeRequests(beads.ListOptions{
		Label:    "gt:merge-request",
		Status:   "open",
		Priority: -1, // No priority filter
	})
	if err != nil {
		return nil, fmt.Errorf("querying merge queue from beads: %w", err)
	}

	// Score and sort issues by priority score (highest first)
	now := time.Now()
	scored := make([]scoredIssue, 0, len(issues))
	for _, issue := range issues {
		// Defensive filter: bd status filters can drift; queue must only include open MRs.
		if issue == nil || issue.Status != "open" {
			continue
		}

		// Filter by rig — wisps are shared across all rigs (GH#2718).
		fields := beads.ParseMRFields(issue)
		if fields != nil && fields.Rig != "" && !strings.EqualFold(fields.Rig, m.rig.Name) {
			continue
		}

		score := m.calculateIssueScore(issue, now)
		scored = append(scored, scoredIssue{issue: issue, score: score})
	}

	sort.Slice(scored, func(i, j int) bool {
		return compareScoredIssues(scored[i], scored[j])
	})

	// Convert scored issues to queue items
	var items []QueueItem
	pos := 1
	for _, s := range scored {
		mr := m.issueToMR(s.issue)
		if mr != nil {
			items = append(items, QueueItem{
				Position: pos,
				MR:       mr,
				Age:      formatAge(mr.CreatedAt),
			})
			pos++
		}
	}

	return items, nil
}

func compareScoredIssues(a, b scoredIssue) bool {
	if a.score != b.score {
		return a.score > b.score
	}
	if a.issue == nil || b.issue == nil {
		return a.issue != nil
	}
	return a.issue.ID < b.issue.ID
}

// calculateIssueScore computes the priority score for an MR issue.
// Higher scores mean higher priority (process first).
func (m *Manager) calculateIssueScore(issue *beads.Issue, now time.Time) float64 {
	fields := beads.ParseMRFields(issue)

	// Parse MR creation time
	mrCreatedAt := parseTime(issue.CreatedAt)
	if mrCreatedAt.IsZero() {
		mrCreatedAt = now // Fallback
	}

	// Build score input
	input := ScoreInput{
		Priority:    issue.Priority,
		MRCreatedAt: mrCreatedAt,
		Now:         now,
	}

	// Add fields from MR metadata if available
	if fields != nil {
		input.RetryCount = fields.RetryCount

		// Parse convoy created at if available
		if fields.ConvoyCreatedAt != "" {
			if convoyTime := parseTime(fields.ConvoyCreatedAt); !convoyTime.IsZero() {
				input.ConvoyCreatedAt = &convoyTime
			}
		}
	}

	return ScoreMRWithDefaults(input)
}

// issueToMR converts a beads issue to a MergeRequest.
func (m *Manager) issueToMR(issue *beads.Issue) *MergeRequest {
	if issue == nil {
		return nil
	}

	// Get configured default branch for this rig
	defaultBranch := m.rig.DefaultBranch()

	fields := beads.ParseMRFields(issue)
	if fields == nil {
		// No MR fields in description, construct from title/ID
		return &MergeRequest{
			ID:           issue.ID,
			IssueID:      issue.ID,
			Status:       MROpen,
			CreatedAt:    parseTime(issue.CreatedAt),
			TargetBranch: defaultBranch,
		}
	}

	// Default target to rig's default branch if not specified
	target := fields.Target
	if target == "" {
		target = defaultBranch
	}

	return &MergeRequest{
		ID:           issue.ID,
		Branch:       fields.Branch,
		Worker:       fields.Worker,
		IssueID:      fields.SourceIssue,
		TargetBranch: target,
		MergeCommit:  fields.MergeCommit,
		Status:       MROpen,
		CreatedAt:    parseTime(issue.CreatedAt),
	}
}

// parseTime parses a time string, returning zero time on error.
func parseTime(s string) time.Time {
	// Try RFC3339 first (most common)
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		// Try date-only format as fallback
		t, _ = time.Parse("2006-01-02", s)
	}
	return t
}

// formatAge formats a duration since the given time.
func formatAge(t time.Time) string {
	d := time.Since(t)

	if d < time.Minute {
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

// Common errors for MR operations
var (
	ErrMRNotFound  = errors.New("merge request not found")
	ErrMRNotFailed = errors.New("merge request has not failed")
)

// GetMR returns a merge request by ID.
// ZFC-compliant: delegates to FindMR which uses beads as source of truth.
// Deprecated: Use FindMR directly for more flexible matching.
func (m *Manager) GetMR(id string) (*MergeRequest, error) {
	return m.FindMR(id)
}

// FindMR finds a merge request by ID or branch name in the queue.
func (m *Manager) FindMR(idOrBranch string) (*MergeRequest, error) {
	queue, err := m.Queue()
	if err != nil {
		return nil, err
	}

	for _, item := range queue {
		// Match by ID
		if item.MR.ID == idOrBranch {
			return item.MR, nil
		}
		// Match by branch name (with or without polecat/ prefix)
		if item.MR.Branch == idOrBranch {
			return item.MR, nil
		}
		if constants.BranchPolecatPrefix+idOrBranch == item.MR.Branch {
			return item.MR, nil
		}
		// Match by ID prefix (partial match for convenience)
		if strings.HasPrefix(item.MR.ID, idOrBranch) {
			return item.MR, nil
		}
	}

	return nil, ErrMRNotFound
}

// Retry is deprecated - the Refinery agent handles retry logic autonomously.
// ZFC-compliant: no state file, agent uses beads issue status.
// The agent will automatically retry failed MRs in its patrol cycle.
func (m *Manager) Retry(_ string, _ bool) error {
	_, _ = fmt.Fprintln(m.output, "Note: Retry is deprecated. The Refinery agent handles retries autonomously via beads.")
	return nil
}

// RegisterMR is deprecated - MRs are registered via beads merge-request issues.
// ZFC-compliant: beads is the source of truth, not state file.
// Use 'gt mr create' or create a merge-request type bead directly.
func (m *Manager) RegisterMR(_ *MergeRequest) error {
	return fmt.Errorf("RegisterMR is deprecated: use beads to create merge-request issues")
}

// RejectMR manually rejects a merge request.
// It closes the MR with rejected status and optionally notifies the worker.
// Returns the rejected MR for display purposes.
func (m *Manager) RejectMR(idOrBranch string, reason string, notify bool) (*MergeRequest, error) {
	mr, err := m.FindMR(idOrBranch)
	if err != nil {
		return nil, err
	}

	// Verify MR is open or in_progress (can't reject already closed)
	if mr.IsClosed() {
		return nil, fmt.Errorf("%w: MR is already closed with reason: %s", ErrClosedImmutable, mr.CloseReason)
	}

	// Close the bead in storage with the rejection reason
	b := beads.New(m.rig.BeadsPath())
	if err := b.CloseWithReason("rejected: "+reason, mr.ID); err != nil {
		return nil, fmt.Errorf("failed to close MR bead: %w", err)
	}

	// Update in-memory state for return value
	if err := mr.Close(CloseReasonRejected); err != nil {
		// Non-fatal: bead is already closed, just log
		_, _ = fmt.Fprintf(m.output, "Warning: failed to update MR state: %v\n", err)
	}
	mr.Error = reason

	// Optionally notify worker
	if notify {
		m.notifyWorkerRejected(mr, reason)
	}

	return mr, nil
}

// PostMergeResult holds the result of a post-merge cleanup operation.
type PostMergeResult struct {
	MR                  *MergeRequest
	MRClosed            bool
	SourceIssueClosed   bool
	SourceIssueID       string
	SourceIssueNotFound bool // true if source issue doesn't exist (already closed or invalid)
}

// PostMerge performs post-merge cleanup for a successfully merged MR.
// It closes the MR bead and its source issue. Branch deletion is handled
// by the caller since the Manager doesn't have git access.
func (m *Manager) PostMerge(idOrBranch string) (*PostMergeResult, error) {
	mr, err := m.FindMR(idOrBranch)
	if err != nil {
		return nil, err
	}

	result := &PostMergeResult{
		MR:            mr,
		SourceIssueID: mr.IssueID,
	}

	b := beads.New(m.rig.BeadsPath())

	// Close the MR bead
	if mr.IsClosed() {
		_, _ = fmt.Fprintf(m.output, "  %s MR already closed\n", style.Dim.Render("—"))
		result.MRClosed = true
	} else {
		if err := b.CloseWithReason("merged", mr.ID); err != nil {
			return result, fmt.Errorf("closing MR bead: %w", err)
		}
		if closeErr := mr.Close(CloseReasonMerged); closeErr != nil {
			_, _ = fmt.Fprintf(m.output, "Warning: failed to update MR state: %v\n", closeErr)
		}
		result.MRClosed = true
	}

	// Close the source issue with reason and --force to bypass dependency checks.
	// The source issue may have an attached molecule (wisp) whose open steps
	// would block a normal bd close. ForceCloseWithReason bypasses this,
	// matching how gt done handles closures for the no-MR path.
	if mr.IssueID != "" {
		closeReason := fmt.Sprintf("Merged in %s", mr.ID)
		if mr.MergeCommit != "" {
			closeReason = fmt.Sprintf("%s\ntarget_branch: %s\ncommit_sha: %s", closeReason, mr.TargetBranch, mr.MergeCommit)
		}
		if err := b.ForceCloseWithReason(closeReason, mr.IssueID); err != nil {
			// Check if already closed (by polecat's gt done) — that's fine
			if issue, showErr := b.Show(mr.IssueID); showErr == nil && beads.IssueStatus(issue.Status).IsTerminal() {
				_, _ = fmt.Fprintf(m.output, "  %s source issue already closed: %s\n", style.Dim.Render("○"), mr.IssueID)
				result.SourceIssueClosed = true
			} else {
				_, _ = fmt.Fprintf(m.output, "  %s source issue close: %v\n", style.Dim.Render("○"), err)
				result.SourceIssueNotFound = true
			}
		} else {
			result.SourceIssueClosed = true
		}
	}

	return result, nil
}

// notifyWorkerRejected sends a rejection notification to a polecat.
func (m *Manager) notifyWorkerRejected(mr *MergeRequest, reason string) {
	// Nudge polecat about rejection instead of sending permanent mail.
	polecatName := strings.TrimPrefix(mr.Worker, "polecats/")
	target := fmt.Sprintf("%s/%s", m.rig.Name, polecatName)
	nudgeMsg := fmt.Sprintf("MR rejected: branch=%s issue=%s reason=%s — review feedback and resubmit with 'gt done'",
		mr.Branch, mr.IssueID, reason)
	nudgeCmd := exec.Command("gt", "nudge", target, nudgeMsg)
	util.SetDetachedProcessGroup(nudgeCmd)
	nudgeCmd.Dir = m.workDir
	if err := nudgeCmd.Run(); err != nil {
		log.Printf("warning: nudging worker about rejection for %s: %v", mr.IssueID, err)
	}
}

// Town root is computed in Start() as filepath.Dir(m.rig.Path) and passed
// through to callers — no filesystem-inference function needed (ZFC gt-qago).
