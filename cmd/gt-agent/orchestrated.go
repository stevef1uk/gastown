package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/steveyegge/gastown/cmd/gt-agent/internal/llm"
	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/nudge"
	"github.com/steveyegge/gastown/internal/agentenv"
	"github.com/steveyegge/gastown/internal/orchestrator"
)

const (
	defaultOrchPollInterval      = 15 * time.Second
	maxOrchestratedCmdTurns      = 5
	maxOrchestratedQACmdTurns    = 8
	maxOrchestratedRetryFeedback = 6000 // chars persisted for next fetch_task attempt
)

func maxOrchestratedTurnsForState(state string) int {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "qa_review", "plan_review":
		return maxOrchestratedQACmdTurns
	default:
		return maxOrchestratedCmdTurns
	}
}

var jsonBlockRE = regexp.MustCompile("(?s)```(?:json)?\\s*([\\s\\S]*?)```")

type orchestratedTaskResult struct {
	Outcome      string   `json:"outcome"`
	Summary      string   `json:"summary"`
	CommandsRun  []string `json:"commands_run"`
}

func orchestratorPollInterval() time.Duration {
	if s := os.Getenv("GT_ORCH_POLL_INTERVAL"); s != "" {
		if d, err := time.ParseDuration(s); err == nil && d > 0 {
			return d
		}
	}
	return defaultOrchPollInterval
}

func runOrchestrated(ctx context.Context, client *llm.Client, townRoot, role, rig, sessionName, stateFile string, state AgentState) error {
	agentID := orchestrator.OrchestratorAgentID(role, rig)
	pollEvery := orchestratorPollInterval()
	initOrchestratedLogger(townRoot, sessionName)
	if !orchestratedQuiet() {
		orchestratedLog("orchestrated %s (poll %s)", agentID, pollEvery)
	}

	for {
		if isShutdownRequested() {
			break
		}

		task, err := orchestrator.FetchTask(townRoot, agentID)
		if err != nil {
			orchestratedFprintfStderr( "[gt-agent] fetch_task error: %v\n", err)
			time.Sleep(pollEvery)
			continue
		}
		if task == nil {
			time.Sleep(pollEvery)
			continue
		}

		orchestratedPrintf("[gt-agent] Task wf=%s template=%s state=%s\n",
			task.WorkflowID, task.TemplateID, task.State)

		taskRig := resolveOrchestratedRigName(townRoot, orchestratedTaskRig(task, rig))
		if taskRig == "" {
			orchestratedFprintfStderr("[gt-agent] workflow %s has no rig name (set instance variable rig= or register rigs.json)\n", task.WorkflowID)
			_, _ = orchestrator.CompleteTask(townRoot, task.WorkflowID, "failure", orchestrator.OrchestratorAgentID(role, rig),
				"orchestrator rig variable missing", "")
			continue
		}
		outcome, summary, attemptLog, runErr := executeOrchestratedTask(ctx, client, townRoot, taskRig, sessionName, task, state.OrchestratedRetry)
		if runErr != nil {
			orchestratedFprintfStderr( "[gt-agent] task execution: %v\n", runErr)
			if outcome == "" {
				outcome = "fail"
			}
		}
		if outcome == "" {
			outcome = "fail"
		}

		orchestratedPrintf("[gt-agent] complete_task outcome=%q summary=%q\n", outcome, summary)
		agentID := orchestrator.OrchestratorAgentID(role, rig)
		nextState, err := orchestrator.CompleteTask(townRoot, task.WorkflowID, outcome, agentID, summary, attemptLog)
		if err != nil {
			orchestratedFprintfStderr( "[gt-agent] complete_task failed: %v\n", err)
			updateOrchestratedRetry(&state, task, "failure", err.Error(), attemptLog)
		} else {
			orchestratedPrintf("[gt-agent] next state: %s\n", nextState)
			updateOrchestratedRetryAfterComplete(&state, task, outcome, summary, attemptLog, nextState)
		}

		state.LastActivity = time.Now()
		_ = saveState(stateFile, state)
		time.Sleep(2 * time.Second)
	}

	return nil
}

