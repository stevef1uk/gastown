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
			if rig != "" && orchestrator.IsRigWorkflowPaused(townRoot, rig) {
				orchestratedPrintf("[gt-agent] workflow paused for rig %s — exiting\n", rig)
				break
			}
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
	runner := newStateRunner(task, townRoot, rig)
	runner.scrubStaleDevServersAtTaskStart()
	defer runner.shutdownStartedServers()
	for _, block := range runner.promptContextBlocks() {
		contextBlocks = append(contextBlocks, block)
	}
	if len(runner.hooks.PromptContext) > 0 {
		orchestratedPrintf("[gt-agent] injecting prompt_context for %s/%s: %v\n", task.WorkflowID, task.State, runner.hooks.PromptContext)
	}
	runner.runPreRun()
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

	maxTurns := runner.maxTurns()
	for turn := 1; turn <= maxTurns; turn++ {
		if rig != "" && orchestrator.IsRigWorkflowPaused(townRoot, rig) {
			return "failure", "workflow paused", lastAttemptFeedback.String(), orchestrator.ErrWorkflowPaused
		}
		orchestratedPrintf("[gt-agent] LLM request (turn %d)...\n", turn)
		response, llmErr := client.CompleteMessages(ctx, messages)
		if llmErr != nil {
			return "fail", "", lastAttemptFeedback.String(), llmErr
		}
		orchestratedPrintf("[gt-agent] LLM response (turn %d):\n%s\n", turn, response)
		messages = append(messages, llm.Message{Role: "assistant", Content: response})

		runner.runPerTurn()

		cmdBlocks := parseOrchestratedCommands(response)
		if len(cmdBlocks) == 0 && responseHasUnterminatedHeredoc(response) {
			msg := "Your reply started a heredoc (e.g. plan.md <<'EOF') but never sent a line with only EOF — the message was cut off, so no command ran.\n\n" +
				"Fix: split across turns — (1) `bd list --status=open`, (2) `cat > plan.md <<'EOF'` with ## Bead map and ### <id>: <full-path> sections (scope + acceptance per file; must meet min_plan_bytes), (3) line with only `EOF`, (4) `wc -c plan.md`, (5) JSON success only."
			if h := runner.failureHint(); h != "" {
				msg += "\n\n" + h
			}
			recordAttemptFeedback(msg + "\n")
			messages = append(messages, llm.Message{Role: "user", Content: msg})
			continue
		}
		if len(cmdBlocks) > 0 {
			var combined strings.Builder
			for _, cmd := range cmdBlocks {
				if strings.Contains(cmd, "CMD:") {
					orchestratedFprintfStderr("[gt-agent] warning: dropping malformed command with embedded CMD:\n")
					continue
				}
				if err := runner.validateCommand(cmd); err != nil {
					orchestratedFprintfStderr("[gt-agent] rejected command: %v\n", err)
					combined.WriteString(fmt.Sprintf("Command REJECTED (%s): %s\nReason: %v\n\n", runner.rejectScope(), cmd, err))
					continue
				}
				cmd = runner.rewriteCommand(cmd)
				runner.repairPipBeforeRun(cmd)
				cmdEnv := runner.commandEnv(os.Environ())
				cmd = runner.rewritePythonCmd(cmd, cmdEnv)
				runner.beforeDevServerCommand(cmd)
				orchestratedPrintf("[gt-agent] $ %s\n", cmd)
				if needsOrchestratedScriptFile(cmd) {
					orchestratedPrintf("[gt-agent] running multiline/heredoc via temp script\n")
				}
				workDir := runner.workDir()
				if isStandaloneHeredocDelimiter(strings.TrimSpace(cmd)) {
					orchestratedPrintf("[gt-agent] skipping stray heredoc delimiter command: %q\n", cmd)
					combined.WriteString(fmt.Sprintf("Command skipped (stray heredoc delimiter): %s\n\n", cmd))
					continue
				}
				out, cmdErr := runOrchestratedCommand(cmd, workDir, sessionName, cmdEnv, runner.hooks.EffectiveCmdTimeoutSeconds())
				if cmdErr != nil && (benignGoCommandError(cmd, cmdErr, out) || (runner.hooks.Artifacts == "planning" && benignPlanningShellNoise(cmd, cmdErr))) {
					orchestratedPrintf("[gt-agent] treating as ok: %v\n", cmdErr)
					combined.WriteString(fmt.Sprintf("Command: %s\n(note: %v — continuing)\nOutput: %s\n\n", cmd, cmdErr, string(out)))
					cmdErr = nil
				}
				runner.afterCommand(cmd, cmdErr, workDir, sessionName, cmdEnv, &combined)
				if cmdErr != nil {
					orchestratedFprintfStderr("[gt-agent] command failed: %v\n%s\n", cmdErr, string(out))
					combined.WriteString(fmt.Sprintf("Command: %s\nError: %v\nOutput: %s\n\n", cmd, cmdErr, string(out)))
					if runner.hooks.AppendGoCompileContext && orchestrator.WorkflowUsesGo(runner.v) {
						appendGoCompileSourceContext(&combined, rigMayorRigDir(townRoot, rig), runner.v.LayoutRoot, cmd, string(out))
					}
				} else {
					feedbackOut := formatSuccessCommandOutput(out)
					orchestratedPrintf("[gt-agent] output: %s\n", strings.TrimSpace(feedbackOut))
					combined.WriteString(feedbackOut)
				}
			}
			feedback := combined.String()
			recordAttemptFeedback(feedback)
			feedback += "\n\nCommands executed. If the step is complete, reply with JSON only (no CMD lines): {\"outcome\":\"...\",\"summary\":\"...\"}"
			if turn == maxTurns {
				feedback += " Use an allowed outcome."
			}
			if o, s, ok := parseOrchestratedResult(response, task.AllowedOutcomes); ok {
				o = normalizeOrchestratedOutcome(o, task.AllowedOutcomes)
				if o == "failure" || o == "fail" {
					orchestratedPrintf("[gt-agent] ignoring failure JSON in same turn as CMD lines; review output then send JSON only\n")
					recordAttemptFeedback("Failure JSON ignored because CMD lines ran this turn. Review command output, then reply with JSON only.\n")
				} else if o != "" {
					if vErr := validateOutcomeForTask(task, townRoot, rig, o, s); vErr != nil {
						orchestratedPrintf("[gt-agent] summary validation failed: %v\n", vErr)
						recordAttemptFeedback("Validation failed: " + vErr.Error() + "\n")
					} else if vErr := runner.validateArtifacts(o); vErr != nil {
						orchestratedPrintf("[gt-agent] artifact validation failed: %v\n", vErr)
						recordAttemptFeedback("Validation failed: " + vErr.Error() + "\n")
					} else {
						return o, s, lastAttemptFeedback.String(), nil
					}
				}
			}
			if o, s, ok := runner.tryAutoOutcome(); ok {
				return o, s, lastAttemptFeedback.String(), nil
			}
			messages = append(messages, llm.Message{Role: "user", Content: feedback})
			continue
		}

		if o, s, ok := parseOrchestratedResult(response, task.AllowedOutcomes); ok {
			o = normalizeOrchestratedOutcome(o, task.AllowedOutcomes)
			if isOrchestratedFailureOutcome(o) && runner.hooks.Artifacts == "planning" {
				if vErr := runner.validateArtifacts("success"); vErr == nil {
					minPlan := orchestrator.EffectiveMinPlanBytes(rigMayorRigDir(townRoot, rig), runner.v)
					orchestratedPrintf("[gt-agent] ignoring planning failure JSON — artifacts already satisfy success (min_plan_bytes=%d)\n", minPlan)
					msg := fmt.Sprintf("Do not report failure: plan.md and beads already meet requirements (≥ %d bytes, %s). Reply with JSON only: {\"outcome\":\"success\",\"summary\":\"plan and beads ready for plan review\"}", minPlan, runner.v.PlanMinSizeHint())
					recordAttemptFeedback(msg + "\n")
					messages = append(messages, llm.Message{Role: "user", Content: msg})
					continue
				}
			}
			if vErr := validateOutcomeForTask(task, townRoot, rig, o, s); vErr != nil {
				orchestratedPrintf("[gt-agent] summary validation failed: %v\n", vErr)
				msg := "Validation failed: " + vErr.Error() + ". Run `bd list` and copy bead IDs exactly into the summary."
				recordAttemptFeedback(msg + "\n")
				messages = append(messages, llm.Message{Role: "user", Content: msg})
				continue
			}
			if vErr := runner.validateArtifacts(o); vErr != nil {
				orchestratedPrintf("[gt-agent] artifact validation failed: %v\n", vErr)
				msg := runner.artifactFailureFeedback(vErr)
				recordAttemptFeedback(msg + "\n")
				messages = append(messages, llm.Message{Role: "user", Content: msg})
				continue
			}
			return o, s, lastAttemptFeedback.String(), nil
		}

		if strings.TrimSpace(response) == "" {
			msg := runner.emptyResponseNudge()
			recordAttemptFeedback(msg + "\n")
			messages = append(messages, llm.Message{Role: "user", Content: msg})
			continue
		}

		hint := "Use CMD: lines to run shell commands (heredoc for multi-line files). When done, reply with JSON only: {\"outcome\":\"...\",\"summary\":\"...\"}"
		if responseHasUnterminatedHeredoc(response) {
			hint = "Heredoc was truncated — shorten plan.md and end with a line containing only EOF, then wc -c."
		}
		recordAttemptFeedback(hint + "\n")
		messages = append(messages, llm.Message{Role: "user", Content: hint})
	}

	if len(runner.hooks.OnTimeout) > 0 {
		for _, allowed := range task.AllowedOutcomes {
			if !strings.EqualFold(allowed, "timeout") {
				continue
			}
			logLine, hookErr := orchestrator.RunOnTimeoutHooks(runner.hooks.OnTimeout, townRoot, rig, runner.v)
			if hookErr != nil {
				orchestratedFprintfStderr("[gt-agent] on_timeout (max turns): %v\n", hookErr)
			} else if logLine != "" {
				orchestratedPrintf("[gt-agent] on_timeout (max turns): %s\n", logLine)
			}
			summary := fmt.Sprintf("%s exhausted %d CMD turns", task.State, maxTurns)
			if logLine != "" {
				summary += "; " + logLine
			}
			return "timeout", summary, lastAttemptFeedback.String(), fmt.Errorf("no structured outcome after %d turns", maxTurns)
		}
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
	s = sanitizeRetryFeedbackForLLM(s)
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	return "...(truncated)\n" + s[len(s)-max:]
}