func executeOrchestratedTask(ctx context.Context, client *llm.Client, townRoot, rig, sessionName string, task *orchestrator.Task, priorRetry *OrchestratedRetry) (outcome, summary, attemptLog string, err error) {
	rig = resolveOrchestratedRigName(townRoot, rig)
	if rig == "" {
		return "failure", "workflow rig name not set", "", fmt.Errorf("orchestrator rig variable missing")
	}
	systemPrompt := buildOrchestratedSystemPrompt(task)
	userPrompt := buildOrchestratedUserPrompt(task)
	var contextBlocks []string
	if block := drainOrchestratedNudges(townRoot, sessionName); block != "" {
		contextBlocks = append(contextBlocks, block)
		orchestratedPrintf("[gt-agent] injected drained nudge(s) for %s/%s\n", task.WorkflowID, task.State)
	}
	if block := formatWorkflowReworkBlock(task, rig); block != "" {
		contextBlocks = append(contextBlocks, block)
		orchestratedPrintf("[gt-agent] injecting QA/review rework context for %s/%s\n", task.WorkflowID, task.State)
	}
	if block := formatOrchestratedRetryBlock(priorRetry, task, rig); block != "" {
		contextBlocks = append(contextBlocks, block)
		orchestratedPrintf("[gt-agent] injecting prior failure context for %s/%s\n", task.WorkflowID, task.State)
	}
	if len(contextBlocks) > 0 {
		userPrompt = strings.Join(contextBlocks, "\n\n") + "\n\n" + userPrompt
	}

	messages := []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	var lastAttemptFeedback strings.Builder
	recordAttemptFeedback := func(s string) {
		if s == "" {
			return
		}
		lastAttemptFeedback.WriteString(s)
		if lastAttemptFeedback.Len() > maxOrchestratedRetryFeedback {
			trunc := lastAttemptFeedback.String()
			lastAttemptFeedback.Reset()
			lastAttemptFeedback.WriteString("...(truncated)\n")
			lastAttemptFeedback.WriteString(trunc[len(trunc)-maxOrchestratedRetryFeedback:])
		}
	}

	var designArchWrittenThisRun bool
	var planningHadCmdFailure bool
	var planningBeadCreateOK bool
	var planningBeadDeleteOK bool
	var implementationHadCmdFailure bool
	var implementationBeadCloseOK bool
	var qaHadCmdFailure bool
	var qaBdListClosedOK bool
	var qaUnittestOK bool
	var planReviewHadCmdFailure bool
	var planReviewListOpenOK bool
	var planReviewDidDelete bool

	maxTurns := maxOrchestratedTurnsForState(task.State)
	for turn := 1; turn <= maxTurns; turn++ {
		response, llmErr := client.CompleteMessages(ctx, messages)
		if llmErr != nil {
			return "fail", "", lastAttemptFeedback.String(), llmErr
		}
		orchestratedPrintf("[gt-agent] LLM response (turn %d):\n%s\n", turn, response)
		messages = append(messages, llm.Message{Role: "assistant", Content: response})

		cmdBlocks := parseOrchestratedCommands(response)
		if len(cmdBlocks) > 0 {
			var combined strings.Builder
			for _, cmd := range cmdBlocks {
				if task.State == "design" {
					if err := validateDesignCommand(cmd, rig); err != nil {
						orchestratedFprintfStderr( "[gt-agent] rejected command: %v\n", err)
						combined.WriteString(fmt.Sprintf("Command REJECTED (architect scope): %s\nReason: %v\n\n", cmd, err))
						continue
					}
				}
				if task.State == "planning" {
					if err := validatePlanningCommand(cmd, rig); err != nil {
						orchestratedFprintfStderr( "[gt-agent] rejected command: %v\n", err)
						combined.WriteString(fmt.Sprintf("Command REJECTED (planner scope): %s\nReason: %v\n\n", cmd, err))
						continue
					}
				}
				if task.State == "implementation" {
					if err := validateImplementationCommand(cmd, rig); err != nil {
						orchestratedFprintfStderr( "[gt-agent] rejected command: %v\n", err)
						combined.WriteString(fmt.Sprintf("Command REJECTED (polecat scope): %s\nReason: %v\n\n", cmd, err))
						continue
					}
				}
				if task.State == "plan_review" {
					if err := validatePlanReviewCommand(cmd, rig); err != nil {
						orchestratedFprintfStderr( "[gt-agent] rejected command: %v\n", err)
						combined.WriteString(fmt.Sprintf("Command REJECTED (plan review scope): %s\nReason: %v\n\n", cmd, err))
						continue
					}
				}
				if task.State == "qa_review" {
					if err := validateQACommand(cmd, rig, taskValidation(task)); err != nil {
						orchestratedFprintfStderr( "[gt-agent] rejected command: %v\n", err)
						combined.WriteString(fmt.Sprintf("Command REJECTED (QA scope): %s\nReason: %v\n\n", cmd, err))
						continue
					}
				}
				if fixed, ok := rewriteSpecMDPathCaseInsensitive(cmd); ok {
					cmd = fixed
				}
				if fixed, ok := rewriteOrchestratedRigPlaceholders(cmd, rig); ok {
					orchestratedPrintf("[gt-agent] rewrote RIG placeholder paths → %s: %s\n", rig, fixed)
					cmd = fixed
				}
				if task.State == "planning" {
					if fixed, ok := rewritePlanMDPathAfterCD(cmd, rig); ok {
						orchestratedPrintf("[gt-agent] rewrote plan.md path after cd: %s\n", fixed)
						cmd = fixed
					}
				}
				if task.State == "implementation" {
					if fixed, ok := rewriteBackendPathAfterCD(cmd, rig); ok {
						orchestratedPrintf("[gt-agent] rewrote backend path after cd: %s\n", fixed)
						cmd = fixed
					}
				}
				if task.State == "implementation" || task.State == "qa_review" {
					if fixed, ok := rewriteUnittestToWorkdir(cmd, rig); ok {
						orchestratedPrintf("[gt-agent] rewrote unittest to run in mayor/rig: %s\n", fixed)
						cmd = fixed
					}
				}
				if task.State == "qa_review" || task.State == "plan_review" || task.State == "planning" || task.State == "implementation" {
					if fixed, ok := rewriteBdListLimit(cmd); ok {
						cmd = fixed
					}
				}
				orchestratedPrintf("[gt-agent] $ %s\n", cmd)
				cmdEnv := orchestratedCommandEnv(townRoot, rig, task.State, os.Environ())
				if needsOrchestratedScriptFile(cmd) {
					orchestratedPrintf("[gt-agent] running multiline/heredoc via temp script\n")
				}
				workDir := orchestratedCommandWorkDir(townRoot, rig, task.State)
				out, cmdErr := runOrchestratedCommand(cmd, workDir, sessionName, cmdEnv)
				if task.State == "design" && cmdErr == nil && isArchitectureMDWriteCommand(cmd) {
					designArchWrittenThisRun = true
				}
				if task.State == "planning" {
					if cmdErr != nil {
						planningHadCmdFailure = true
					}
					if isBeadCreateCommand(cmd) && cmdErr == nil {
						planningBeadCreateOK = true
					}
					if cmdErr == nil && isBeadDeleteCommand(cmd) {
						planningBeadDeleteOK = true
					}
					if cmdErr == nil && isPlanMDWriteCommand(cmd) && planMDMeetsMinSize(townRoot, rig) {
						planningHadCmdFailure = false
					}
				}
				if task.State == "implementation" {
					if cmdErr != nil {
						implementationHadCmdFailure = true
					}
					if isBeadCloseCommand(cmd) && cmdErr == nil {
						implementationBeadCloseOK = true
					}
					if cmdErr == nil && isQATestCommandOK(cmd, taskValidation(task)) {
						implementationHadCmdFailure = false
					}
					if cmdErr == nil && isGitCommitBackendCommand(cmd) {
						implementationHadCmdFailure = false
					}
				}
				if task.State == "plan_review" {
					if cmdErr != nil {
						planReviewHadCmdFailure = true
					}
					if cmdErr == nil && isBdListOpenCommand(cmd) {
						planReviewListOpenOK = true
						planReviewHadCmdFailure = false
					}
					if cmdErr == nil && isQAReadOnlyCommand(cmd) {
						planReviewHadCmdFailure = false
					}
					if cmdErr == nil && isBeadDeleteCommand(cmd) {
						planReviewHadCmdFailure = false
						planReviewDidDelete = true
					}
				}
				if task.State == "qa_review" {
					if cmdErr != nil {
						qaHadCmdFailure = true
					}
					if cmdErr == nil && isBdListClosedCommand(cmd) {
						qaBdListClosedOK = true
					}
					if cmdErr == nil && isQATestCommandOK(cmd, taskValidation(task)) {
						qaUnittestOK = true
						qaHadCmdFailure = false
					}
					if cmdErr == nil && isQAReadOnlyCommand(cmd) {
						qaHadCmdFailure = false
					}
				}
				if cmdErr != nil {
					orchestratedFprintfStderr( "[gt-agent] command failed: %v\n%s\n", cmdErr, string(out))
					combined.WriteString(fmt.Sprintf("Command: %s\nError: %v\nOutput: %s\n\n", cmd, cmdErr, string(out)))
				} else {
					orchestratedPrintf("[gt-agent] output:\n%s\n", string(out))
					combined.WriteString(string(out))
				}
			}
			feedback := combined.String()
			recordAttemptFeedback(feedback)
			feedback += "\n\nCommands executed. If the step is complete, reply with JSON only (no CMD lines): {\"outcome\":\"...\",\"summary\":\"...\"}"
			if turn == maxTurns {
				feedback += " Use an allowed outcome."
			}
			// Same turn may include CMD lines and JSON; accept success when artifacts are ready.
			if o, s, ok := parseOrchestratedResult(response, task.AllowedOutcomes); ok {
				if vErr := validateOutcomeSummaryBeadIDs(townRoot, rig, task.State, o, s); vErr != nil {
					orchestratedPrintf("[gt-agent] summary validation failed: %v\n", vErr)
					recordAttemptFeedback("Validation failed: " + vErr.Error() + "\n")
				} else if vErr := validateOrchestratedArtifacts(task, townRoot, rig, o, designArchWrittenThisRun, planningHadCmdFailure, planningBeadCreateOK, planningBeadDeleteOK, planReviewHadCmdFailure, planReviewListOpenOK, planReviewDidDelete, implementationHadCmdFailure, implementationBeadCloseOK, qaHadCmdFailure, qaBdListClosedOK, qaUnittestOK); vErr != nil {
					orchestratedPrintf("[gt-agent] artifact validation failed: %v\n", vErr)
					recordAttemptFeedback("Validation failed: " + vErr.Error() + "\n")
				} else {
					return o, s, lastAttemptFeedback.String(), nil
				}
			}
			if o, s, ok := orchestratedArtifactAutoOutcome(task, townRoot, rig, designArchWrittenThisRun, planningHadCmdFailure, planningBeadCreateOK, planningBeadDeleteOK); ok {
				return o, s, lastAttemptFeedback.String(), nil
			}
			messages = append(messages, llm.Message{Role: "user", Content: feedback})
			continue
		}

		// Outcome is only accepted on a turn with no CMD lines (after work is done).
		if o, s, ok := parseOrchestratedResult(response, task.AllowedOutcomes); ok {
			if vErr := validateOutcomeSummaryBeadIDs(townRoot, rig, task.State, o, s); vErr != nil {
				orchestratedPrintf("[gt-agent] summary validation failed: %v\n", vErr)
				msg := "Validation failed: " + vErr.Error() + ". Run `bd list` and copy bead IDs exactly into the summary."
				recordAttemptFeedback(msg + "\n")
				messages = append(messages, llm.Message{Role: "user", Content: msg})
				continue
			}
			if vErr := validateOrchestratedArtifacts(task, townRoot, rig, o, designArchWrittenThisRun, planningHadCmdFailure, planningBeadCreateOK, planningBeadDeleteOK, planReviewHadCmdFailure, planReviewListOpenOK, planReviewDidDelete, implementationHadCmdFailure, implementationBeadCloseOK, qaHadCmdFailure, qaBdListClosedOK, qaUnittestOK); vErr != nil {
				orchestratedPrintf("[gt-agent] artifact validation failed: %v\n", vErr)
				hint := "Use CMD: with a heredoc to write files, then send JSON outcome."
				if task.State == "design" {
					hint = "Write architecture.md with a heredoc CMD in this session (stale files from prior runs do not count). Read SPEC with head -n 60."
				}
				if task.State == "planning" {
					work := rigMayorRigPath(rig)
					hint = fmt.Sprintf("Repair beads: `bd delete %s --force` for duplicates, `bd create` only for missing paths, rewrite plan.md if needed — then JSON success. Work from %s with BEADS_DIR=$GT_ROOT/%s/.beads. No python/git/backend code.", beadIDExample(townRoot, rig), work, rig)
				}
				if task.State == "plan_review" {
					work := rigMayorRigPath(rig)
					hint = fmt.Sprintf("Run `bd list --status=open` from %s with BEADS_DIR set; compare titles to architecture required_files. Use outcome failure to send the Planner back to fix duplicates or missing paths.", work)
				}
				if task.State == "implementation" {
					hint = fmt.Sprintf("Use bare `bd` from %s with BEADS_DIR=$GT_ROOT/%s/.beads: fix files QA named, `python3 -m pip install -r requirements.txt` if needed, run %s, `bd close` a bead, then success. No shell text in .py files.",
						rigMayorRigPath(rig), rig, taskValidation(task).UnittestCommandHint())
				}
				if task.State == "qa_review" {
					v := taskValidation(task)
					hint = "Run real CMD: lines (not markdown fences): bd list --status=closed, head SPEC.md, " + v.UnittestCommandHint() + " from " + rigMayorRigPath(rig) + ". No /workspace paths. Then JSON only."
				}
				msg := "Validation failed: " + vErr.Error() + ". " + hint
				recordAttemptFeedback(msg + "\n")
				messages = append(messages, llm.Message{Role: "user", Content: msg})
				continue
			}
			return o, s, lastAttemptFeedback.String(), nil
		}

		hint := "Use CMD: lines to run shell commands (heredoc for multi-line files). When done, reply with JSON only: {\"outcome\":\"...\",\"summary\":\"...\"}"
		recordAttemptFeedback(hint + "\n")
		messages = append(messages, llm.Message{Role: "user", Content: hint})
	}

	return "fail", "", lastAttemptFeedback.String(), fmt.Errorf("no structured outcome after %d turns", maxTurns)
}

func isOrchestratedFailureOutcome(outcome string) bool {
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case "fail", "failure":
		return true
	default:
		return false
	}
}

func truncateOrchestratedFeedback(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	return "...(truncated)\n" + s[len(s)-max:]
}

// formatWorkflowReworkBlock returns cross-step failure context (e.g. QA plan_review → planner).
func formatWorkflowReworkBlock(task *orchestrator.Task, rig string) string {
	if task == nil || task.PendingRework == nil {
		return ""
	}
	r := task.PendingRework
	if r.FromState == task.State {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Prior step failed — fix before completing this step\n")
	b.WriteString(fmt.Sprintf("- Failed step: %s (workflow %s)\n", r.FromState, task.WorkflowID))
	b.WriteString(fmt.Sprintf("- Your step now: %s\n", task.State))
	if r.AgentID != "" {
		b.WriteString(fmt.Sprintf("- Reported by: %s\n", r.AgentID))
	}
	b.WriteString(fmt.Sprintf("- Outcome: %s\n", r.Outcome))
	if r.Summary != "" {
		b.WriteString(fmt.Sprintf("- Summary: %s\n", r.Summary))
	}
	if r.Feedback != "" {
		b.WriteString("\n### Details from the failed review\n")
		b.WriteString(r.Feedback)
		b.WriteString("\n")
	}
	if orchestrator.PlanReviewSummarySaysPlanOK(r.Summary) {
		b.WriteString("\nFix only what the QA summary names (beads/paths). plan.md size was already accepted.\n")
	} else {
		b.WriteString("\nAddress the issues above. Use bead IDs and paths from command output — do not invent IDs.\n")
	}
	v := taskValidation(task)
	b.WriteString(workflowReworkHints(r.FromState, task.State, rig, r.Summary, v))
	b.WriteString(orchestratedRetryHintsForState(task.State, rig, v))
	return b.String()
}

func workflowReworkHints(fromState, toState, rig, summary string, v orchestrator.WorkflowValidation) string {
	if fromState == "plan_review" && toState == "planning" {
		worktree := "<rig>/mayor/rig"
		if rig != "" {
			worktree = rig + "/mayor/rig"
		}
		if orchestrator.PlanReviewSummarySaysPlanOK(summary) {
			return fmt.Sprintf(`
### Plan review failed — repair beads (plan.md is OK)
1. Read the QA **summary** above — fix duplicate or missing implementation beads only.
2. `+"`"+`CMD: export BEADS_DIR=$GT_ROOT/%s/.beads && cd %s && bd list --status=open --flat --limit=0`+"`"+`
3. Delete duplicate beads: `+"`"+`bd delete <id-from-bd-list> --force`+"`"+` (only IDs from bd list output).
4. Create missing beads with `+"`"+`bd create`+"`"+` — one per required path (implement-prefix in title).
5. Do **not** pad or rewrite `+"`"+`plan.md`+"`"+` unless the summary says it is too small.
`, rig, worktree)
		}
		return fmt.Sprintf(`
### Plan review failed — repair beads and plan.md
1. Read the QA **summary** and details above (duplicate paths, missing files, weak plan.md).
2. `+"`"+`CMD: export BEADS_DIR=$GT_ROOT/%s/.beads && cd %s && bd list --status=open --flat --limit=0`+"`"+`
3. Delete duplicate bad beads: `+"`"+`bd delete <id-from-bd-list> --force`+"`"+` (only IDs from bd list).
4. Create missing beads with `+"`"+`bd create`+"`"+` — one per required path in architecture (implement-prefix in title).
5. Rewrite `+"`"+`plan.md`+"`"+` (≥ min size) listing real bead IDs from bd output. Do not invent IDs.
`, rig, worktree)
	}
	if fromState != "qa_review" || toState != "implementation" {
		return ""
	}
	worktree := "<rig>/mayor/rig"
	if rig != "" {
		worktree = rig + "/mayor/rig"
	}
	layout := v.LayoutRootDir()
	prefix := strings.TrimSpace(v.BeadTitleContains)
	if prefix == "" {
		prefix = "implement prefix from profile"
	}
	return fmt.Sprintf(`
### QA sent you back — do this first
1. Fix the **specific** issues in the QA summary and command output (paths under %s/, tests, stubs).
2. `+"`"+`CMD: bash -lc 'cd %s && bd list --status=open'`+"`"+` — pick a bead whose title contains %q.
3. If **no** open implement beads: `+"`"+`bd list --status=closed`+"`"+`, find closed implement beads, reopen one with `+"`"+`bd update <id-from-bd-list> --status=open`+"`"+`, then fix code and `+"`"+`bd close <id-from-bd-list>`+"`"+`.
4. **Never** invent bead IDs — copy only from bd list output for this rig.
5. Use `+"`"+`cat > path <<'EOF'`+"`"+` heredocs (line with only EOF). Do not nest `+"`"+`bash -lc '...<<'EOF''`+"`"+`.
`, layout, worktree, prefix)
}

// formatOrchestratedRetryBlock returns prior-attempt context for the next LLM session.
func formatOrchestratedRetryBlock(prior *OrchestratedRetry, task *orchestrator.Task, rig string) string {
	if prior == nil || task == nil {
		return ""
	}
	if prior.WorkflowID != task.WorkflowID || prior.State != task.State {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Previous attempt on this step failed\n")
	b.WriteString(fmt.Sprintf("- Workflow: %s state: %s\n", prior.WorkflowID, prior.State))
	b.WriteString(fmt.Sprintf("- Outcome: %s\n", prior.Outcome))
	b.WriteString(fmt.Sprintf("- Summary: %s\n", prior.Summary))
	if prior.Feedback != "" {
		b.WriteString("\n### Command output from that attempt\n")
		b.WriteString(prior.Feedback)
		b.WriteString("\n")
	}
	b.WriteString("\nFix the issues above. Use bead IDs and paths from command output — do not invent IDs.\n")
	b.WriteString(orchestratedRetryHintsForState(task.State, rig, taskValidation(task)))
	return b.String()
}

// orchestratedRetryHintsForState returns step-specific guidance after a failed attempt.
func orchestratedRetryHintsForState(state, rig string, v orchestrator.WorkflowValidation) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "kickoff":
		return "One CMD: per line. Confirm the rig is registered and SPEC.md exists before reporting success.\n"
	case "design":
		return "One CMD: per line. Read SPEC with head; write architecture.md via a single-quoted heredoc (≥200 bytes). " +
			"Prose may mention python3 or backend/ paths — that is fine inside the document.\n"
	case "planning":
		worktree := "<rig>/mayor/rig"
		if rig != "" {
			worktree = rig + "/mayor/rig"
		}
		return fmt.Sprintf("One CMD: per line. After `cd %s`, write plan.md with a relative path (not %s/plan.md). "+
			"Heredoc must end with a line containing only EOF. Verify with wc -c from town root.\n",
			worktree, worktree)
	case "implementation":
		layout := v.LayoutRootDir()
		return fmt.Sprintf("One CMD: per line. Run `bd list` first; use only bead IDs from that output (rig prefix from bd — never invent IDs). "+
			"Create files under %s/ per bead titles and profile required_files. Heredoc: `cat > path <<'EOF'` then EOF alone on its own line.\n", layout)
	case "qa_review":
		return "One CMD: per line. Review closed beads against SPEC and architecture; use allowed QA outcomes only.\n"
	default:
		return "One CMD: per line.\n"
	}
}

// updateOrchestratedRetryAfterComplete clears stale retry when the workflow left this step.
// Cross-step failures (e.g. plan_review → planning) must not leave QA retry context that
// replays on the next plan_review after the planner fixes beads.
func updateOrchestratedRetryAfterComplete(state *AgentState, task *orchestrator.Task, outcome, summary, attemptLog, nextState string) {
	if state == nil || task == nil {
		return
	}
	if nextState != "" && nextState != task.State {
		if state.OrchestratedRetry != nil &&
			state.OrchestratedRetry.WorkflowID == task.WorkflowID &&
			state.OrchestratedRetry.State == task.State {
			state.OrchestratedRetry = nil
		}
		return
	}
	updateOrchestratedRetry(state, task, outcome, summary, attemptLog)
}

// updateOrchestratedRetry stores failure context for the next fetch_task or clears it on success.
func updateOrchestratedRetry(state *AgentState, task *orchestrator.Task, outcome, summary, attemptLog string) {
	if state == nil || task == nil {
		return
	}
	if !isOrchestratedFailureOutcome(outcome) {
		if state.OrchestratedRetry != nil &&
			state.OrchestratedRetry.WorkflowID == task.WorkflowID &&
			state.OrchestratedRetry.State == task.State {
			state.OrchestratedRetry = nil
		}
		return
	}
	feedback := attemptLog
	if feedback == "" {
		feedback = summary
	}
	state.OrchestratedRetry = &OrchestratedRetry{
		WorkflowID: task.WorkflowID,
		TemplateID: task.TemplateID,
		State:      task.State,
		Outcome:    outcome,
		Summary:    summary,
		Feedback:   truncateOrchestratedFeedback(feedback, maxOrchestratedRetryFeedback),
		At:         time.Now(),
	}
}

// outcomeJSONTailRE strips outcome JSON glued onto the end of a CMD line.
var outcomeJSONTailRE = regexp.MustCompile(`(?i)\s*\{[\s]*"outcome"[\s\S]*$`)

var markdownFencedCMDRE = regexp.MustCompile("(?im)^```\\s*cmd:\\s*")