// htmlRetryDumpRE matches curl false-positives (agent-console Svelte HTML on :8080).
var htmlRetryDumpRE = regexp.MustCompile(`(?is)<!doctype html>[\s\S]*?</html>`)

// sanitizeRetryFeedbackForLLM strips huge HTML verify noise before the next LLM turn.
func sanitizeRetryFeedbackForLLM(s string) string {
	if s == "" {
		return s
	}
	if htmlRetryDumpRE.MatchString(s) {
		s = htmlRetryDumpRE.ReplaceAllString(s, "\n[verify output: unrelated HTML on :8080 — not Link Shelf; use go mod tidy only in project_setup]\n")
	}
	return s
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
	if task.State == "implementation" && (r.Outcome == "failure" || r.Outcome == "timeout") {
		b.WriteString("\nUse **sed -i** or **patch** on internal packages; **cmd/…/main.go may use heredoc** when wiring is broken. Use store/handler APIs from **Dependency packages** — do not invent symbols.\n")
	}
	runner := newStateRunner(task, "", rig)
	runner.v = v
	runner.promptVars["rig"] = rig
	b.WriteString(runner.retryHint())
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
5. **Incremental fixes only** — existing files must use `+"`"+`sed -i`+"`"+` or `+"`"+`patch`+"`"+`, not `+"`"+`cat > path <<'EOF'`+"`"+` full rewrites. Use heredoc only for **new** files.
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
		b.WriteString(sanitizeRetryFeedbackForLLM(prior.Feedback))
		b.WriteString("\n")
	}
	b.WriteString("\nFix the issues above. Use bead IDs and paths from command output — do not invent IDs.\n")
	if task.State == "implementation" {
		b.WriteString("\nUse **sed -i** or **patch** on internal .go files; **cmd/…/main.go may use heredoc** when Source context shows duplicate or stub handlers. Match APIs in **Dependency packages**.\n")
	}
	runner := newStateRunner(task, "", rig)
	b.WriteString(runner.retryHint())
	return b.String()
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