// stripOutcomeLines removes JSON/outcome lines so they are not fed into shell scripts.
func stripOutcomeLinesForCmdParse(response string) string {
	lines := strings.Split(response, "\n")
	var kept []string
	inOutcomeJSON := false
	braceDepth := 0
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" {
			if !inOutcomeJSON {
				kept = append(kept, line)
			}
			continue
		}
		if !inOutcomeJSON && strings.HasPrefix(t, "{") && strings.Contains(strings.ToLower(t), "outcome") {
			inOutcomeJSON = true
			braceDepth = strings.Count(t, "{") - strings.Count(t, "}")
			if braceDepth <= 0 {
				inOutcomeJSON = false
			}
			continue
		}
		if inOutcomeJSON {
			braceDepth += strings.Count(t, "{") - strings.Count(t, "}")
			if braceDepth <= 0 {
				inOutcomeJSON = false
			}
			continue
		}
		if trimmed := outcomeJSONTailRE.ReplaceAllString(line, ""); trimmed != line {
			line = strings.TrimRight(trimmed, " \t")
			t = strings.TrimSpace(line)
			if t == "" {
				continue
			}
		}
		if isOrchestratedOutcomeLine(t) {
			continue
		}
		if strings.HasPrefix(strings.ToUpper(t), "OUTCOME:") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// isOrchestratedOutcomeLine reports JSON field lines that must not be executed as shell.
func isOrchestratedOutcomeLine(t string) bool {
	if strings.Contains(t, "CMD:") {
		return false
	}
	lower := strings.ToLower(t)
	if strings.HasPrefix(lower, "outcome:") || strings.HasPrefix(lower, "summary:") {
		return true
	}
	if strings.HasPrefix(t, "{") || strings.HasPrefix(t, "}") || t == "}," {
		return true
	}
	if strings.HasPrefix(lower, `"outcome"`) || strings.HasPrefix(lower, `'outcome'`) {
		return true
	}
	if strings.HasPrefix(lower, `"summary"`) {
		return true
	}
	return false
}

// parseOrchestratedCommands extracts CMD blocks without treating JSON or outcome lines as shell.
func parseOrchestratedCommands(response string) []string {
	filtered := stripOutcomeLinesForCmdParse(response)
	filtered = stripModelToolArtifacts(filtered)
	filtered = normalizeMarkdownFencedCMD(filtered)
	// Un-glue EOF'CMD: and similar before line-oriented parsing (polecat heredoc bursts).
	filtered = normalizeGluedCMDMarkers(filtered)
	cmds, _, _ := parseLLMResponse(filtered)
	return cmds
}

// stripModelToolArtifacts removes [TOOL_CALLS] markers and hallucinated shell output
// the model pastes after CMD lines (common with local LLMs in QA step).
func stripModelToolArtifacts(response string) string {
	var kept []string
	for _, line := range strings.Split(response, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			kept = append(kept, line)
			continue
		}
		upper := strings.ToUpper(t)
		if strings.Contains(upper, "[TOOL_CALLS]") {
			continue
		}
		if looksLikeHallucinatedShellOutput(t) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// normalizeMarkdownFencedCMD converts ```CMD: / ```cmd: blocks to plain CMD: lines.
func normalizeMarkdownFencedCMD(response string) string {
	response = markdownFencedCMDRE.ReplaceAllString(response, "CMD: ")
	response = strings.ReplaceAll(response, "```[TOOL_CALLS]", "\n")
	response = strings.ReplaceAll(response, "[TOOL_CALLS]", "")
	response = strings.ReplaceAll(response, "```json", "")
	response = strings.ReplaceAll(response, "```JSON", "")
	response = strings.ReplaceAll(response, "```", "")
	return response
}

// orchestratedTaskRig returns the workflow rig (from fetch_task) when the agent has no GT_RIG.
func orchestratedTaskRig(task *orchestrator.Task, agentRig string) string {
	if agentRig != "" {
		return strings.TrimSpace(agentRig)
	}
	if task != nil && strings.TrimSpace(task.Rig) != "" {
		return strings.TrimSpace(task.Rig)
	}
	return ""
}

// resolveOrchestratedRigName returns the real rig directory name for shell paths.
// Never use the literal string "RIG" in commands — that was a placeholder bug when rig was empty.
func resolveOrchestratedRigName(townRoot, rig string) string {
	rig = strings.TrimSpace(rig)
	if rig != "" {
		return rig
	}
	if r := strings.TrimSpace(os.Getenv("GT_RIG")); r != "" {
		return r
	}
	return discoverOrchestratedRigName(townRoot)
}

// discoverOrchestratedRigName reads the first rig key from rigs.json (mayor/ or town root).
func discoverOrchestratedRigName(townRoot string) string {
	if townRoot == "" {
		return ""
	}
	for _, path := range []string{
		filepath.Join(townRoot, "mayor", "rigs.json"),
		filepath.Join(townRoot, "rigs.json"),
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var cfg struct {
			Rigs map[string]json.RawMessage `json:"rigs"`
		}
		if err := json.Unmarshal(data, &cfg); err != nil || len(cfg.Rigs) == 0 {
			continue
		}
		keys := make([]string, 0, len(cfg.Rigs))
		for k := range cfg.Rigs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return keys[0]
	}
	return ""
}

// rewriteOrchestratedRigPlaceholders fixes commands that copied the old "RIG/mayor/rig"
// placeholder from error hints when the workflow rig variable was empty.
func rewriteOrchestratedRigPlaceholders(cmd, rig string) (string, bool) {
	rig = strings.TrimSpace(rig)
	if rig == "" {
		return cmd, false
	}
	work := rig + "/mayor/rig"
	out := cmd
	changed := false
	replacements := []struct{ from, to string }{
		{"RIG/mayor/rig", work},
		{"rig/mayor/rig", work}, // empty {{rig}} substitution
		{"$GT_ROOT/RIG/.beads", "$GT_ROOT/" + rig + "/.beads"},
		{"GT_ROOT/RIG/.beads", "GT_ROOT/" + rig + "/.beads"},
		{"cd RIG/mayor/rig", "cd " + work},
		{"cd RIG/", "cd " + rig + "/"},
		{"&& cd RIG/mayor/rig", "&& cd " + work},
	}
	for _, r := range replacements {
		if strings.Contains(out, r.from) {
			out = strings.ReplaceAll(out, r.from, r.to)
			changed = true
		}
	}
	return out, changed
}

func rigMayorRigDir(townRoot, rig string) string {
	if rig == "" {
		return filepath.Join(townRoot, "mayor", "rig")
	}
	return filepath.Join(townRoot, rig, "mayor", "rig")
}

func isArchitectureMDHeredoc(cmd string) bool {
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "architecture.md") && strings.Contains(lower, "<<")
}

// isArchitectureMDWriteCommand reports shell commands that create/overwrite architecture.md.
func isArchitectureMDWriteCommand(cmd string) bool {
	if isArchitectureMDHeredoc(cmd) {
		return true
	}
	lower := strings.ToLower(cmd)
	if !strings.Contains(lower, "architecture.md") {
		return false
	}
	return strings.Contains(lower, ">") || strings.Contains(lower, "tee ") ||
		strings.Contains(lower, "cp ") || strings.Contains(lower, "mv ")
}

func isPlanMDHeredoc(cmd string) bool {
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "plan.md") && strings.Contains(lower, "<<")
}

func isPlanMDWriteCommand(cmd string) bool {
	if isPlanMDHeredoc(cmd) {
		return true
	}
	lower := strings.ToLower(cmd)
	if !strings.Contains(lower, "plan.md") {
		return false
	}
	return strings.Contains(lower, ">") || strings.Contains(lower, "tee ")
}

func planMDMeetsMinSize(townRoot, rig string) bool {
	path := filepath.Join(rigMayorRigDir(townRoot, rig), "plan.md")
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Size() >= orchestrator.DefaultWorkflowValidation().MinPlanBytes
}

// rewriteBackendPathAfterCD fixes polecat writing rig/mayor/rig/backend/... after cd into worktree.
func rewriteBackendPathAfterCD(cmd, rig string) (string, bool) {
	rigName := strings.TrimSpace(rig)
	if rigName == "" {
		return cmd, false
	}
	mayorRig := rigName + "/mayor/rig"
	lower := strings.ToLower(cmd)
	if !strings.Contains(lower, "backend/") || !strings.Contains(lower, "cd ") {
		return cmd, false
	}
	if !strings.Contains(lower, strings.ToLower(mayorRig)) {
		return cmd, false
	}
	wrong := mayorRig + "/backend/"
	if !strings.Contains(cmd, wrong) {
		return cmd, false
	}
	return strings.ReplaceAll(cmd, wrong, "backend/"), true
}

// rewritePlanMDPathAfterCD fixes a common planner mistake: after `cd rig/mayor/rig`,
// the model still writes to `rig/mayor/rig/plan.md` (missing from that cwd).
func rewritePlanMDPathAfterCD(cmd, rig string) (string, bool) {
	rigName := strings.TrimSpace(rig)
	if rigName == "" {
		return cmd, false
	}
	mayorRig := rigName + "/mayor/rig"
	lower := strings.ToLower(cmd)
	if !strings.Contains(lower, "plan.md") || !strings.Contains(lower, "cd ") {
		return cmd, false
	}
	if !strings.Contains(lower, strings.ToLower(mayorRig)) {
		return cmd, false
	}
	wrong := mayorRig + "/plan.md"
	if !strings.Contains(cmd, wrong) {
		return cmd, false
	}
	return strings.ReplaceAll(cmd, wrong, "plan.md"), true
}

// designCommandShellPortion returns the shell preamble before a heredoc delimiter (<<).
// Architecture.md bodies often mention python3, gt bd, etc. in prose; those must not
// trigger design-step side-effect checks.
func designCommandShellPortion(cmd string) string {
	lower := strings.ToLower(cmd)
	idx := strings.Index(lower, "<<")
	if idx < 0 {
		return cmd
	}
	return strings.TrimSpace(cmd[:idx])
}

func validateDesignShellSideEffects(lower string) error {
	gitCmd := strings.Contains(lower, "git") &&
		(strings.Contains(lower, " commit") || strings.Contains(lower, " push") || strings.Contains(lower, " add"))
	forbidden := []struct {
		cond bool
		msg  string
	}{
		{gitCmd, "must not run git add/commit/push in design step"},
		{strings.Contains(lower, "python3"), "must not run python in design step"},
		{strings.Contains(lower, "pip install"), "must not install packages in design step"},
		{strings.Contains(lower, "gt bd"), "must not create beads in design step (planner)"},
		{strings.Contains(lower, "bd add"), "must not create beads in design step (planner)"},
		{strings.Contains(lower, "mkdir"), "must not mkdir in design step"},
	}
	for _, f := range forbidden {
		if f.cond {
			return fmt.Errorf("%s", f.msg)
		}
	}
	return nil
}

// validateDesignCommand blocks architect scope creep before shell execution.
func validateDesignCommand(cmd, rig string) error {
	lower := strings.ToLower(cmd)
	rigSlash := ""
	if rig != "" {
		rigSlash = strings.ToLower(strings.TrimSpace(rig)) + "/"
	}

	if isArchitectureMDHeredoc(cmd) {
		// Mentioning backend/, python3, gt bd, etc. inside architecture.md body is allowed.
		shell := strings.ToLower(designCommandShellPortion(cmd))
		if err := validateDesignShellSideEffects(shell); err != nil {
			return err
		}
		return nil
	}

	if err := validateDesignShellSideEffects(lower); err != nil {
		return err
	}
	if commandWritesBackend(lower) {
		return fmt.Errorf("must not create or modify backend/ (polecat implements code)")
	}

	if strings.Contains(lower, ">") {
		if strings.Contains(lower, "architecture.md") {
			return nil
		}
		if strings.Contains(lower, rigSlash) || strings.Contains(lower, "mayor/rig/") {
			if rig == "" {
				return fmt.Errorf("may only write architecture.md under <rig>/mayor/rig/")
			}
			return fmt.Errorf("may only write architecture.md under %s/mayor/rig/", rig)
		}
	}
	return nil
}

func isPlanningReadOnlyCmd(cmd string) bool {
	lower := strings.ToLower(strings.TrimSpace(cmd))
	if strings.Contains(lower, "<<") {
		return false
	}
	if isBeadCreateCommand(cmd) || isBeadDeleteCommand(cmd) || isBdListOpenCommand(cmd) || strings.Contains(lower, "bd show") {
		return true
	}
	if strings.Contains(lower, ">") && !strings.Contains(lower, "plan.md") {
		return false
	}
	readPrefixes := []string{"head ", "tail ", "cat ", "wc ", "ls ", "stat ", "test ", "grep ", "less ", "more ", "find ", "export ", "cd "}
	for _, p := range readPrefixes {
		if strings.HasPrefix(lower, p) || strings.Contains(lower, " && "+p) {
			return true
		}
	}
	return false
}

func commandWritesBackend(lower string) bool {
	// Bead titles mention backend paths; creating tasks is allowed in planning.
	if strings.Contains(lower, "bd create") || strings.Contains(lower, "bd delete") ||
		strings.Contains(lower, "bd close") || strings.Contains(lower, "bd list") || strings.Contains(lower, "bd show") {
		return false
	}
	// plan.md / architecture.md heredocs describe backend paths in prose; destination is not backend/.
	if (strings.Contains(lower, "plan.md") || strings.Contains(lower, "architecture.md")) &&
		(strings.Contains(lower, "cat >") || strings.Contains(lower, "<<")) {
		return false
	}
	if strings.Contains(lower, "mkdir") && strings.Contains(lower, "backend") {
		return true
	}
	if strings.Contains(lower, "python3") && strings.Contains(lower, "backend") {
		return true
	}
	writePatterns := []string{"> backend/", ">> backend/", "cat >", "tee ", "touch ", "cp ", "mv ", "install "}
	for _, p := range writePatterns {
		if strings.Contains(lower, p) && strings.Contains(lower, "backend/") {
			return true
		}
	}
	return strings.Contains(lower, "/backend/") && strings.Contains(lower, ">")
}

func isBeadCreateCommand(cmd string) bool {
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "bd create") ||
		(strings.Contains(lower, "bd") && strings.Contains(lower, " create"))
}

func isBeadCloseCommand(cmd string) bool {
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "bd close") ||
		(strings.Contains(lower, "bd") && strings.Contains(lower, " close"))
}

func validatePlanningShellSideEffects(lower string) error {
	gitCmd := strings.Contains(lower, "git") &&
		(strings.Contains(lower, " commit") || strings.Contains(lower, " push") || strings.Contains(lower, " add"))
	if isPlanningReadOnlyCmd(lower) {
		if gitCmd {
			return fmt.Errorf("must not run git add/commit/push in planning step")
		}
		return nil
	}
	forbidden := []struct {
		cond bool
		msg  string
	}{
		{commandWritesBackend(lower), "must not create or modify backend/ (polecat implements code)"},
		{strings.Contains(lower, "python3"), "must not run python in planning step"},
		{strings.Contains(lower, "pip install"), "must not install packages in planning step"},
		{gitCmd, "must not run git add/commit/push in planning step"},
		{strings.Contains(lower, "mkdir"), "must not mkdir in planning step"},
	}
	for _, f := range forbidden {
		if f.cond {
			return fmt.Errorf("%s", f.msg)
		}
	}
	return nil
}

// validatePlanningPlanHeredoc allows plan.md bodies that mention backend/ paths in prose.
func validatePlanningPlanHeredoc(lower string) error {
	gitCmd := strings.Contains(lower, "git") &&
		(strings.Contains(lower, " commit") || strings.Contains(lower, " push") || strings.Contains(lower, " add"))
	if gitCmd {
		return fmt.Errorf("must not run git add/commit/push in planning step")
	}
	if strings.Contains(lower, "python3") || strings.Contains(lower, "pip install") {
		return fmt.Errorf("must not run python/pip in planning step")
	}
	if strings.Contains(lower, "> backend/") || strings.Contains(lower, ".py>") {
		return fmt.Errorf("must not write backend source files in planning step")
	}
	return nil
}

// validatePlanningCommand blocks planner scope creep (polecat implements code).
func validatePlanningCommand(cmd, rig string) error {
	lower := strings.ToLower(cmd)
	rigPrefix := strings.TrimSpace(rig)
	rigSlash := ""
	if rigPrefix != "" {
		rigSlash = strings.ToLower(rigPrefix) + "/"
	}

	if isPlanMDHeredoc(cmd) {
		return validatePlanningPlanHeredoc(lower)
	}

	if strings.Contains(lower, "gt bd add") || strings.Contains(lower, "bd add") {
		return fmt.Errorf("use `cd %s/mayor/rig && bd create --type task --title \"...\"` (gt bd is not the bd CLI)", rigMayorRigPath(rigPrefix))
	}

	if isPlanningReadOnlyCmd(cmd) {
		if strings.Contains(lower, "git") && (strings.Contains(lower, " commit") || strings.Contains(lower, " push") || strings.Contains(lower, " add")) {
			return fmt.Errorf("must not run git add/commit/push in planning step")
		}
		return nil
	}

	if err := validatePlanningShellSideEffects(lower); err != nil {
		return err
	}

	if strings.Contains(lower, ">") {
		if strings.Contains(lower, "plan.md") {
			return nil
		}
		if strings.Contains(lower, ".py>") || strings.Contains(lower, "> backend/") {
			return fmt.Errorf("must not write Python or backend files in planning step")
		}
		if rigSlash != "" && strings.Contains(lower, rigSlash) {
			return fmt.Errorf("may only write plan.md under %s/mayor/rig/", rigPrefix)
		}
	}
	return nil
}

// validateImplementationCommand blocks gt bd hallucinations; polecat uses bare bd in rig workdir.
func validateImplementationCommand(cmd, rig string) error {
	lower := strings.ToLower(cmd)
	if strings.Contains(lower, "```") {
		return fmt.Errorf("do not wrap commands in markdown code fences")
	}
	if strings.HasPrefix(strings.TrimSpace(lower), "<<eof") ||
		(strings.Contains(lower, "<<") && !strings.Contains(lower, "cat ")) {
		return fmt.Errorf("use cat <<'EOF' > path for heredocs")
	}
	if strings.Contains(lower, "gt bd ") || strings.HasPrefix(strings.TrimSpace(lower), "gt bd") {
		return fmt.Errorf("use bare `bd` from %s (gt bd is gt bead, not the bd CLI)", rigMayorRigPath(rig))
	}
	for _, bad := range []string{"gt bead claim", "gt bead close", "gt bd claim", "gt bd close", "bd claim", "bd add "} {
		if strings.Contains(lower, bad) {
			return fmt.Errorf("use bd update/close from rig workdir, not %q", bad)
		}
	}
	if strings.Contains(lower, "git push") {
		return fmt.Errorf("do not push to remote during orchestrator implementation (local commits only)")
	}
	if strings.Contains(lower, "git add .") || strings.Contains(lower, "git add -a") ||
		strings.Contains(lower, "git add --all") {
		return fmt.Errorf("do not git add . — stage only files under %s/backend/", rigMayorRigPath(rig))
	}
	for _, artifact := range []string{"/typescript", ".claude/", ".gt-agent", ".runtime/", "bookmarks.txt", "dummy.py", "plan_complete.js"} {
		if strings.Contains(lower, artifact) {
			return fmt.Errorf("do not commit agent artifacts (%s)", artifact)
		}
	}
	return nil
}