// Matches ```cmd: / ```CMD / ```cmd (LLMs often omit the colon).
var markdownFencedCMDRE = regexp.MustCompile("(?im)^```\\s*cmd:?\\s*")

// stripOutcomeLines removes JSON/outcome lines so they are not fed into shell scripts.
// Heredoc bodies are copied verbatim so Go lines containing only "}" are preserved.
func stripOutcomeLinesForCmdParse(response string) string {
	lines := strings.Split(response, "\n")
	var kept []string
	inOutcomeJSON := false
	braceDepth := 0
	heredocTerm := ""
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if heredocTerm != "" {
			kept = append(kept, line)
			if t == heredocTerm {
				heredocTerm = ""
			}
			continue
		}
		if term := detectHeredocTerm(line); term != "" {
			kept = append(kept, line)
			heredocTerm = term
			continue
		}
		if t == "" {
			if !inOutcomeJSON {
				kept = append(kept, line)
			}
			continue
		}
		if !inOutcomeJSON && (t == "{" || (strings.HasPrefix(t, "{") && strings.Contains(strings.ToLower(t), "outcome"))) {
			inOutcomeJSON = true
			braceDepth = strings.Count(t, "{") - strings.Count(t, "}")
			if braceDepth <= 0 {
				braceDepth = 1
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
	trimmed := strings.TrimSpace(t)
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "outcome:") || strings.HasPrefix(lower, "summary:") {
		return true
	}
	// Do not match bare "{" or "}" — they appear on their own lines in Go heredocs.
	if strings.HasPrefix(trimmed, "{") && strings.Contains(lower, `"outcome"`) {
		return true
	}
	if strings.HasPrefix(trimmed, `"outcome"`) || strings.HasPrefix(trimmed, `"summary"`) {
		return true
	}
	return false
}

// responseHasUnterminatedHeredoc reports whether the model started a heredoc but omitted the closing delimiter line.
func responseHasUnterminatedHeredoc(response string) bool {
	lines := strings.Split(response, "\n")
	var heredocTerm string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if heredocTerm != "" {
			if trimmed == heredocTerm {
				heredocTerm = ""
			}
			continue
		}
		if term := detectHeredocTerm(line); term != "" {
			heredocTerm = term
		}
	}
	return heredocTerm != ""
}

// parseOrchestratedCommands extracts CMD blocks without treating JSON or outcome lines as shell.
func parseOrchestratedCommands(response string) []string {
	filtered := stripOutcomeLinesForCmdParse(response)
	filtered = stripModelToolArtifacts(filtered)
	filtered = normalizeMarkdownFencedCMD(filtered)
	// Un-glue EOF'CMD: and similar before line-oriented parsing (polecat heredoc bursts).
	filtered = normalizeGluedCMDMarkers(filtered)
	cmds, _, _ := parseLLMResponse(filtered)
	return expandGluedOrchestratedCommands(cmds)
}