// validateOrchestratedArtifacts rejects false-success when required files are missing or empty.
func validateOrchestratedArtifacts(task *orchestrator.Task, townRoot, rig, outcome string, designArchWrittenThisRun, planningHadCmdFailure, planningBeadCreateOK, planningBeadDeleteOK, planReviewHadCmdFailure, planReviewListOpenOK, planReviewDidDelete, implementationHadCmdFailure, implementationBeadCloseOK, qaHadCmdFailure, qaBdListClosedOK, qaUnittestOK bool) error {
	if outcome != "success" && outcome != "task_passed" && outcome != "all_passed" {
		return nil
	}
	v := taskValidation(task)
	switch task.State {
	case "design":
		return validateDesignArtifacts(townRoot, rig, designArchWrittenThisRun, v)
	case "planning":
		return validatePlanningArtifacts(townRoot, rig, planningHadCmdFailure, planningBeadCreateOK, planningBeadDeleteOK, v)
	case "plan_review":
		return validatePlanReviewArtifacts(townRoot, rig, planReviewHadCmdFailure, planReviewListOpenOK, planReviewDidDelete, v)
	case "implementation":
		return validateImplementationArtifacts(townRoot, rig, implementationHadCmdFailure, implementationBeadCloseOK, v)
	case "qa_review":
		return validateQAArtifacts(townRoot, rig, outcome, qaHadCmdFailure, qaBdListClosedOK, qaUnittestOK, v)
	}
	return nil
}

func validatePlanningArtifacts(townRoot, rig string, hadCmdFailure, beadCreateOK, beadDeleteOK bool, v orchestrator.WorkflowValidation) error {
	if hadCmdFailure {
		return fmt.Errorf("planning step had failed commands; fix errors before completing")
	}
	if !beadCreateOK {
		if err := validatePlanningBeadSet(townRoot, rig, v); err != nil {
			if beadDeleteOK {
				return fmt.Errorf("bead set still invalid after bd delete: %w", err)
			}
			return fmt.Errorf("run `bd create` for missing paths or `bd delete` for duplicates in %s, then ensure open beads match architecture: %w", rigMayorRigPath(rig), err)
		}
	}
	path := filepath.Join(rigMayorRigDir(townRoot, rig), "plan.md")
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("plan.md missing at %s", path)
	}
	if info.Size() < v.MinPlanBytes {
		return fmt.Errorf("plan.md too small (%d bytes); need ≥%d", info.Size(), v.MinPlanBytes)
	}
	if rig != "" {
		if err := validateRigOpenBeads(townRoot, rig); err != nil {
			return err
		}
	}
	return nil
}

// orchestratedCommandEnv pins BEADS_DIR to the workflow rig for planning/implementation
// so town-level planner sessions do not write beads into ~/gt/.beads.
func orchestratedCommandEnv(townRoot, rig, taskState string, base []string) []string {
	env := agentenv.EnsurePATH(base)
	if rig == "" || townRoot == "" {
		return env
	}
	switch taskState {
	case "planning", "plan_review", "implementation", "qa_review":
	default:
		return env
	}
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	env = withEnvKey(env, "BEADS_DIR", beadsDir)
	workDir := rigMayorRigDir(townRoot, rig)
	if taskState == "implementation" || taskState == "qa_review" {
		env = prependEnvPath(env, "PYTHONPATH", workDir)
	}
	return env
}

func prependEnvPath(env []string, key, dir string) []string {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			rest := strings.TrimPrefix(e, prefix)
			if rest == "" {
				return withEnvKey(env, key, dir)
			}
			return withEnvKey(env, key, dir+string(os.PathListSeparator)+rest)
		}
	}
	return append(env, prefix+dir)
}

func withEnvKey(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if !strings.HasPrefix(e, prefix) {
			out = append(out, e)
		}
	}
	return append(out, prefix+value)
}

// validateRigOpenBeads ensures planning created tasks in the rig DB, not town hq-* only.
func validateRigOpenBeads(townRoot, rig string) error {
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	if _, err := os.Stat(beadsDir); err != nil {
		return nil
	}
	args := beads.InjectFlatForListJSON([]string{"list", "--status=open", "--json", "--limit=0"})
	cmd := exec.Command("bd", args...)
	cmd.Env = withEnvKey(os.Environ(), "BEADS_DIR", beadsDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("rig beads check (BEADS_DIR=%s): %w: %s", beadsDir, err, strings.TrimSpace(string(out)))
	}
	out = beads.StripStdoutWarnings(out)
	var issues []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &issues); err != nil {
		return fmt.Errorf("parse rig beads list: %w", err)
	}
	for _, issue := range issues {
		id := strings.TrimSpace(issue.ID)
		if id == "" {
			continue
		}
		if !strings.HasPrefix(id, "hq-") {
			return nil
		}
	}
	return fmt.Errorf("no rig-scoped open beads in %s (only town hq-* or empty); planning must use BEADS_DIR=%s", rig, beadsDir)
}

// validatePlanningBeadSet checks open implementation beads against architecture (rework may only bd delete).
func validatePlanningBeadSet(townRoot, rig string, v orchestrator.WorkflowValidation) error {
	open, err := listOpenImplementationBeads(townRoot, rig)
	if err != nil {
		return err
	}
	if len(open) == 0 {
		return fmt.Errorf("no open implementation beads matching %q", v.BeadTitleContains)
	}
	archPath := filepath.Join(rigMayorRigDir(townRoot, rig), "architecture.md")
	if len(v.RequiredFiles) > 0 {
		return orchestrator.ValidatePlanBeads(open, archPath, v)
	}
	pathToIDs := map[string][]string{}
	for _, b := range open {
		p := orchestrator.ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains)
		if p == "" {
			return fmt.Errorf("bead %s title has no file path: %q", b.ID, b.Title)
		}
		pathToIDs[p] = append(pathToIDs[p], b.ID)
	}
	for p, ids := range pathToIDs {
		if len(ids) > 1 {
			return fmt.Errorf("duplicate open beads for %s (%s)", p, strings.Join(ids, ", "))
		}
	}
	return nil
}

func validateImplementationArtifacts(townRoot, rig string, hadCmdFailure, beadCloseOK bool, v orchestrator.WorkflowValidation) error {
	if hadCmdFailure {
		return fmt.Errorf("implementation step had failed commands; fix errors before completing")
	}
	if !beadCloseOK {
		return fmt.Errorf("at least one successful `bd close` in %s is required before success", rigMayorRigPath(rig))
	}
	if err := validateRequiredWorkFiles(townRoot, rig, v); err != nil {
		return err
	}
	rigDir := rigMayorRigDir(townRoot, rig)
	if err := orchestrator.ValidateLayoutPythonSources(rigDir, v); err != nil {
		return fmt.Errorf("invalid Python under %s: %w", v.LayoutRoot, err)
	}
	if err := orchestrator.ValidateWorkNotStubbed(rigDir, v); err != nil {
		return fmt.Errorf("implementation still looks like stubs: %w", err)
	}
	return nil
}

func drainOrchestratedNudges(townRoot, sessionName string) string {
	if townRoot == "" || strings.TrimSpace(sessionName) == "" {
		return ""
	}
	drained, err := nudge.Drain(townRoot, sessionName)
	if err != nil || len(drained) == 0 {
		return ""
	}
	return "## Operator / peer nudge\n\n" + nudge.FormatForInjection(drained)
}

func isUnittestCommand(cmd, unittestModule string) bool {
	lower := strings.ToLower(cmd)
	if !strings.Contains(lower, "unittest") {
		return false
	}
	mod := strings.ToLower(strings.TrimSpace(unittestModule))
	if mod == "" {
		mod = strings.ToLower(orchestrator.DefaultWorkflowValidation().UnittestModule)
	}
	slashMod := strings.ReplaceAll(mod, ".", "/")
	return strings.Contains(lower, mod) || (slashMod != mod && strings.Contains(lower, slashMod))
}

func isGitCommitBackendCommand(cmd string) bool {
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "git") && strings.Contains(lower, "commit") &&
		strings.Contains(lower, "backend")
}

func isQAReadOnlyCommand(cmd string) bool {
	lower := strings.ToLower(strings.TrimSpace(cmd))
	readPrefixes := []string{"head ", "tail ", "cat ", "wc ", "ls ", "stat ", "grep ", "find "}
	for _, p := range readPrefixes {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return strings.Contains(lower, "bd list")
}

func validateQACommand(cmd, rig string, v orchestrator.WorkflowValidation) error {
	lower := strings.ToLower(cmd)
	if strings.Contains(lower, "[tool_calls]") {
		return fmt.Errorf("do not emit [TOOL_CALLS] markers — use CMD: lines only")
	}
	if strings.Contains(lower, "unittest") && (strings.Contains(lower, "| grep") || strings.Contains(lower, "if [")) {
		return fmt.Errorf("run unittest as a single CMD (e.g. cd %s && python3 -m unittest %s -v); do not use pipes or shell if-blocks", rigMayorRigPath(rig), v.UnittestModule)
	}
	tr := strings.ToLower(strings.TrimSpace(v.TestRunner))
	if strings.Contains(lower, "pytest") && (strings.Contains(lower, "| grep") || strings.Contains(lower, "if [")) {
		return fmt.Errorf("run pytest as a single CMD from %s; do not use pipes or shell if-blocks", rigMayorRigPath(rig))
	}
	if strings.Contains(lower, "pytest") && tr != "pytest" && tr != "custom" {
		return fmt.Errorf("pytest not allowed for this workflow test_runner=%q — use %s or update rig workflow profile", v.TestRunner, v.UnittestCommandHint())
	}
	if strings.Contains(lower, "pytest") && !strings.Contains(lower, "cd ") && !strings.Contains(lower, strings.ToLower(rigMayorRigPath(rig))) {
		return fmt.Errorf("pytest must run from under %s (e.g. cd %s && …)", rigMayorRigPath(rig), rigMayorRigPath(rig))
	}
	if strings.Contains(lower, "unittest") && !strings.Contains(lower, "cd ") && !strings.Contains(lower, rigMayorRigPath(rig)) {
		return fmt.Errorf("unittest must run from %s (e.g. cd %s && python3 -m unittest %s -v)", rigMayorRigPath(rig), rigMayorRigPath(rig), v.UnittestModule)
	}
	forbidden := []struct {
		cond bool
		msg  string
	}{
		{strings.Contains(lower, "/workspace"), "do not use /workspace paths — work from $GT_ROOT"},
		{strings.Contains(lower, "flake8"), "do not run flake8 in QA step"},
		{strings.Contains(lower, "pip install"), "do not install packages in QA step"},
		{strings.Contains(lower, "follow-arch"), "do not grep for invented FOLLOW-ARCH markers"},
		{strings.Contains(lower, "arch-deviation"), "do not grep for invented ARCH-DEVIATION markers"},
		{strings.Contains(lower, "spec-not-compliant"), "do not grep for invented SPEC markers"},
		{strings.Contains(lower, "/beads/implementation"), "beads are in the rig .beads DB — use bd list"},
	}
	for _, f := range forbidden {
		if f.cond {
			return fmt.Errorf("%s", f.msg)
		}
	}
	rigSlash := strings.ToLower(rig) + "/"
	if strings.Contains(lower, "bd ") || strings.Contains(lower, "bd\t") {
		if !strings.Contains(lower, rigSlash) && !strings.Contains(lower, "beads_dir") && !strings.Contains(lower, "mayor/rig") {
			return fmt.Errorf("run bd from %s/mayor/rig with BEADS_DIR=$GT_ROOT/%s/.beads", rig, rig)
		}
	}
	return nil
}

func isBdListClosedCommand(cmd string) bool {
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "bd list") && strings.Contains(lower, "closed")
}

func isBdListOpenCommand(cmd string) bool {
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "bd list") && strings.Contains(lower, "open")
}

func isBeadDeleteCommand(cmd string) bool {
	lower := strings.ToLower(strings.TrimSpace(cmd))
	return strings.Contains(lower, "bd delete") || strings.Contains(lower, "bd close")
}

func validatePlanReviewCommand(cmd, rig string) error {
	lower := strings.ToLower(cmd)
	if strings.Contains(lower, "[tool_calls]") {
		return fmt.Errorf("do not emit [TOOL_CALLS] markers — use CMD: lines only")
	}
	if strings.Contains(lower, "bd create") {
		return fmt.Errorf("do not create beads in plan_review — use outcome failure so the Planner repairs in planning")
	}
	if strings.Contains(lower, "unittest") || strings.Contains(lower, "pytest") {
		return fmt.Errorf("no test commands in plan_review — implementation has not started")
	}
	forbidden := []struct {
		cond bool
		msg  string
	}{
		{strings.Contains(lower, "/workspace"), "do not use /workspace paths — work from $GT_ROOT"},
		{strings.Contains(lower, "pip install"), "do not install packages in plan_review"},
	}
	for _, f := range forbidden {
		if f.cond {
			return fmt.Errorf("%s", f.msg)
		}
	}
	rigSlash := strings.ToLower(rig) + "/"
	if strings.Contains(lower, "bd ") || strings.Contains(lower, "bd\t") {
		if !strings.Contains(lower, rigSlash) && !strings.Contains(lower, "beads_dir") && !strings.Contains(lower, "mayor/rig") {
			return fmt.Errorf("run bd from %s/mayor/rig with BEADS_DIR=$GT_ROOT/%s/.beads", rig, rig)
		}
	}
	if isBeadCreateCommand(cmd) {
		return fmt.Errorf("bd create is not allowed in plan_review")
	}
	if err := validatePlanReviewGrep(cmd); err != nil {
		return err
	}
	if isBeadDeleteCommand(cmd) {
		return fmt.Errorf("do not delete or close beads in plan_review — use outcome failure so the Planner repairs in planning")
	}
	if isQAReadOnlyCommand(cmd) || isBdListOpenCommand(cmd) {
		return nil
	}
	if strings.Contains(lower, "bd show") {
		return nil
	}
	return fmt.Errorf("plan_review allows read-only inspection (bd list/show, head, grep on files) — not: %s", cmd)
}

func validatePlanReviewGrep(cmd string) error {
	lower := strings.ToLower(strings.TrimSpace(cmd))
	if !strings.Contains(lower, "grep") {
		return nil
	}
	if strings.Contains(lower, "beads.md") {
		return fmt.Errorf("do not grep beads.md — use bd list --status=open and read bead IDs from that output")
	}
	if strings.Contains(lower, "te-") {
		hasFile := strings.Contains(lower, ".md") || strings.Contains(lower, ".py") ||
			strings.Contains(lower, ".yaml") || strings.Contains(lower, ".yml") ||
			strings.Contains(lower, "architecture") || strings.Contains(lower, "/")
		if !hasFile {
			return fmt.Errorf("do not grep bead IDs (te-xxx) — use bd list and bd show <id> instead")
		}
	}
	return nil
}

func listOpenImplementationBeads(townRoot, rig string) ([]orchestrator.PlanBead, error) {
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	args := beads.InjectFlatForListJSON([]string{"list", "--status=open", "--json", "--limit=0"})
	cmd := exec.Command("bd", args...)
	cmd.Env = withEnvKey(os.Environ(), "BEADS_DIR", beadsDir)
	cmd.Dir = rigMayorRigDir(townRoot, rig)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("bd list open: %w: %s", err, strings.TrimSpace(string(out)))
	}
	out = beads.StripStdoutWarnings(out)
	var rows []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("parse open beads: %w", err)
	}
	result := make([]orchestrator.PlanBead, 0, len(rows))
	for _, r := range rows {
		id := strings.TrimSpace(beads.ExtractIssueID(r.ID))
		if id == "" {
			continue
		}
		result = append(result, orchestrator.PlanBead{ID: id, Title: strings.TrimSpace(r.Title)})
	}
	return result, nil
}

func validatePlanReviewArtifacts(townRoot, rig string, hadCmdFailure, listOpenOK, didDelete bool, v orchestrator.WorkflowValidation) error {
	if didDelete {
		return fmt.Errorf("do not bd delete in plan_review then report success — use outcome failure so the Planner repairs beads and plan.md")
	}
	if hadCmdFailure {
		return fmt.Errorf("plan review step had failed commands; fix errors before completing")
	}
	if !listOpenOK {
		return fmt.Errorf("run `bd list --status=open` from %s before reporting plan review outcome", rigMayorRigPath(rig))
	}
	planPath := filepath.Join(rigMayorRigDir(townRoot, rig), "plan.md")
	info, err := os.Stat(planPath)
	if err != nil {
		return fmt.Errorf("plan.md missing at %s", planPath)
	}
	if info.Size() < v.MinPlanBytes {
		return fmt.Errorf("plan.md too small (%d bytes); need ≥%d", info.Size(), v.MinPlanBytes)
	}
	open, err := listOpenImplementationBeads(townRoot, rig)
	if err != nil {
		return err
	}
	archPath := filepath.Join(rigMayorRigDir(townRoot, rig), "architecture.md")
	if err := orchestrator.ValidatePlanBeads(open, archPath, v); err != nil {
		return fmt.Errorf("plan beads do not match architecture/profile: %w", err)
	}
	return nil
}

func countOpenMatchingBeads(townRoot, rig, titleContains string) (int, error) {
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	cmd := exec.Command("bd", "list", "--status=open", "--limit=0")
	cmd.Env = withEnvKey(os.Environ(), "BEADS_DIR", beadsDir)
	cmd.Dir = rigMayorRigDir(townRoot, rig)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("bd list open: %w: %s", err, strings.TrimSpace(string(out)))
	}
	n := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, titleContains) {
			n++
		}
	}
	return n, nil
}

func beadIDExample(townRoot, rig string) string {
	prefix, err := orchestrator.RigIssuePrefix(townRoot, rig)
	if err != nil || prefix == "" {
		return "<id-from-bd-list>"
	}
	return prefix + "-xxx"
}

func validateOutcomeSummaryBeadIDs(townRoot, rig, taskState, outcome, summary string) error {
	if !isOrchestratedFailureOutcome(outcome) {
		return nil
	}
	if taskState != "qa_review" && taskState != "plan_review" {
		return nil
	}
	known, prefix, err := orchestrator.ListRigBeadIDSet(townRoot, rig)
	if err != nil {
		return nil
	}
	return orchestrator.ValidateSummaryBeadIDs(summary, known, prefix)
}