// expandGluedOrchestratedCommands splits shell lines that embed CMD: markers mid-command.
func expandGluedOrchestratedCommands(cmds []string) []string {
	var out []string
		for _, c := range cmds {
		c = strings.TrimSpace(c)
		if c == "" || isStandaloneHeredocDelimiter(c) {
			continue
		}
		if !strings.Contains(c, "CMD:") {
			out = append(out, c)
			continue
		}
		for _, part := range splitInlineCMDs(c) {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
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
// placeholder from error hints when the workflow rig variable was empty, and rewrites
// hallucinated absolute town paths (e.g. /home/ubuntu/gt) to the real town root.
func rewriteOrchestratedRigPlaceholders(cmd, townRoot, rig string) (string, bool) {
	rig = strings.TrimSpace(rig)
	if rig == "" {
		return cmd, false
	}
	work := rig + "/mayor/rig"
	out := cmd
	changed := false
	if townRoot != "" {
		townRoot = strings.TrimRight(filepath.Clean(townRoot), string(filepath.Separator))
		if alt, ok := rewriteHallucinatedAbsoluteTownRoot(out, townRoot, rig, work); ok {
			out = alt
			changed = true
		}
	}
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

// rewriteHallucinatedAbsoluteTownRoot replaces wrong absolute town paths in agent CMD lines.
func rewriteHallucinatedAbsoluteTownRoot(cmd, townRoot, rig, work string) (string, bool) {
	out := cmd
	changed := false
	// ~/gt/<rig>/... from town cwd — never replace bare "/gt" (breaks ~/gt/ → ~<rig>).
	if strings.Contains(out, "~/gt/") {
		out = strings.ReplaceAll(out, "~/gt/", "")
		changed = true
	}
	if strings.Contains(out, "~$GT_ROOT") {
		out = strings.ReplaceAll(out, "~$GT_ROOT", "$GT_ROOT")
		changed = true
	}
	for _, wrongRoot := range []string{"/home/ubuntu/gt", "/workspace/gt"} {
		if strings.Contains(out, wrongRoot) {
			out = strings.ReplaceAll(out, wrongRoot, townRoot)
			changed = true
		}
	}
	rigWorkAbs := townRoot + string(filepath.Separator) + filepath.FromSlash(work)
	// Leave correct absolute mayor/rig paths alone (e.g. …/mayor/rig/.venv/bin/python3).
	// Replacing them with relative testgt5/mayor/rig breaks venv when cwd is already mayor/rig.
	if strings.Contains(out, rigWorkAbs+"/.venv") {
		out = strings.ReplaceAll(out, rigWorkAbs+"/.venv", ".venv")
		changed = true
	} else if strings.Contains(out, rigWorkAbs) && !strings.Contains(out, "$GT_ROOT") {
		repl := work
		if strings.Contains(out, "$GT_ROOT") {
			repl = "$GT_ROOT/" + work
		}
		out = strings.ReplaceAll(out, rigWorkAbs, repl)
		changed = true
	}
	rigAbs := townRoot + string(filepath.Separator) + rig
	if strings.Contains(out, rigAbs+"/") {
		repl := rig + "/"
		if strings.Contains(out, "$GT_ROOT") {
			repl = "$GT_ROOT/" + rig + "/"
		}
		out = strings.ReplaceAll(out, rigAbs+"/", repl)
		changed = true
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

func isPlanMDSizeCheckCommand(cmd string) bool {
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "wc") && strings.Contains(lower, "plan.md")
}

func planMDMeetsMinSize(townRoot, rig string, v orchestrator.WorkflowValidation) bool {
	rigDir := rigMayorRigDir(townRoot, rig)
	path := filepath.Join(rigDir, "plan.md")
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Size() >= orchestrator.EffectiveMinPlanBytes(rigDir, v)
}

// rewriteBackendPathAfterCD fixes paths like rig/mayor/rig/<layout>/... after cd into mayor/rig.
// Uses profile layout_root when set; otherwise "backend" for legacy Python rigs.
func rewriteBackendPathAfterCD(cmd, rig, layoutRoot string) (string, bool) {
	rigName := strings.TrimSpace(rig)
	layout := strings.Trim(strings.TrimSpace(layoutRoot), "/")
	if layout == "" || layout == "." {
		layout = "backend"
	}
	if rigName == "" {
		return cmd, false
	}
	mayorRig := rigName + "/mayor/rig"
	lower := strings.ToLower(cmd)
	needle := strings.ToLower(layout) + "/"
	if !strings.Contains(lower, needle) || !strings.Contains(lower, "cd ") {
		return cmd, false
	}
	if !strings.Contains(lower, strings.ToLower(mayorRig)) {
		return cmd, false
	}
	wrong := mayorRig + "/" + layout + "/"
	if !strings.Contains(cmd, wrong) {
		return cmd, false
	}
	return strings.ReplaceAll(cmd, wrong, layout+"/"), true
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

// extractBeadCreateTitle returns the title string from a bd create command.
func extractBeadCreateTitle(cmd string) string {
	idx := strings.Index(strings.ToLower(cmd), "bd create")
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(cmd[idx+len("bd create"):])
	if rest == "" {
		return ""
	}
	if title := extractCLIFlagValue(rest, "--title", "-t"); title != "" {
		return title
	}
	return takeShellWord(rest)
}

// extractCLIFlagValue reads --flag value or --flag=value (quoted values may contain spaces).
func extractCLIFlagValue(s string, flags ...string) string {
	lower := strings.ToLower(s)
	for _, flag := range flags {
		fl := strings.ToLower(flag)
		search := 0
		for {
			pos := strings.Index(lower[search:], fl)
			if pos < 0 {
				break
			}
			pos += search
			after := pos + len(flag)
			if after < len(s) && s[after] == '=' {
				return takeShellWord(strings.TrimSpace(s[after+1:]))
			}
			if after >= len(s) {
				break
			}
			if s[after] != ' ' && s[after] != '\t' {
				search = pos + 1
				continue
			}
			val := strings.TrimSpace(s[after:])
			if val != "" {
				return takeShellWord(val)
			}
			break
		}
	}
	return ""
}

// takeShellWord returns the first shell token (handles "quoted strings" with spaces).
func takeShellWord(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	switch s[0] {
	case '"':
		var b strings.Builder
		for i := 1; i < len(s); i++ {
			if s[i] == '\\' && i+1 < len(s) {
				b.WriteByte(s[i+1])
				i++
				continue
			}
			if s[i] == '"' {
				return b.String()
			}
			b.WriteByte(s[i])
		}
		return strings.Trim(s, `"`)
	case '\'':
		if end := strings.IndexByte(s[1:], '\''); end >= 0 {
			return s[1 : 1+end]
		}
		return strings.Trim(s, `'`)
	default:
		if sp := strings.IndexAny(s, " \t"); sp >= 0 {
			return s[:sp]
		}
		return s
	}
}

func isBeadCloseCommand(cmd string) bool {
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "bd close") ||
		(strings.Contains(lower, "bd") && strings.Contains(lower, " close"))
}

func isBeadUpdateInProgressCommand(cmd string) bool {
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "bd update") && strings.Contains(lower, "in_progress")
}

func extractBeadIDFromBdUpdate(cmd string) string {
	lower := strings.ToLower(cmd)
	idx := strings.Index(lower, "bd update")
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(cmd[idx+len("bd update"):])
	if rest == "" {
		return ""
	}
	id := strings.Fields(rest)[0]
	return strings.Trim(id, `"'`)
}

func extractBeadIDFromBdClose(cmd string) string {
	lower := strings.ToLower(cmd)
	idx := strings.Index(lower, "bd close")
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(cmd[idx+len("bd close"):])
	if rest == "" {
		return ""
	}
	id := strings.Fields(rest)[0]
	return strings.Trim(id, `"'`)
}

func validateImplementationCommandWithState(cmd, townRoot, rig, activeBead string, v orchestrator.WorkflowValidation, verifyOK bool) error {
	if err := validateImplementationCommand(cmd, rig); err != nil {
		return err
	}
	mayorDir := rigMayorRigDir(townRoot, rig)
	if err := validateGoImplementationCommand(cmd, townRoot, rig, mayorDir, activeBead, v, verifyOK); err != nil {
		return err
	}
	if err := validatePythonImplementationCommand(cmd, townRoot, rig, activeBead, v, verifyOK); err != nil {
		return err
	}
	if err := validateCustomImplementationCommand(cmd, townRoot, rig, activeBead, v, verifyOK); err != nil {
		return err
	}
	if err := validateImplementationBeadFileWrite(cmd, townRoot, rig, activeBead, v); err != nil {
		return err
	}
	if activeBead == "" || !isBeadUpdateInProgressCommand(cmd) {
		return nil
	}
	if id := extractBeadIDFromBdUpdate(cmd); id != "" && id != activeBead {
		return fmt.Errorf("only one implement bead may be in_progress at a time (active: %s)", activeBead)
	}
	return nil
}

// validateImplementationBeadFileWrite rejects heredoc/touch writes to paths outside the active or next implement bead.
func validateImplementationBeadFileWrite(cmd, townRoot, rig, activeBead string, v orchestrator.WorkflowValidation) error {
	if reason := orchestrator.RejectFullFileHeredocReason(cmd, townRoot, rig, activeBead, v); reason != "" {
		return fmt.Errorf("%s", reason)
	}
	written := orchestrator.ExtractImplementWritePathFromCmd(cmd, v.LayoutRoot)
	if written == "" {
		return nil
	}
	allowedID := strings.TrimSpace(activeBead)
	if allowedID == "" {
		next, err := orchestrator.NextOpenImplementBead(townRoot, rig, v)
		if err != nil || next == nil {
			return nil
		}
		allowedID = next.ID
	}
	allowedPath := orchestrator.ImplementBeadPathForID(townRoot, rig, allowedID, v)
	if closedOnly, err := orchestrator.ImplementPathHasOnlyClosedBeads(townRoot, rig, written, v); err == nil && closedOnly {
		if allowedPath != "" {
			return fmt.Errorf("do not overwrite %q — its implement bead is closed (fix via QA rework reopening that bead, or edit only %s for %s)",
				written, allowedPath, allowedID)
		}
		return fmt.Errorf("do not overwrite %q — its implement bead is closed (active bead %s)", written, allowedID)
	}
	if allowedPath == "" {
		return nil
	}
	if orchestrator.PathMatchesImplementWrite(written, allowedPath, v.RequiredFiles) {
		return nil
	}
	// cmd/main (and similar) verify builds import earlier packages — allow heredoc only while that path's bead is still open.
	if orchestrator.AllowedEarlierImplementDependencyWrite(townRoot, rig, allowedPath, written, v) {
		return nil
	}
	// go.mod bead: go mod tidy fails until other packages import correctly — allow fixing those .go files.
	if strings.HasSuffix(filepath.ToSlash(allowedPath), "go.mod") && strings.HasSuffix(written, ".go") {
		for _, want := range v.RequiredFiles {
			if orchestrator.PathMatchesImplementWrite(written, want, v.RequiredFiles) {
				return nil
			}
		}
	}
	return fmt.Errorf("write only the active/next implement file (%s for bead %s), not %q",
		allowedPath, allowedID, written)
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

// shellContainsGitWrite detects real git write commands, not prose like "commits to repo" or ".gitkeep".
func shellContainsGitWrite(lower string) bool {
	if !strings.Contains(lower, "git") {
		return false
	}
	for _, sub := range []string{"git commit", "git push", "git add", "git  commit", "git  add", "git  push"} {
		if strings.Contains(lower, sub) {
			return true
		}
	}
	return false
}

// planHeredocBody returns the plan.md heredoc body (after <<), for content-only checks.
func planHeredocBody(cmd string) string {
	lower := strings.ToLower(cmd)
	markers := []string{"<<'eof'", "<<\"eof\"", "<<eof"}
	var start int
	for _, m := range markers {
		if i := strings.Index(lower, m); i >= 0 {
			start = i + len(m)
			break
		}
	}
	if start == 0 {
		return ""
	}
	return cmd[start:]
}

// validatePlanningPlanHeredoc allows plan.md bodies that mention backend/ paths in prose.
func validatePlanningPlanHeredoc(cmd string) error {
	shell := strings.ToLower(designCommandShellPortion(cmd))
	if shellContainsGitWrite(shell) {
		return fmt.Errorf("must not run git add/commit/push in planning step")
	}
	if strings.Contains(shell, "python3") || strings.Contains(shell, "pip install") {
		return fmt.Errorf("must not run python/pip in planning step")
	}
	body := strings.ToLower(planHeredocBody(cmd))
	if strings.Contains(body, "> backend/") || strings.Contains(body, ".py>") {
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
		return validatePlanningPlanHeredoc(cmd)
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

func validatePlanningCommandWithProfile(cmd, rig string, v orchestrator.WorkflowValidation) error {
	if err := validatePlanningCommand(cmd, rig); err != nil {
		return err
	}
	if isBeadCreateCommand(cmd) {
		if title := extractBeadCreateTitle(cmd); title != "" {
			if err := orchestrator.ValidateImplementBeadCreateTitle(title, v); err != nil {
				return err
			}
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
	if isGitAddEntireWorktree(lower) {
		return fmt.Errorf("do not git add entire worktree — stage a path (e.g. git add -A <layout_root>/) from %s", rigMayorRigPath(rig))
	}
	for _, artifact := range []string{"/typescript", ".claude/", ".gt-agent", ".runtime/", "bookmarks.txt", "dummy.py", "plan_complete.js"} {
		if strings.Contains(lower, artifact) {
			return fmt.Errorf("do not commit agent artifacts (%s)", artifact)
		}
	}
	return nil
}

func validatePlanningArtifacts(townRoot, rig string, hadCmdFailure, beadCreateOK, beadDeleteOK bool, v orchestrator.WorkflowValidation) error {
	if err := validatePlanningBeadSet(townRoot, rig, v); err != nil {
		if beadDeleteOK {
			return fmt.Errorf("bead set still invalid after bd delete: %w", err)
		}
		if !beadCreateOK {
			return fmt.Errorf("run `bd create` for missing paths or `bd delete` for duplicates in %s, then ensure open beads match architecture: %w", rigMayorRigPath(rig), err)
		}
		return fmt.Errorf("open implement beads must match active phase required_files: %w", err)
	}
	rigDir := rigMayorRigDir(townRoot, rig)
	path := filepath.Join(rigDir, "plan.md")
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("plan.md missing at %s", path)
	}
	minPlan := orchestrator.EffectiveMinPlanBytes(rigDir, v)
	if info.Size() < minPlan {
		return fmt.Errorf("plan.md too small (%d bytes); need ≥%d (%s)", info.Size(), minPlan, v.PlanMinSizeHint())
	}
	if err := validatePlanMDBeadIDs(townRoot, rig, path, v); err != nil {
		return err
	}
	if rig != "" {
		if err := validateRigOpenBeads(townRoot, rig); err != nil {
			return err
		}
	}
	if hadCmdFailure {
		return fmt.Errorf("planning step had failed commands; fix errors before completing")
	}
	return nil
}

var planBeadIDLineRE = regexp.MustCompile(`(?m)^###\s+([a-zA-Z0-9][a-zA-Z0-9_-]*):\s+`)

// validatePlanMDBeadIDs rejects plan.md sections that cite bead IDs not open in bd list.
func validatePlanMDBeadIDs(townRoot, rig, planPath string, v orchestrator.WorkflowValidation) error {
	data, err := os.ReadFile(planPath)
	if err != nil {
		return err
	}
	open, err := listOpenImplementationBeads(townRoot, rig)
	if err != nil {
		return err
	}
	openIDs := map[string]bool{}
	for _, b := range open {
		if strings.Contains(strings.ToLower(b.Title), strings.ToLower(strings.TrimSpace(v.BeadTitleContains))) {
			openIDs[b.ID] = true
		}
	}
	var missing []string
	for _, m := range planBeadIDLineRE.FindAllStringSubmatch(string(data), -1) {
		id := strings.TrimSpace(m[1])
		if id == "" || openIDs[id] {
			continue
		}
		missing = append(missing, id)
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("plan.md cites bead ID(s) not open in bd list (run bd create first, then rewrite plan.md): %s", strings.Join(missing, ", "))
}

func maybeRepairWorkflowRequirements(townRoot, rig string, v orchestrator.WorkflowValidation) {
	if !v.UsesPythonVenv() {
		return
	}
	workDir := rigMayorRigDir(townRoot, rig)
	repaired, err := agentenv.RepairRequirementsUnder(workDir)
	if err != nil {
		orchestratedFprintfStderr("[gt-agent] requirements repair: %v\n", err)
		return
	}
	if len(repaired) > 0 {
		orchestratedPrintf("[gt-agent] repaired requirements.txt: %s\n", strings.Join(repaired, ", "))
	}
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout != "" {
		if fixed, err := agentenv.RepairBrokenPythonInit(workDir, layout); err != nil {
			orchestratedFprintfStderr("[gt-agent] python __init__ repair: %v\n", err)
		} else if fixed {
			orchestratedPrintf("[gt-agent] repaired %s/__init__.py (removed invalid TaskStore import)\n", layout)
		}
	}
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
	v = v.ForActivePhase()
	if logLine, err := orchestrator.RepairPlanningBeadSet(townRoot, rig, v); err != nil {
		return fmt.Errorf("planning bead repair: %w", err)
	} else if logLine != "" {
		orchestratedPrintf("[gt-agent] planning bead repair: %s\n", logLine)
	}
	open, err := listOpenImplementationBeads(townRoot, rig)
	if err != nil {
		return err
	}
	if len(open) == 0 {
		return fmt.Errorf("no open implementation beads matching %q", v.BeadTitleContains)
	}
	archPath := filepath.Join(rigMayorRigDir(townRoot, rig), "architecture.md")
	if len(v.RequiredFiles) > 0 {
		return orchestrator.ValidatePlanBeads(open, archPath, v, rig)
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

// countOpenMatchingBeadsHook is set by tests to avoid calling bd list.
var countOpenMatchingBeadsHook func(townRoot, rig, titleContains string) (int, error)

func validateImplementationArtifacts(townRoot, rig string, hadCmdFailure, beadCloseOK, verifyOK bool, v orchestrator.WorkflowValidation) error {
	rigDir := rigMayorRigDir(townRoot, rig)
	scoped := v.ForActivePhase()
	diskReady := len(scoped.RequiredFiles) > 0 && orchestrator.ImplementationDiskWorkReady(rigDir, scoped) == nil
	titleContains := strings.TrimSpace(v.BeadTitleContains)
	openImpl := 0
	if titleContains != "" {
		n, err := countOpenMatchingBeads(townRoot, rig, titleContains)
		if err != nil {
			return err
		}
		openImpl = n
	}
	if openImpl > 0 {
		return fmt.Errorf("%d open implement bead(s) remain — continue with Next bead (bd update → heredoc → verify → bd close); send JSON success only when none are open", openImpl)
	}
	if hadCmdFailure {
		return fmt.Errorf("implementation step had failed commands; fix errors before completing")
	}
	if !beadCloseOK && !diskReady {
		return fmt.Errorf("at least one successful `bd close` in %s is required before success", rigMayorRigPath(rig))
	}
	if strings.TrimSpace(v.QAVerifyCommand) != "" && !verifyOK && !diskReady {
		return fmt.Errorf("profile verification must pass in this session before success (%s)", strings.TrimSpace(v.QAVerifyCommand))
	}
	if err := validateRequiredWorkFiles(townRoot, rig, v); err != nil {
		return err
	}
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

func isGitAddEntireWorktree(lower string) bool {
	if strings.Contains(lower, "git add .") || strings.Contains(lower, "git add --all") {
		return true
	}
	for _, flag := range []string{"git add -a", "git add -all"} {
		idx := strings.Index(lower, flag)
		if idx < 0 {
			continue
		}
		rest := strings.TrimSpace(lower[idx+len(flag):])
		if rest == "" || strings.HasPrefix(rest, "&&") || strings.HasPrefix(rest, ";") {
			return true
		}
	}
	return false
}

func isGitCommitLayoutCommand(cmd, layoutRoot string) bool {
	layout := strings.Trim(strings.TrimSpace(layoutRoot), "/")
	if layout == "" || layout == "." {
		layout = "backend"
	}
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "git") && strings.Contains(lower, "commit") &&
		strings.Contains(lower, strings.ToLower(layout))
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

// listOpenImplementationBeadsHook is set by tests to avoid calling bd list.
var listOpenImplementationBeadsHook func(townRoot, rig string) ([]orchestrator.PlanBead, error)

func listOpenImplementationBeads(townRoot, rig string) ([]orchestrator.PlanBead, error) {
	if listOpenImplementationBeadsHook != nil {
		return listOpenImplementationBeadsHook(townRoot, rig)
	}
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
	rigDir := rigMayorRigDir(townRoot, rig)
	planPath := filepath.Join(rigDir, "plan.md")
	info, err := os.Stat(planPath)
	if err != nil {
		return fmt.Errorf("plan.md missing at %s", planPath)
	}
	minPlan := orchestrator.EffectiveMinPlanBytes(rigDir, v)
	if info.Size() < minPlan {
		return fmt.Errorf("plan.md too small (%d bytes); need ≥%d (%s)", info.Size(), minPlan, v.PlanMinSizeHint())
	}
	open, err := listOpenImplementationBeads(townRoot, rig)
	if err != nil {
		return err
	}
	archPath := filepath.Join(rigMayorRigDir(townRoot, rig), "architecture.md")
	if err := orchestrator.ValidatePlanBeads(open, archPath, v, rig); err != nil {
		return fmt.Errorf("plan beads do not match architecture/profile: %w", err)
	}
	return nil
}

func countOpenMatchingBeads(townRoot, rig, titleContains string) (int, error) {
	if countOpenMatchingBeadsHook != nil {
		return countOpenMatchingBeadsHook(townRoot, rig, titleContains)
	}
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	if beadsDir == "" {
		return 0, nil
	}
	if _, err := os.Stat(beadsDir); err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
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

func validateQAArtifacts(townRoot, rig, outcome string, hadCmdFailure, bdListClosedOK, unittestOK, qaSmokeOK bool, v orchestrator.WorkflowValidation) error {
	sendToImpl := outcome == "failure"
	if hadCmdFailure && !sendToImpl {
		return fmt.Errorf("QA step had failed commands; fix errors before completing")
	}
	if !bdListClosedOK {
		return fmt.Errorf("run `bd list --status=closed` from %s before reporting QA outcome", rigMayorRigPath(rig))
	}
	if !sendToImpl {
		if !unittestOK {
			return fmt.Errorf("run `%s` from %s before reporting QA outcome", v.UnittestCommandHint(), rigMayorRigPath(rig))
		}
		if err := validateRequiredWorkFiles(townRoot, rig, v); err != nil {
			return err
		}
		if err := orchestrator.ValidateWorkNotStubbed(rigMayorRigDir(townRoot, rig), v); err != nil {
			return fmt.Errorf("implementation files look like stubs (QA must use outcome failure): %w", err)
		}
		if err := validateWebStaticReferences(townRoot, rig, v); err != nil {
			return err
		}
		if requiresQARuntimeSmoke(v) && !qaSmokeOK {
			return fmt.Errorf("web/API QA requires a live smoke command before passing: start the server with `go run`, then curl `/`, referenced static assets, and API GET/POST behavior")
		}
	}
	if sendToImpl {
		if err := validateRequiredWorkFiles(townRoot, rig, v); err != nil {
			return err
		}
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

func requiresQARuntimeSmoke(v orchestrator.WorkflowValidation) bool {
	if !orchestrator.WorkflowUsesGo(v) {
		return false
	}
	files := append([]string(nil), v.RequiredFiles...)
	files = append(files, v.UnionRequiredFiles()...)
	hasWeb := false
	hasServer := false
	for _, f := range files {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if strings.Contains(f, "/web/") && (strings.HasSuffix(f, ".html") || strings.HasSuffix(f, ".js") || strings.HasSuffix(f, ".css")) {
			hasWeb = true
		}
		if strings.HasSuffix(f, "/cmd/server/main.go") {
			hasServer = true
		}
	}
	return hasWeb && hasServer
}

func isQARuntimeSmokeCommandOK(cmd string, v orchestrator.WorkflowValidation) bool {
	if !requiresQARuntimeSmoke(v) {
		return false
	}
	lower := strings.ToLower(strings.Join(strings.Fields(cmd), " "))
	if !strings.Contains(lower, "go run") || !strings.Contains(lower, "curl ") {
		return false
	}
	if !strings.Contains(lower, "localhost") && !strings.Contains(lower, "127.0.0.1") {
		return false
	}
	if !strings.Contains(lower, " /api/") && !strings.Contains(lower, "/api/") {
		return false
	}
	if profileHasAPI(v) && !strings.Contains(lower, "post") && !strings.Contains(lower, " -d ") && !strings.Contains(lower, " --data") {
		return false
	}
	return true
}

func profileHasAPI(v orchestrator.WorkflowValidation) bool {
	for _, f := range append(append([]string(nil), v.RequiredFiles...), v.UnionRequiredFiles()...) {
		f = strings.ToLower(filepath.ToSlash(strings.TrimSpace(f)))
		if strings.Contains(f, "/api/") || strings.Contains(f, "handler") {
			return true
		}
	}
	return false
}

var htmlAttrRefRE = regexp.MustCompile(`(?i)\b(src|href)\s*=\s*["']([^"'#][^"']*)["']`)

func validateWebStaticReferences(townRoot, rig string, v orchestrator.WorkflowValidation) error {
	rigDir := rigMayorRigDir(townRoot, rig)
	for _, rel := range webHTMLRequiredFiles(v) {
		abs := filepath.Join(rigDir, filepath.FromSlash(rel))
		body, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		webRoot := webRootForHTML(rigDir, rel, v)
		for _, m := range htmlAttrRefRE.FindAllStringSubmatch(string(body), -1) {
			attr := strings.ToLower(m[1])
			ref := strings.TrimSpace(m[2])
			if skipLocalHTMLRef(ref) {
				continue
			}
			if attr == "src" || strings.HasSuffix(strings.ToLower(ref), ".js") || strings.HasSuffix(strings.ToLower(ref), ".css") {
				if !webRefExists(webRoot, rel, ref) {
					return fmt.Errorf("HTML references missing static asset %q from %s; fix the path or add the file", ref, rel)
				}
				continue
			}
			if attr == "href" && isLocalPageRef(ref) && !webPageRefExists(webRoot, rel, ref) && !goServerDefinesRoute(rigDir, v, ref) {
				return fmt.Errorf("HTML link %q in %s has no matching static page or server route; use an in-page anchor for SPA sections", ref, rel)
			}
		}
	}
	return nil
}

func webHTMLRequiredFiles(v orchestrator.WorkflowValidation) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range append(append([]string(nil), v.RequiredFiles...), v.UnionRequiredFiles()...) {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if f == "" || seen[f] || !strings.HasSuffix(strings.ToLower(f), ".html") || !strings.Contains(f, "/web/") {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

func webRootForHTML(rigDir, htmlRel string, v orchestrator.WorkflowValidation) string {
	parts := strings.Split(filepath.ToSlash(htmlRel), "/web/")
	if len(parts) > 1 {
		return filepath.Join(rigDir, filepath.FromSlash(parts[0]), "web")
	}
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" || layout == "." {
		return filepath.Join(rigDir, "web")
	}
	return filepath.Join(rigDir, filepath.FromSlash(layout), "web")
}

func skipLocalHTMLRef(ref string) bool {
	lower := strings.ToLower(strings.TrimSpace(ref))
	return lower == "" || strings.HasPrefix(lower, "#") || strings.HasPrefix(lower, "http://") ||
		strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "mailto:") || strings.HasPrefix(lower, "tel:") ||
		strings.HasPrefix(lower, "/api/")
}

func webRefExists(webRoot, htmlRel, ref string) bool {
	path := webRefPath(webRoot, htmlRel, ref)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func webPageRefExists(webRoot, htmlRel, ref string) bool {
	path := webRefPath(webRoot, htmlRel, ref)
	if path == "" {
		return false
	}
	candidates := []string{path, path + ".html", filepath.Join(path, "index.html")}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

func webRefPath(webRoot, htmlRel, ref string) string {
	ref = strings.TrimSpace(strings.Split(ref, "?")[0])
	ref = strings.Split(ref, "#")[0]
	if ref == "" || strings.Contains(ref, "..") {
		return ""
	}
	if strings.HasPrefix(ref, "/") {
		return filepath.Join(webRoot, filepath.FromSlash(strings.TrimPrefix(ref, "/")))
	}
	htmlDir := filepath.Dir(filepath.Join(webRoot, filepath.Base(filepath.ToSlash(htmlRel))))
	return filepath.Join(htmlDir, filepath.FromSlash(ref))
}

func isLocalPageRef(ref string) bool {
	if skipLocalHTMLRef(ref) || strings.HasPrefix(ref, "/#") {
		return false
	}
	lower := strings.ToLower(strings.Split(strings.Split(ref, "?")[0], "#")[0])
	return !strings.Contains(filepath.Base(lower), ".")
}

func goServerDefinesRoute(rigDir string, v orchestrator.WorkflowValidation, ref string) bool {
	path := "/" + strings.Trim(strings.Split(strings.Split(ref, "?")[0], "#")[0], "/")
	if path == "/" {
		return true
	}
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	root := rigDir
	if layout != "" && layout != "." {
		root = filepath.Join(rigDir, filepath.FromSlash(layout))
	}
	found := false
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || found {
			return nil
		}
		data, readErr := os.ReadFile(p)
		if readErr == nil && strings.Contains(string(data), `"`+path+`"`) {
			found = true
		}
		return nil
	})
	return found
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
		short := v.MinArchitectureBytes - info.Size()
		return fmt.Errorf("architecture.md too small (%d bytes); need ≥%d (%d more). Run `CMD: wc -c %s/mayor/rig/architecture.md`, then rewrite the heredoc with fuller per-file sections (API tables, data model, acceptance) before JSON success", info.Size(), v.MinArchitectureBytes, short, rig)
	}
	// Stale implementation files at mayor/rig root must not block design completion.
	for _, name := range v.ForbiddenRigRootBasenames() {
		if _, err := os.Stat(filepath.Join(rigDir, name)); err == nil {
			return fmt.Errorf("implementation file %q must not exist in mayor/rig/ (only architecture.md)", name)
		}
	}
	return nil
}

func buildOrchestratedSystemPrompt(task *orchestrator.Task) string {
	vars := orchestratorPromptVars(task)
	var b strings.Builder
	if task.SystemPrompt != "" {
		b.WriteString(strings.TrimSpace(task.SystemPrompt))
	}
	if !task.Hooks.OmitOrchestratorContext {
		if task.SystemPrompt != "" {
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
	}
	if footer := task.Hooks.SystemPromptFooterText(vars); footer != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(footer)
	}
	return b.String()
}

func orchestratorPromptVars(task *orchestrator.Task) map[string]string {
	vars := map[string]string{"rig": task.Rig}
	for k, v := range task.Validation.WithDefaults().PromptVars() {
		vars[k] = v
	}
	return vars
}

func buildOrchestratedUserPrompt(task *orchestrator.Task) string {
	body := task.Instructions
	if task.TaskPrompt != "" {
		body = task.TaskPrompt
	}
	if !task.Hooks.UserPromptWrapsWithCompleteStep() {
		return body
	}
	return "Complete this step only:\n\n" + body
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