func validateQAArtifacts(townRoot, rig, outcome string, hadCmdFailure, bdListClosedOK, unittestOK bool, v orchestrator.WorkflowValidation) error {
	if hadCmdFailure {
		return fmt.Errorf("QA step had failed commands; fix errors before completing")
	}
	if !bdListClosedOK {
		return fmt.Errorf("run `bd list --status=closed` from %s before reporting QA outcome", rigMayorRigPath(rig))
	}
	if !unittestOK {
		return fmt.Errorf("run `%s` from %s before reporting QA outcome", v.UnittestCommandHint(), rigMayorRigPath(rig))
	}
	if err := validateRequiredWorkFiles(townRoot, rig, v); err != nil {
		return err
	}
	if err := orchestrator.ValidateWorkNotStubbed(rigMayorRigDir(townRoot, rig), v); err != nil {
		return fmt.Errorf("implementation files look like stubs (QA must use outcome failure): %w", err)
	}
	openImpl, err := countOpenMatchingBeads(townRoot, rig, v.BeadTitleContains)
	if err != nil {
		return err
	}
	switch outcome {
	case "all_passed":
		if openImpl > 0 {
			return fmt.Errorf("cannot use all_passed: %d open beads matching %q remain", openImpl, v.BeadTitleContains)
		}
	case "task_passed":
		if openImpl == 0 {
			return fmt.Errorf("use all_passed when no open beads matching %q remain", v.BeadTitleContains)
		}
	}
	return nil
}

func validateRequiredWorkFiles(townRoot, rig string, v orchestrator.WorkflowValidation) error {
	rigDir := rigMayorRigDir(townRoot, rig)
	for _, rel := range v.RequiredFiles {
		path := filepath.Join(rigDir, filepath.FromSlash(rel))
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("missing %s (implement and commit before success)", path)
		}
		if info.Size() == 0 {
			return fmt.Errorf("%s is empty", path)
		}
	}
	return nil
}

func rigMayorRigPath(rig string) string {
	rig = strings.TrimSpace(rig)
	if rig == "" {
		return "<rig>/mayor/rig"
	}
	return rig + "/mayor/rig"
}

func validateDesignArtifacts(townRoot, rig string, writtenThisRun bool, v orchestrator.WorkflowValidation) error {
	if !writtenThisRun {
		return fmt.Errorf("architecture.md must be written in this design step (heredoc CMD); stale files from prior runs are ignored")
	}
	rigDir := rigMayorRigDir(townRoot, rig)
	archPath := filepath.Join(rigDir, "architecture.md")
	info, err := os.Stat(archPath)
	if err != nil {
		return fmt.Errorf("architecture.md missing at %s", archPath)
	}
	if info.Size() < v.MinArchitectureBytes {
		return fmt.Errorf("architecture.md too small (%d bytes); need ≥%d", info.Size(), v.MinArchitectureBytes)
	}
	// Stale implementation files at mayor/rig root must not block design completion.
	for _, name := range v.ForbiddenRigRootBasenames() {
		if _, err := os.Stat(filepath.Join(rigDir, name)); err == nil {
			return fmt.Errorf("implementation file %q must not exist in mayor/rig/ (only architecture.md)", name)
		}
	}
	return nil
}

// orchestratedArtifactAutoOutcome completes a step when required files exist after CMD work,
// even if the model never sends a clean JSON-only turn (common with small local LLMs).
func orchestratedArtifactAutoOutcome(task *orchestrator.Task, townRoot, rig string, designArchWrittenThisRun, planningHadCmdFailure, planningBeadCreateOK, planningBeadDeleteOK bool) (outcome, summary string, ok bool) {
	var vErr error
	switch task.State {
	case "design":
		vErr = validateDesignArtifacts(townRoot, rig, designArchWrittenThisRun, taskValidation(task))
	case "planning":
		vErr = validateOrchestratedArtifacts(task, townRoot, rig, "success", designArchWrittenThisRun, planningHadCmdFailure, planningBeadCreateOK, planningBeadDeleteOK, false, false, false, false, false, false, false, false)
	default:
		return "", "", false
	}
	if vErr != nil {
		return "", "", false
	}
	o := normalizeOrchestratedOutcome("success", task.AllowedOutcomes)
	if o == "" {
		return "", "", false
	}
	orchestratedPrintf("[gt-agent] auto-completing %s: artifacts satisfied\n", task.State)
	return o, "artifacts validated", true
}

func buildOrchestratedSystemPrompt(task *orchestrator.Task) string {
	var b strings.Builder
	if task.SystemPrompt != "" {
		b.WriteString(task.SystemPrompt)
		b.WriteString("\n\n")
	}
	b.WriteString("## Orchestrator context\n")
	b.WriteString(fmt.Sprintf("- Workflow: %s (%s)\n", task.TemplateID, task.WorkflowID))
	b.WriteString(fmt.Sprintf("- State: %s\n", task.State))
	b.WriteString(fmt.Sprintf("- Role: %s\n", task.Role))
	if len(task.AllowedOutcomes) > 0 {
		b.WriteString(fmt.Sprintf("- Allowed outcomes: %s\n", strings.Join(task.AllowedOutcomes, ", ")))
	}
	b.WriteString("\nComplete **only this step**.\n")
	b.WriteString("1. Run shell work as `CMD: <command>` lines (use a single heredoc CMD for multi-line files).\n")
	b.WriteString("2. After commands succeed, send a **separate** message with JSON only (no CMD lines in that message):\n")
	b.WriteString(`{"outcome":"<one allowed outcome>","summary":"<brief result>"}`)
	b.WriteString("\nDo not put JSON on the same line as CMD. Do not use `cat > file` without a heredoc body.\n")
	return b.String()
}

func buildOrchestratedUserPrompt(task *orchestrator.Task) string {
	if task.TaskPrompt != "" {
		return "Complete this step only:\n\n" + task.TaskPrompt
	}
	return "Complete this step only:\n\n" + task.Instructions
}

func parseOrchestratedResult(response string, allowed []string) (outcome, summary string, ok bool) {
	if r, s, parsed := parseOrchestratedJSON(response); parsed {
		if normalizeOrchestratedOutcome(r, allowed) != "" {
			return normalizeOrchestratedOutcome(r, allowed), s, true
		}
	}
	if o := parseOutcomeLine(response); o != "" {
		if normalizeOrchestratedOutcome(o, allowed) != "" {
			return normalizeOrchestratedOutcome(o, allowed), "", true
		}
	}
	return "", "", false
}

func parseOrchestratedJSON(response string) (outcome, summary string, ok bool) {
	candidates := extractOrchestratedJSONObjects(response)
	if m := jsonBlockRE.FindStringSubmatch(response); len(m) > 1 {
		candidates = append(candidates, strings.TrimSpace(m[1]))
	}
	candidates = append(candidates, strings.TrimSpace(response))
	var last orchestratedTaskResult
	found := false
	for _, raw := range candidates {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		var r orchestratedTaskResult
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			continue
		}
		if r.Outcome != "" {
			last = r
			found = true
		}
	}
	if !found {
		return "", "", false
	}
	return last.Outcome, last.Summary, true
}

// extractOrchestratedJSONObjects finds inline {"outcome":...} objects; last one wins in parseOrchestratedJSON.
func extractOrchestratedJSONObjects(response string) []string {
	var objs []string
	for i := 0; i < len(response); i++ {
		if response[i] != '{' {
			continue
		}
		end, ok := jsonObjectEnd(response, i)
		if !ok {
			continue
		}
		raw := strings.TrimSpace(response[i : end+1])
		var r orchestratedTaskResult
		if err := json.Unmarshal([]byte(raw), &r); err != nil || r.Outcome == "" {
			continue
		}
		objs = append(objs, raw)
	}
	return objs
}

func jsonObjectEnd(s string, start int) (int, bool) {
	if start >= len(s) || s[start] != '{' {
		return 0, false
	}
	depth := 0
	inString := false
	escape := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inString {
			if escape {
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}
	return 0, false
}

func parseOutcomeLine(response string) string {
	for _, line := range strings.Split(response, "\n") {
		line = strings.TrimSpace(line)
		upper := strings.ToUpper(line)
		if strings.HasPrefix(upper, "OUTCOME:") {
			return strings.TrimSpace(line[len("OUTCOME:"):])
		}
	}
	// case-insensitive search in body
	lower := strings.ToLower(response)
	for _, key := range []string{"task_passed", "all_passed", "failure", "success", "fail"} {
		if strings.Contains(lower, "outcome: "+key) {
			return key
		}
	}
	if strings.Contains(response, "OUTCOME: success") {
		return "success"
	}
	if strings.Contains(response, "OUTCOME: fail") {
		return "fail"
	}
	return ""
}

func normalizeOrchestratedOutcome(outcome string, allowed []string) string {
	outcome = strings.TrimSpace(strings.ToLower(outcome))
	if outcome == "" {
		return ""
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, a := range allowed {
		allowedSet[strings.ToLower(a)] = struct{}{}
	}
	if _, ok := allowedSet[outcome]; ok {
		return outcome
	}
	if outcome == "fail" {
		if _, ok := allowedSet["failure"]; ok {
			return "failure"
		}
	}
	if outcome == "failure" {
		if _, ok := allowedSet["fail"]; ok {
			return "fail"
		}
	}
	if len(allowed) == 0 {
		return outcome
	}
	if outcome == "success" {
		if _, ok := allowedSet["success"]; ok {
			return "success"
		}
	}
	return ""
}
