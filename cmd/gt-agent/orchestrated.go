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
	"strconv"
	"strings"
	"time"

	"github.com/steveyegge/gastown/cmd/gt-agent/internal/llm"
	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/nudge"
	"github.com/steveyegge/gastown/internal/agentenv"
	"github.com/steveyegge/gastown/internal/orchestrator"
	rigpkg "github.com/steveyegge/gastown/internal/rig"
)

const (
	defaultOrchPollInterval           = 15 * time.Second
	implementationLLMHealthMinTurns   = 10 // rig-flow implementation uses 20; ping before long loops
	implementationLLMHealthTimeout    = 8 * time.Second
	maxOrchestratedCmdTurns           = 5
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
		if runErr != nil && isRetriableLLMError(runErr) {
			orchestratedPrintf("[gt-agent] LLM unavailable (%v) — not completing task; backing off before retry\n", runErr)
			time.Sleep(90 * time.Second)
			state.LastActivity = time.Now()
			_ = saveState(stateFile, state)
			continue
		}
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
			clearQAReviewProgressIfLeaving(townRoot, taskRig, task.State, nextState)
			clearImplementationProgressIfLeaving(townRoot, taskRig, task.State, nextState)
		}

		state.LastActivity = time.Now()
		_ = saveState(stateFile, state)
		time.Sleep(2 * time.Second)
	}

	return nil
}

// ensureLLMReachableForImplementation fails fast when the local LLM proxy is down (avoids burning max_cmd_turns).
func ensureLLMReachableForImplementation(ctx context.Context, client *llm.Client, task *orchestrator.Task, maxTurns int) error {
	if task == nil || client == nil || task.State != "implementation" || maxTurns < implementationLLMHealthMinTurns {
		return nil
	}
	pingCtx, cancel := context.WithTimeout(ctx, implementationLLMHealthTimeout)
	defer cancel()
	if err := client.Ping(pingCtx); err != nil {
		checkURL := llm.HealthCheckURL(client.Endpoint())
		return fmt.Errorf("LLM endpoint unreachable before %d-turn implementation loop: %w (GET %s — start freeride/Ollama or fix LLM_ENDPOINT)", maxTurns, err, checkURL)
	}
	orchestratedPrintf("[gt-agent] LLM health check OK (%s)\n", llm.HealthCheckURL(client.Endpoint()))
	return nil
}

func executeOrchestratedTask(ctx context.Context, client *llm.Client, townRoot, rig, sessionName string, task *orchestrator.Task, priorRetry *OrchestratedRetry) (outcome, summary, attemptLog string, err error) {
	rig = resolveOrchestratedRigName(townRoot, rig)
	if rig == "" {
		return "failure", "workflow rig name not set", "", fmt.Errorf("orchestrator rig variable missing")
	}
	systemPrompt := buildOrchestratedSystemPrompt(task, townRoot)
	userPrompt := buildOrchestratedUserPrompt(task)
	var contextBlocks []string
	if block := drainOrchestratedNudges(townRoot, sessionName); block != "" {
		contextBlocks = append(contextBlocks, block)
		orchestratedPrintf("[gt-agent] injected drained nudge(s) for %s/%s\n", task.WorkflowID, task.State)
	}
	if block := formatWorkflowReworkBlock(task, townRoot, rig); block != "" {
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
	// pre_run (refresh_codeindex, bead queue, reconcile) must run before prompt_context so
	// implement_bead_context sees a fresh codeindex.json and the correct queue head.
	runner.runPreRun()
	if !shouldSkipPlanningAutoComplete(task, townRoot, rig, runner.v) &&
		!shouldSkipImplementationAutoComplete(task, townRoot, rig, runner.v) {
		if o, s, ok := runner.tryAutoOutcome(); ok {
			orchestratedPrintf("[gt-agent] skipping LLM: %s artifacts already valid after pre_run\n", task.State)
			return o, s, "", nil
		}
	} else {
		orchestratedPrintf("[gt-agent] plan review rework pending — running planner LLM (no auto-complete)\n")
	}
	if task.State == "implementation" || task.State == "qa_review" || task.State == "project_setup" {
		if block := orchestrator.FormatMissingImplementFilesBlock(townRoot, rig, runner.v); block != "" {
			contextBlocks = append(contextBlocks, block)
			orchestratedPrintf("[gt-agent] injecting missing implement files context for %s/%s\n", task.WorkflowID, task.State)
		}
	}
	if block := runner.initQAReviewProgress(); block != "" {
		contextBlocks = append(contextBlocks, block)
		orchestratedPrintf("[gt-agent] loaded qa-review-progress for %s/%s\n", task.WorkflowID, task.State)
	}
	if block := runner.initImplementationProgress(); block != "" {
		contextBlocks = append(contextBlocks, block)
		orchestratedPrintf("[gt-agent] loaded implementation-progress for %s/%s\n", task.WorkflowID, task.State)
	}
	for _, block := range runner.promptContextBlocks() {
		contextBlocks = append(contextBlocks, block)
	}
	if len(runner.hooks.PromptContext) > 0 {
		orchestratedPrintf("[gt-agent] injecting prompt_context for %s/%s: %v\n", task.WorkflowID, task.State, runner.hooks.PromptContext)
	}
	runner.logCodeindexInjectionForActiveBead()
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
	if err := ensureLLMReachableForImplementation(ctx, client, task, maxTurns); err != nil {
		return "failure", err.Error(), "", err
	}
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

		var combined strings.Builder
		hadNative, hadSuccessfulNative, cmdCount := runner.processOrchestratedTools(response, sessionName, &combined)
		if hadSuccessfulNative {
			runner.noteImplementationFixAttempt("", true)
		}

		if cmdCount == 0 && !hadNative && responseHasUnterminatedHeredoc(response) {
			msg := "Your reply started a heredoc (e.g. plan.md <<'EOF') but never sent a line with only EOF — the message was cut off, so no command ran.\n\n" +
				"Fix: split across turns — (1) `bd list --status=open`, (2) `cat > plan.md <<'EOF'` with ## Bead map and ### <id>: <full-path> sections (scope + acceptance per file; must meet min_plan_bytes), (3) line with only `EOF`, (4) `wc -c plan.md`, (5) JSON success only."
			if h := runner.failureHint(); h != "" {
				msg += "\n\n" + h
			}
			recordAttemptFeedback(msg + "\n")
			messages = append(messages, llm.Message{Role: "user", Content: msg})
			continue
		}
		if cmdCount > 0 || hadNative {
			var feedbackBuilder strings.Builder
			feedbackBuilder.WriteString(combined.String())
			recordAttemptFeedback(feedbackBuilder.String())
			feedbackBuilder.WriteString("\n\nCommands executed. If the step is complete, reply with JSON only (no CMD lines): {\"outcome\":\"...\",\"summary\":\"...\"}")
			if runner.hooks.Artifacts == "design" && runner.track.designArchWritten {
				feedbackBuilder.WriteString("\n\nDesign: when architecture.md meets the byte minimum and validates, this step auto-completes — no JSON turn required.")
			}
			if turn == maxTurns {
				feedbackBuilder.WriteString(" Use an allowed outcome.")
			}
			runner.appendImplementationCodeindexReminder(&feedbackBuilder)
			feedback := feedbackBuilder.String()
			if o, s, ok := parseOrchestratedResult(response, task.AllowedOutcomes); ok {
				o = normalizeOrchestratedOutcome(o, task.AllowedOutcomes)
				if o == "failure" || o == "fail" {
					orchestratedPrintf("[gt-agent] ignoring failure JSON in same turn as CMD lines; review output then send JSON only\n")
					recordAttemptFeedback("Failure JSON ignored because CMD lines ran this turn. Review command output, then reply with JSON only.\n")
				} else if task.State == "implementation" && isOrchestratedSuccessOutcome(o) {
					orchestratedPrintf("[gt-agent] ignoring success JSON in same turn as CMD/native tools; run Verify and bd close first, then JSON only\n")
					recordAttemptFeedback("Success JSON ignored because commands ran this turn. Finish Verify + bd close for the active bead, then reply with JSON only.\n")
				} else if msg, reject := runner.rejectImplementationNoOpFailure(o); reject {
					orchestratedPrintf("[gt-agent] rejecting implementation failure JSON without fix work this attempt\n")
					recordAttemptFeedback(msg + "\n")
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

		if hadNative {
			var feedbackBuilder strings.Builder
			feedbackBuilder.WriteString(combined.String())
			recordAttemptFeedback(feedbackBuilder.String())
			feedbackBuilder.WriteString("\n\nNative edits processed. If the step is complete, reply with JSON only: {\"outcome\":\"...\",\"summary\":\"...\"}")
			if turn == maxTurns {
				feedbackBuilder.WriteString(" Use an allowed outcome.")
			}
			runner.appendImplementationCodeindexReminder(&feedbackBuilder)
			feedback := feedbackBuilder.String()
			if o, s, ok := parseOrchestratedResult(response, task.AllowedOutcomes); ok {
				o = normalizeOrchestratedOutcome(o, task.AllowedOutcomes)
				if task.State == "implementation" && isOrchestratedSuccessOutcome(o) {
					orchestratedPrintf("[gt-agent] ignoring success JSON in same turn as native tools\n")
					recordAttemptFeedback("Success JSON ignored — run Verify from Next bead and `bd close` before JSON success.\n")
				} else if o != "" {
					if msg, reject := runner.rejectImplementationNoOpFailure(o); reject {
						orchestratedPrintf("[gt-agent] rejecting implementation failure JSON without fix work this attempt\n")
						recordAttemptFeedback(msg + "\n")
					} else if vErr := runner.validateArtifacts(o); vErr != nil {
						recordAttemptFeedback("Validation failed: " + vErr.Error() + "\n")
					} else {
						return o, s, lastAttemptFeedback.String(), nil
					}
				}
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
			if o, s, ok := runner.tryPlanReviewFailureToSuccess(o); ok {
				orchestratedPrintf("[gt-agent] ignoring plan_review failure JSON — artifacts already satisfy success\n")
				return o, s, lastAttemptFeedback.String(), nil
			}
			if msg, reject := runner.rejectPlanReviewSpuriousFailure(o, s); reject {
				orchestratedPrintf("[gt-agent] rejecting spurious plan_review failure JSON\n")
				recordAttemptFeedback(msg + "\n")
				messages = append(messages, llm.Message{Role: "user", Content: msg})
				continue
			}
			if vErr := validateOutcomeForTask(task, townRoot, rig, o, s); vErr != nil {
				orchestratedPrintf("[gt-agent] summary validation failed: %v\n", vErr)
				msg := "Validation failed: " + vErr.Error() + ". Run `bd list` and copy bead IDs exactly into the summary."
				recordAttemptFeedback(msg + "\n")
				messages = append(messages, llm.Message{Role: "user", Content: msg})
				continue
			}
			if msg, reject := runner.rejectImplementationFalseBeadInfraFailure(o, s); reject {
				orchestratedPrintf("[gt-agent] rejecting false bead-infra failure JSON\n")
				recordAttemptFeedback(msg + "\n")
				messages = append(messages, llm.Message{Role: "user", Content: msg})
				continue
			}
			if msg, reject := runner.rejectImplementationNoOpFailure(o); reject {
				orchestratedPrintf("[gt-agent] rejecting implementation failure JSON without fix work this attempt\n")
				recordAttemptFeedback(msg + "\n")
				messages = append(messages, llm.Message{Role: "user", Content: msg})
				continue
			}
			if msg, reject := runner.rejectImplementationSuccessWithoutDisk(o); reject {
				orchestratedPrintf("[gt-agent] rejecting implementation success JSON while active bead file missing on disk\n")
				recordAttemptFeedback(msg + "\n")
				messages = append(messages, llm.Message{Role: "user", Content: msg})
				continue
			}
			if msg, reject := runner.rejectImplementationPrematureSuccess(o); reject {
				orchestratedPrintf("[gt-agent] rejecting implementation success JSON while phase verify still blocked\n")
				recordAttemptFeedback(msg + "\n")
				messages = append(messages, llm.Message{Role: "user", Content: msg})
				continue
			}
			if msg, reject := runner.rejectImplementationOpenBeadsSuccess(o, s); reject {
				orchestratedPrintf("[gt-agent] rejecting implementation success JSON while open beads remain\n")
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

		hint := orchestratedEmptyTurnHint(runner.hooks)
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
	if task != nil && task.State == "qa_review" {
		summary = "QA exhausted turns without a clean outcome; check phase verify and artifact validation results above"
	}
	return "fail", summary, lastAttemptFeedback.String(), fmt.Errorf("no structured outcome after %d turns", maxTurns)
}

func isOrchestratedFailureOutcome(outcome string) bool {
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case "fail", "failure":
		return true
	default:
		return false
	}
}

func isOrchestratedSuccessOutcome(outcome string) bool {
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case "success", "all_passed", "task_passed", "completed":
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

// shouldSkipPlanningAutoComplete blocks planning auto-complete while plan_review rework is pending
// or while docs would still fail plan_review (e.g. architecture store API drift).
func shouldSkipPlanningAutoComplete(task *orchestrator.Task, townRoot, rig string, v orchestrator.WorkflowValidation) bool {
	if task == nil || task.State != "planning" {
		return false
	}
	if task.PendingRework != nil && task.PendingRework.FromState == "plan_review" {
		return true
	}
	if townRoot != "" && rig != "" {
		if err := orchestrator.ValidatePlanningPhaseGate(townRoot, rig, "plan_review", v); err != nil {
			return true
		}
	}
	return false
}

// shouldSkipImplementationAutoComplete blocks auto-complete after QA rework or while go.mod
// still fails SPEC validation (go mod tidy alone is not enough).
func shouldSkipImplementationAutoComplete(task *orchestrator.Task, townRoot, rig string, v orchestrator.WorkflowValidation) bool {
	if task == nil || task.State != "implementation" {
		return false
	}
	if task.PendingRework != nil && task.PendingRework.FromState == "qa_review" {
		return true
	}
	if townRoot == "" || rig == "" || !orchestrator.WorkflowUsesGo(v) {
		return false
	}
	scoped := v.ForActivePhase()
	hasGoMod := false
	for _, f := range scoped.RequiredFiles {
		if strings.HasSuffix(filepath.ToSlash(f), "/go.mod") || f == "go.mod" {
			hasGoMod = true
			break
		}
	}
	if !hasGoMod {
		return false
	}
	rigDir := rigMayorRigDir(townRoot, rig)
	return orchestrator.ValidateGoModFile(rigDir, scoped) != nil
}

// formatWorkflowReworkBlock returns cross-step failure context (e.g. QA plan_review → planner).
func formatWorkflowReworkBlock(task *orchestrator.Task, townRoot, rig string) string {
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
	v := taskValidation(townRoot, task)
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
	if fromState == "qa_review" && toState == "design" {
		return "\n### QA escalated architecture rework — do this first\n" +
			"1. Read the QA **summary** and smoke/curl output above — unit tests passed; the **architecture** is wrong.\n" +
			"2. Rewrite architecture.md so HTTP routes, static asset paths, API request/response shapes, and SPA navigation match what QA must verify.\n" +
			"3. Align with SPEC.md; resolve contradictions (wrong paths in the HTTP table, missing POST routes, SPA using bare paths instead of /# anchors).\n" +
			"4. wc -c architecture.md ≥ minimum, then JSON success — the planner will update plan.md and beads next.\n"
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
1. Fix the **specific** issues in the QA summary and command output (paths under %s/, tests, stubs). If QA reported **smoke/404/curl** failures, fix **handlers** and **web/** (routes + index.html asset paths) — not only package unit tests.
2. `+"`"+`CMD: bash -lc 'cd %s && bd list --status=open'`+"`"+` — pick a bead whose title contains %q (handler/web beads first when smoke failed).
3. If **no** open implement beads: `+"`"+`bd list --status=closed`+"`"+`, find closed implement beads, reopen one with `+"`"+`bd update <id-from-bd-list> --status=open`+"`"+`, then fix code and `+"`"+`bd close <id-from-bd-list>`+"`"+`.
4. **Never** invent bead IDs — copy only from bd list output for this rig.
5. **Incremental fixes only** — existing files must use `+"`"+`sed -i`+"`"+` or `+"`"+`patch`+"`"+`, not `+"`"+`cat > path <<'EOF'`+"`"+` full rewrites. Use heredoc only for **new** files.
6. **Do not** send JSON success until runtime issues are fixed; gt-agent will not auto-complete implementation on unit tests alone while QA rework is pending.
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
	if task.State == "implementation" && prior.Outcome == "failure" {
		b.WriteString("\nIf **Implementation progress** or **Active bead looks complete on disk** says the file is already done, run **Verify** + `bd close` for that bead — do not repeat failure JSON.\n")
	}
	if task.State == "implementation" {
		b.WriteString("\nUse **sed -i** or **patch** on internal .go files; **cmd/…/main.go may use heredoc** when Source context shows duplicate or stub handlers. Match APIs in **Dependency packages**.\n")
		if strings.Contains(prior.Feedback, "invalid memory address") && strings.Contains(prior.Feedback, "nil pointer") {
			b.WriteString("\n### LIKELY CAUSE: package-level DB variable is nil\n")
			b.WriteString("The test calls functions that use a package-level `*sql.DB` which was never initialized. Add at the top of the failing test or TestMain:\n")
			b.WriteString("```go\ndb, err := sql.Open(\"sqlite3\", \":memory:\")\nif err != nil { t.Fatal(err) }\n\n// Set the package-level DB variable (check the owning package for the variable name)\npkg.DB = db\n// Call the schema init function from that same package\npkg.InitSchema(db)\n```\n")
			b.WriteString("Import `\"database/sql\"` and the package that owns the DB variable. Do NOT rename handler functions — the nil pointer is a test setup issue only.\n")
		}
		if strings.Contains(prior.Feedback, "got 500 want 200") || strings.Contains(prior.Feedback, "got 404 want 200") {
			b.WriteString("\n### LIKELY CAUSE: wrong working directory for file serving\n")
			b.WriteString("Go tests run with cwd set to the package directory. If the handler serves files relative to the module root, add to TestMain:\n")
			b.WriteString("```go\nfunc TestMain(m *testing.M) {\n    os.Chdir(filepath.Join(\"..\", \"..\")) // go up to module root\n    os.Exit(m.Run())\n}\n```\n")
		}
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

// outcomeJSONLeadingColonRE strips partial JSON the model glues after verify (e.g. :"success","summary":...).
var outcomeJSONLeadingColonRE = regexp.MustCompile(`(?i)^\s*:\s*"success"\s*,\s*"summary"\s*:[\s\S]*$`)

// stripGluedOutcomeJSONFromLine removes outcome JSON glued onto a shell line.
func stripGluedOutcomeJSONFromLine(line string) string {
	var out []string
	for _, l := range strings.Split(line, "\n") {
		l = outcomeJSONTailRE.ReplaceAllString(l, "")
		if outcomeJSONLeadingColonRE.MatchString(strings.TrimSpace(l)) {
			continue
		}
		l = outcomeJSONLeadingColonRE.ReplaceAllString(l, "")
		l = strings.TrimRight(l, " \t")
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// Matches ```cmd: / ```CMD / ```cmd (LLMs often omit the colon).
var markdownFencedCMDRE = regexp.MustCompile("(?im)^```\\s*cmd:?\\s*$")

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
		if cleaned := stripGluedOutcomeJSONFromLine(line); cleaned != line {
			line = cleaned
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
	filtered := preprocessOrchestratedResponse(response)
	filtered = stripNativeToolBlocksForCmdParse(filtered)
	filtered = stripOutcomeLinesForCmdParse(filtered)
	filtered = stripModelToolArtifacts(filtered)
	filtered = normalizeMarkdownFencedCMD(filtered)
	cmds, _, _ := parseLLMResponse(filtered)
	return expandGluedOrchestratedCommands(cmds)
}

// expandGluedOrchestratedCommands splits shell lines that embed CMD: markers mid-command.
func expandGluedOrchestratedCommands(cmds []string) []string {
	var out []string
		for _, c := range cmds {
		c = strings.TrimSpace(c)
		if c == "" || isStandaloneHeredocDelimiter(c) || isOrchestratedShellNoiseLine(c) {
			continue
		}
		if !strings.Contains(c, "CMD:") {
			if fixed, ok := sanitizeOrchestratedShellCommand(c); ok {
				c = fixed
			}
			out = append(out, c)
			continue
		}
		for _, part := range splitInlineCMDs(c) {
			part = strings.TrimSpace(part)
			if fixed, ok := sanitizeOrchestratedShellCommand(part); ok {
				part = fixed
			}
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
	response = markdownFencedCMDRE.ReplaceAllString(response, "```CMD")
	response = unwrapMarkdownFencedToolBlocks(response)
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
		{"{{rig}}/mayor/rig", work},
		{"{{rig}}", rig},
		{"RIG/mayor/rig", work},
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
	// Safely replace "rig/mayor/rig" if the LLM output it literally, but avoid matching "ping_rig/..."
	re := regexp.MustCompile(`(^|[^a-zA-Z0-9_])rig/mayor/rig`)
	if re.MatchString(out) {
		out = re.ReplaceAllString(out, "${1}"+work)
		changed = true
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

func validateImplementationCommandWithState(cmd, townRoot, rig, activeBead string, v orchestrator.WorkflowValidation, verifyOK bool, scope *orchestrator.ImplementWriteScope, lastVerifyOutput string) error {
	if err := validateImplementationCommand(cmd, rig); err != nil {
		return err
	}
	if err := rigpkg.RejectMayorRigRootShellCommand(cmd, v.LayoutRoot); err != nil {
		return err
	}
	mayorDir := rigMayorRigDir(townRoot, rig)
	if err := validateGoImplementationCommand(cmd, townRoot, rig, mayorDir, activeBead, v, verifyOK); err != nil {
		return err
	}
	if err := validatePythonImplementationCommand(cmd, townRoot, rig, activeBead, v, verifyOK); err != nil {
		return err
	}
	if isPipInstallRequirementsCommand(cmd) {
		if !verifyOK && isPipInstallForActiveBead(cmd, townRoot, rig, activeBead, v) {
			return nil
		}
		if !pythonVerifyOutputSuggestsMissingDeps(lastVerifyOutput) {
			return fmt.Errorf("install dependencies in project_setup — venv and pip install already ran there")
		}
	}
	if err := validateCustomImplementationCommand(cmd, townRoot, rig, activeBead, v, verifyOK); err != nil {
		return err
	}
	if err := validateImplementationBeadFileWrite(cmd, townRoot, rig, activeBead, v, scope); err != nil {
		return err
	}
	if err := validateImplementationBeadClose(cmd, townRoot, rig, v, verifyOK); err != nil {
		return err
	}
	if err := validateImplementationBeadReopen(cmd, townRoot, rig, v, scope, lastVerifyOutput); err != nil {
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

// validateImplementationBeadReopen blocks reopening implement beads when the queue is done and module tests pass.
func validateImplementationBeadReopen(cmd, townRoot, rig string, v orchestrator.WorkflowValidation, scope *orchestrator.ImplementWriteScope, lastVerifyOutput string) error {
	lower := strings.ToLower(cmd)
	if !strings.Contains(lower, "bd update") || !strings.Contains(lower, "--status=open") {
		return nil
	}
	id := extractBeadIDFromBdUpdate(cmd)
	if id == "" {
		return nil
	}
	if scope != nil && scope.QAReworkFromQAReview && scope.BeadCited(id) {
		return nil
	}
	// Allow reopen only for the next open implement bead (the one the polecat should work on).
	next, err := orchestrator.NextOpenImplementBead(townRoot, rig, v)
	if err == nil && next != nil && strings.EqualFold(strings.TrimSpace(next.ID), strings.TrimSpace(id)) {
		return nil
	}
	// Block reopening closed beads — the polecat should only work on the active/next open bead.
	if !orchestrator.ImplementationQueueGreen(townRoot, rig, v) {
		return fmt.Errorf("do not reopen %s — work on the current open bead `bd list --status=open`", id)
	}
	if orchestrator.WorkflowNeedsRuntimeSmoke(townRoot, rig, v) {
		if err := orchestrator.ImplementationPhaseVerifyOK(townRoot, rig, v); err != nil {
			return nil
		}
	}
	if verifyOutputCitesClosedBead(lastVerifyOutput, townRoot, rig, id, v) {
		return nil
	}
	return fmt.Errorf("do not reopen implement beads (%s) — go test ./... already passes; send JSON success only", id)
}

func verifyOutputCitesClosedBead(output, townRoot, rig, beadID string, v orchestrator.WorkflowValidation) bool {
	if output == "" || townRoot == "" || rig == "" || beadID == "" {
		return false
	}
	beadPath := orchestrator.ImplementBeadPathForID(townRoot, rig, beadID, v)
	if beadPath == "" {
		return false
	}
	return strings.Contains(strings.ToLower(output), strings.ToLower(beadPath))
}

// validateImplementationBeadFileWrite rejects heredoc/touch writes to paths outside the active or next implement bead.
func validateImplementationBeadFileWrite(cmd, townRoot, rig, activeBead string, v orchestrator.WorkflowValidation, scope *orchestrator.ImplementWriteScope) error {
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
		var sc orchestrator.ImplementWriteScope
		if scope != nil {
			sc = *scope
		}
		if reopened, rerr := orchestrator.EnsureOpenImplementBeadForRework(townRoot, rig, written, v); rerr != nil {
			return rerr
		} else if reopened != "" {
			return nil
		}
		if !orchestrator.AllowedQAReworkWebImplementWrite(townRoot, rig, allowedID, allowedPath, written, sc, v) {
			if allowedPath != "" {
				return fmt.Errorf("do not overwrite %q — its implement bead is closed (fix via QA rework reopening that bead, or edit only %s for %s)",
					written, allowedPath, allowedID)
			}
			return fmt.Errorf("do not overwrite %q — its implement bead is closed (active bead %s)", written, allowedID)
		}
	}
	if allowedPath == "" {
		return nil
	}
	// Scope only (fullReplace false): heredoc/WRITE incremental rules handled above via RejectFullFileHeredocReason.
	return orchestrator.ValidateImplementWritePath(townRoot, rig, activeBead, written, v, false, "", scope)
}

func validatePlanningRuntimeCommands(lower string) error {
	for _, blocked := range []struct {
		sub string
		msg string
	}{
		{"go test", "must not run go test in planning — polecat verifies after implementation"},
		{"go build", "must not run go build in planning — polecat verifies after implementation"},
		{"go run", "must not run go run in planning — no server smoke until QA/implementation"},
		{"go vet", "must not run go vet in planning step"},
		{"pytest", "must not run pytest in planning step"},
		{"curl ", "must not run curl/runtime smoke in planning step"},
	} {
		if strings.Contains(lower, blocked.sub) {
			return fmt.Errorf("%s", blocked.msg)
		}
	}
	return nil
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
	if err := validatePlanningRuntimeCommands(lower); err != nil {
		return err
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

func validatePlanningCommandWithProfile(cmd, townRoot, rig string, v orchestrator.WorkflowValidation) error {
	if orchestrator.RequiresExactImplementPaths(v) && isPlanMDWriteCommand(cmd) {
		return fmt.Errorf("do not write plan.md (heredoc or redirect) — sync_planning_artifacts already builds plan.md from required_files and open bead IDs; run bd list and wc -c plan.md only, then JSON success")
	}
	if err := validatePlanningCommand(cmd, rig); err != nil {
		return err
	}
	if err := rigpkg.RejectMayorRigRootShellCommand(cmd, v.LayoutRoot); err != nil {
		return err
	}
	if isBeadCreateCommand(cmd) {
		if title := extractBeadCreateTitle(cmd); title != "" {
			if err := orchestrator.ValidatePlanningBeadCreate(townRoot, rig, title, v); err != nil {
				return err
			}
		}
	}
	return nil
}

var bdPlaceholderIDRE = regexp.MustCompile(`<[a-zA-Z][a-zA-Z0-9_-]*>`)

// validateImplementationCommand blocks gt bd hallucinations; polecat uses bare bd in rig workdir.
func validateImplementationCommand(cmd, rig string) error {
	lower := strings.ToLower(cmd)
	if err := validateImplementationBeadPlaceholder(cmd, "", rig); err != nil {
		return err
	}
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
	if strings.Contains(lower, ".beads") && (strings.Contains(lower, "rm -rf") || strings.Contains(lower, "rm -r ")) {
		return fmt.Errorf("do not delete the rig .beads database during implementation — use bd list --status=closed and bd update <id> --status=open")
	}
	if strings.Contains(lower, "rm -rf") || strings.Contains(lower, "rm -r ") {
		mayor := strings.ToLower(rigMayorRigPath(rig))
		if strings.Contains(lower, mayor) && !strings.Contains(lower, ".beads") {
			return fmt.Errorf("do not rm -rf under %s during implementation — use EDIT:/WRITE: on the active bead file", rigMayorRigPath(rig))
		}
	}
	if strings.Contains(lower, "bd init") {
		return fmt.Errorf("do not run bd init during implementation — beads already exist; use bd list --status=closed")
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

func validateImplementationBeadPlaceholder(cmd, townRoot, rig string) error {
	lower := strings.ToLower(cmd)
	if !strings.Contains(lower, "bd ") && !strings.Contains(lower, "bd\t") {
		return nil
	}
	for _, literal := range []string{
		"<id-from-bd-list>", "<bead-id>", "<identified-bead-id>", "<bead_id>",
		"<te-xxx>", "<id>", "bead_id", "identified-bead-id",
	} {
		if strings.Contains(lower, literal) {
			return fmt.Errorf("use a real bead ID from `bd list` output (e.g. %s) — not placeholder %q",
				beadIDExample(townRoot, rig), literal)
		}
	}
	if strings.Contains(cmd, "BEAD_ID") {
		return fmt.Errorf("replace BEAD_ID with a real ID from `bd list` output (e.g. %s)", beadIDExample(townRoot, rig))
	}
	if bdPlaceholderIDRE.MatchString(cmd) {
		return fmt.Errorf("do not use angle-bracket placeholders in bd commands — copy IDs from `bd list` (e.g. %s)",
			beadIDExample(townRoot, rig))
	}
	if err := validateBdCommandBeadID(cmd, townRoot, rig); err != nil {
		return err
	}
	return nil
}

func validateBdCommandBeadID(cmd, townRoot, rig string) error {
	var id string
	switch {
	case isBeadUpdateInProgressCommand(cmd):
		id = extractBeadIDFromBdUpdate(cmd)
	case isBeadCloseCommand(cmd):
		id = extractBeadIDFromBdClose(cmd)
	default:
		return nil
	}
	id = strings.Trim(id, `"'`)
	if id == "" {
		return nil
	}
	if bdBeadNumericIDRE.MatchString(id) {
		return fmt.Errorf("bead ID %q is invalid — use a real ID from `bd list` (e.g. %s), not a bare number",
			id, beadIDExample(townRoot, rig))
	}
	return nil
}

func validatePlanningArtifacts(townRoot, rig string, hadCmdFailure, beadCreateOK, beadDeleteOK bool, v orchestrator.WorkflowValidation) error {
	rigDir := rigMayorRigDir(townRoot, rig)
	path := filepath.Join(rigDir, "plan.md")
	if err := validatePlanMDBeadIDs(townRoot, rig, path, v); err != nil {
		return err
	}
	if err := orchestrator.ValidatePlanMDBeadPathAlignment(townRoot, rig, v); err != nil {
		return err
	}
	if rig != "" {
		if err := validateRigOpenBeads(townRoot, rig); err != nil {
			return err
		}
	}
	if err := orchestrator.ValidatePlanningPhaseGate(townRoot, rig, "planning", v); err != nil {
		if beadDeleteOK {
			return fmt.Errorf("bead set still invalid after bd delete: %w", err)
		}
		if !beadCreateOK {
			return fmt.Errorf("run `bd create` for missing paths or `bd delete` for duplicates in %s: %w", rigMayorRigPath(rig), err)
		}
		return err
	}
	if hadCmdFailure {
		return fmt.Errorf("planning step had failed commands; fix errors before completing")
	}
	return nil
}

var planBeadIDLineRE = regexp.MustCompile(`(?m)^###\s+([a-zA-Z0-9][a-zA-Z0-9_-]*):\s+`)

// validatePlanMDBeadIDs rejects plan.md sections that cite bead IDs not open/in_progress in bd list.
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
		if orchestrator.MatchesImplementBeadTitle(b.Title, v) {
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
	return fmt.Errorf("plan.md cites bead ID(s) not open/in_progress in bd list (run bd list --status=open,in_progress, then rewrite or run gt rig sync-planning %s --force): %s", rig, strings.Join(missing, ", "))
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
var countOpenMatchingBeadsHook func(townRoot, rig string, v orchestrator.WorkflowValidation) (int, error)

// implementationPhaseVerifyOKHook overrides on-disk phase verify (tests only).
var implementationPhaseVerifyOKHook func(townRoot, rig string, v orchestrator.WorkflowValidation) error

func implementationPhaseVerifyOKOnDisk(townRoot, rig string, v orchestrator.WorkflowValidation) error {
	if implementationPhaseVerifyOKHook != nil {
		return implementationPhaseVerifyOKHook(townRoot, rig, v)
	}
	return orchestrator.ImplementationPhaseVerifyOK(townRoot, rig, v)
}

func validateImplementationArtifacts(townRoot, rig string, hadCmdFailure, beadCloseOK, verifyOK bool, v orchestrator.WorkflowValidation) error {
	rigDir := rigMayorRigDir(townRoot, rig)
	scoped := v.ForActivePhase()
	diskReady := len(scoped.RequiredFiles) > 0 && orchestrator.ImplementationDiskWorkReady(rigDir, scoped) == nil
	openImpl := 0
	if strings.TrimSpace(scoped.BeadTitleContains) != "" || len(scoped.RequiredFiles) > 0 {
		n, err := countOpenMatchingBeads(townRoot, rig, scoped)
		if err != nil {
			return err
		}
		openImpl = n
	}
	if openImpl > 0 {
		next, nerr := orchestrator.NextOpenImplementBead(townRoot, rig, scoped)
		if nerr == nil && next != nil && next.ID != "" {
			path := orchestrator.ImplementBeadPathForID(townRoot, rig, next.ID, scoped)
			return fmt.Errorf("%d open implement bead(s) remain — Next bead: %s (%s, `%s`). Run `bd update %s --status=in_progress`, implement, Verify, then `bd close %s`; send JSON success only when none are open",
				openImpl, next.ID, next.Title, path, next.ID, next.ID)
		}
		return fmt.Errorf("%d open implement bead(s) remain — run `bd close <id>` (bead is written and verify passes), then send JSON success. Use `bd list --status=open` for bead IDs", openImpl)
	}
	if hadCmdFailure {
		return fmt.Errorf("implementation step had failed commands; fix errors before completing")
	}
	if !beadCloseOK && !diskReady {
		return fmt.Errorf("at least one successful `bd close` in %s is required before success", rigMayorRigPath(rig))
	}
	phaseVerify := strings.TrimSpace(scoped.QAVerifyCommand)
	if phaseVerify != "" && !verifyOK {
		if openImpl == 0 && implementationPhaseVerifyOKOnDisk(townRoot, rig, scoped) == nil {
			// Queue empty and phase verify is green on disk — do not require a manual verify CMD
			// in this session after gt-agent restarts (verifyOK clears on new sessions).
		} else {
			return fmt.Errorf("profile verification must pass in this session before success (%s)", phaseVerify)
		}
	}
	if openImpl == 0 && orchestrator.WorkflowUsesGo(scoped) {
		if err := orchestrator.HandleImplementationPhaseVerifyFailure(townRoot, rig, scoped); err != nil {
			return fmt.Errorf("all implement beads are closed but compile or runtime smoke failed: %w", err)
		}
	}
	if err := validateRequiredWorkFiles(townRoot, rig, scoped); err != nil {
		return err
	}
	if err := orchestrator.ValidateLayoutPythonSources(rigDir, scoped); err != nil {
		return fmt.Errorf("invalid Python under %s: %w", scoped.LayoutRoot, err)
	}
	stubScope := scoped
	if v.HasPhasedDelivery() {
		// During phased delivery, only validate files from active + past phases.
		// Future-phase files don't exist yet and are validated when their phase activates.
		stubScope.RequiredFiles = v.PhasedActiveAndPastRequiredFiles()
	}
	if err := orchestrator.ValidateRequiredFilesNotStubbed(rigDir, stubScope); err != nil {
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

func validateQARuntimeSmokeCommand(cmd, rig, townRoot string, v orchestrator.WorkflowValidation) error {
	if townRoot == "" || rig == "" || !orchestrator.WorkflowNeedsQARuntimeSmoke(townRoot, rig, v) {
		return nil
	}
	lower := strings.ToLower(cmd)
	if !strings.Contains(lower, "curl ") {
		return nil
	}
	if orchestrator.IsProfileDerivedSmokeCommand(cmd) {
		return nil
	}
	spec, _ := orchestrator.LoadAPISmokeSpecFromRig(townRoot, rig, v)
	port := spec.Port
	if port <= 0 {
		port = 8080
	}
	layout := v.LayoutRootDir()
	if orchestrator.WorkflowUsesPython(v) {
		if !strings.Contains(lower, "uvicorn") && !strings.Contains(lower, "gunicorn") &&
			!strings.Contains(lower, "flask run") && !strings.Contains(lower, "hypercorn") {
			return fmt.Errorf("Python runtime smoke must start the server in the same CMD (e.g. cd %s/%s && uvicorn main:app --host 127.0.0.1 --port %d & sleep 1 && curl http://127.0.0.1:%d/ping) — bare curl with no server running always fails", rigMayorRigPath(rig), layout, port, port)
		}
	}
	if orchestrator.WorkflowUsesGo(v) {
		if !strings.Contains(lower, "go run") {
			return fmt.Errorf("Go runtime smoke must include go run ./cmd/server in the same CMD as curl — bare curl with no server running always fails")
		}
	}
	if spec.Port <= 0 {
		return nil
	}
	for _, m := range localhostPortRE.FindAllStringSubmatch(cmd, -1) {
		if len(m) < 2 {
			continue
		}
		port, err := strconv.Atoi(m[1])
		if err != nil || port == spec.Port {
			continue
		}
		return fmt.Errorf("curl uses port %d but SPEC/architecture documents port %d — use http://127.0.0.1:%d", port, spec.Port, spec.Port)
	}
	return nil
}

func validateQACommand(cmd, rig, townRoot string, v orchestrator.WorkflowValidation) error {
	lower := strings.ToLower(cmd)
	if strings.Contains(lower, "[tool_calls]") {
		return fmt.Errorf("do not emit [TOOL_CALLS] markers — use CMD: lines only")
	}
	if orchestrator.WorkflowUsesGo(v) && orchestrator.PhaseIsGoModOnly(v) {
		if strings.Contains(lower, "go test") || strings.Contains(lower, "go build") {
			return fmt.Errorf("active phase is go.mod only — run %s (no go test until .go sources exist)", v.QAVerifyHint())
		}
	}
	if townRoot != "" && rig != "" && !orchestrator.WorkflowNeedsQARuntimeSmoke(townRoot, rig, v) {
		if strings.Contains(lower, "curl ") || strings.Contains(lower, "go run") {
			return fmt.Errorf("this rig profile does not require runtime smoke — run %s only (no curl/go run)", v.QAVerifyHint())
		}
	}
	if err := validateQARuntimeSmokeCommand(cmd, rig, townRoot, v); err != nil {
		return err
	}
	if strings.Contains(lower, "if [") || strings.Contains(lower, "then ") || strings.Contains(lower, "fi\n") {
		return fmt.Errorf("do not use shell if/then blocks in QA — use simple CMD: lines and JSON outcomes")
	}
	if err := validateImplementationBeadPlaceholder(cmd, "", rig); err != nil {
		return err
	}
	if strings.Contains(lower, "unittest") && (strings.Contains(lower, "| grep") || strings.Contains(lower, "if [")) {
		return fmt.Errorf("run unittest as a single CMD (e.g. cd %s && python3 -m unittest %s -v); do not use pipes or shell if-blocks", rigMayorRigPath(rig), v.UnittestModule)
	}
	tr := strings.ToLower(strings.TrimSpace(v.TestRunner))
	if strings.Contains(lower, "pytest") && (strings.Contains(lower, "| grep") || strings.Contains(lower, "if [")) {
		return fmt.Errorf("run pytest as a single CMD from %s; do not use pipes or shell if-blocks", rigMayorRigPath(rig))
	}
	if strings.Contains(lower, "pytest") && tr != "pytest" && tr != "custom" {
		return fmt.Errorf("pytest not allowed for this workflow test_runner=%q — use %s or update rig workflow profile", v.TestRunner, v.QAVerifyHint())
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
	if path, mutates := orchestrator.QACommandMutatesLayoutSource(cmd, v); mutates {
		return fmt.Errorf("QA must not modify implementation files (blocked write to %q) — send outcome failure with bead IDs so the polecat fixes handlers/web; do not sed or redirect-edit under %s", path, strings.TrimSpace(v.LayoutRoot))
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
	var result []orchestrator.PlanBead
	seen := map[string]bool{}
	for _, status := range []string{"open", "in_progress"} {
		args := beads.InjectFlatForListJSON([]string{"list", "--status=" + status, "--json", "--limit=0"})
		cmd := exec.Command("bd", args...)
		cmd.Env = withEnvKey(os.Environ(), "BEADS_DIR", beadsDir)
		cmd.Dir = rigMayorRigDir(townRoot, rig)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("bd list %s: %w: %s", status, err, strings.TrimSpace(string(out)))
		}
		out = beads.StripStdoutWarnings(out)
		var rows []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		}
		if err := json.Unmarshal(out, &rows); err != nil {
			return nil, fmt.Errorf("parse %s beads: %w", status, err)
		}
		for _, r := range rows {
			id := strings.TrimSpace(beads.ExtractIssueID(r.ID))
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			result = append(result, orchestrator.PlanBead{ID: id, Title: strings.TrimSpace(r.Title)})
		}
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
	if err := orchestrator.ValidatePlanningPhaseGate(townRoot, rig, "plan_review", v); err != nil {
		return err
	}
	return nil
}

func countOpenMatchingBeads(townRoot, rig string, v orchestrator.WorkflowValidation) (int, error) {
	if countOpenMatchingBeadsHook != nil {
		return countOpenMatchingBeadsHook(townRoot, rig, v)
	}
	active, err := orchestrator.ListImplementBeadsOpenOrInProgress(townRoot, rig, v)
	if err != nil {
		return 0, err
	}
	return len(active), nil
}

func beadIDExample(townRoot, rig string) string {
	prefix, err := orchestrator.RigIssuePrefix(townRoot, rig)
	if err != nil || prefix == "" {
		return "<id-from-bd-list>"
	}
	return prefix + "-xxx"
}

func validateQAArtifacts(townRoot, rig, outcome string, hadCmdFailure, bdListClosedOK, unittestOK, qaSmokeOK bool, v orchestrator.WorkflowValidation) error {
	scoped := v.ForActivePhase()
	sendToImpl := outcome == "failure"
	sendToArchitect := outcome == "architecture_failure"
	if hadCmdFailure && !sendToImpl && !sendToArchitect {
		return fmt.Errorf("QA step had failed commands; fix errors before completing")
	}
	if !bdListClosedOK {
		return fmt.Errorf("run `bd list --status=closed` from %s before reporting QA outcome", rigMayorRigPath(rig))
	}
	if sendToArchitect {
		if !unittestOK {
			return fmt.Errorf("architecture_failure requires green %s in this session — use outcome failure for test failures", scoped.QAVerifyHint())
		}
		if requiresQARuntimeSmoke(townRoot, rig, scoped) && qaSmokeOK {
			return fmt.Errorf("architecture_failure requires failed runtime smoke while unit tests pass — use all_passed if smoke passed")
		}
		if err := validateRequiredWorkFiles(townRoot, rig, scoped); err != nil {
			return err
		}
		if err := orchestrator.ValidateWorkNotStubbed(rigMayorRigDir(townRoot, rig), scoped); err != nil {
			return fmt.Errorf("stub/placeholder code cannot use architecture_failure — use outcome failure: %w", err)
		}
		return nil
	}
	if sendToImpl && unittestOK && requiresQARuntimeSmoke(townRoot, rig, scoped) && !qaSmokeOK {
		return fmt.Errorf("unit tests passed but runtime smoke failed — if implementation matches architecture.md, use outcome architecture_failure (architect revises design); use failure only for code bugs")
	}
	if !sendToImpl {
		if !unittestOK {
			return fmt.Errorf("run `%s` from %s before reporting QA outcome", scoped.QAVerifyHint(), rigMayorRigPath(rig))
		}
		if err := validateRequiredWorkFiles(townRoot, rig, scoped); err != nil {
			return err
		}
		if err := orchestrator.ValidateWorkNotStubbed(rigMayorRigDir(townRoot, rig), scoped); err != nil {
			return fmt.Errorf("implementation files look like stubs (QA must use outcome failure): %w", err)
		}
		if err := validateWebStaticReferences(townRoot, rig, scoped); err != nil {
			return err
		}
		if requiresQARuntimeSmoke(townRoot, rig, scoped) && !qaSmokeOK {
			return fmt.Errorf("QA requires a successful runtime smoke CMD in this gt-agent session before all_passed (qa-review-progress.json does not count); probes come from SPEC/architecture only — no invented API routes")
		}
	}
	if sendToImpl {
		if err := validateRequiredWorkFiles(townRoot, rig, scoped); err != nil {
			return err
		}
	}
	switch outcome {
	case "all_passed", "task_passed":
		openImpl, err := countOpenMatchingBeads(townRoot, rig, scoped)
		if err != nil {
			return err
		}
		if outcome == "all_passed" && openImpl > 0 {
			return fmt.Errorf("cannot use all_passed: %d open implement bead(s) remain for active phase", openImpl)
		}
		if outcome == "task_passed" && openImpl == 0 {
			return fmt.Errorf("use all_passed when no open implement beads remain for active phase")
		}
	}
	return nil
}

func requiresQARuntimeSmoke(townRoot, rig string, v orchestrator.WorkflowValidation) bool {
	return orchestrator.WorkflowNeedsQARuntimeSmoke(townRoot, rig, v)
}

func isQARuntimeSmokeCommandOK(cmd, townRoot, rig string, v orchestrator.WorkflowValidation) bool {
	if !requiresQARuntimeSmoke(townRoot, rig, v) {
		return false
	}
	if orchestrator.IsProfileDerivedSmokeCommand(cmd) {
		return true
	}
	lower := strings.ToLower(strings.Join(strings.Fields(cmd), " "))
	if orchestrator.WorkflowUsesPython(v) {
		if orchestrator.IsProfileDerivedSmokeCommand(cmd) {
			return true
		}
		return strings.Contains(lower, "curl ") &&
			(strings.Contains(lower, "localhost") || strings.Contains(lower, "127.0.0.1"))
	}
	if !strings.Contains(lower, "go run") || !strings.Contains(lower, "cmd/server") {
		return false
	}
	if !strings.Contains(lower, "curl ") && !strings.Contains(lower, ".gt-smoke.pid") {
		return false
	}
	if !strings.Contains(lower, "localhost") && !strings.Contains(lower, "127.0.0.1") {
		return false
	}
	spec, _ := orchestrator.LoadAPISmokeSpecFromRig(townRoot, rig, v)
	if orchestrator.APISmokeHasHTTPAPI(spec) {
		if !strings.Contains(lower, " /api/") && !strings.Contains(lower, "/api/") {
			return false
		}
		if !strings.Contains(lower, "post") && !strings.Contains(lower, " -d ") && !strings.Contains(lower, " --data") {
			return false
		}
	} else {
		// SPEC has no API table — do not count smoke that probes invented /api/ routes.
		if strings.Contains(lower, "/api/") {
			return false
		}
		if strings.Contains(lower, " -x post") || strings.Contains(lower, " -d ") || strings.Contains(lower, " --data") {
			return false
		}
	}
	return true
}

var htmlAttrRefRE = regexp.MustCompile(`(?i)\b(src|href)\s*=\s*["']([^"'#][^"']*)["']`)

func validateWebStaticReferences(townRoot, rig string, v orchestrator.WorkflowValidation) error {
	rigDir := rigMayorRigDir(townRoot, rig)
	staticMap := orchestrator.LoadWebStaticMappingFromRig(townRoot, rig, v)
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
				if hint := staticMap.StaticRefMismatchHint(ref); hint != "" {
					return fmt.Errorf("HTML references %q from %s: %s", ref, rel, hint)
				}
				if !webRefExists(webRoot, rel, ref, staticMap) {
					return fmt.Errorf("HTML references missing static asset %q from %s; fix the path or add the file under web/", ref, rel)
				}
				continue
			}
			if attr == "href" && isLocalPageRef(ref) && !webPageRefExists(webRoot, rel, ref, staticMap) && !goServerDefinesRoute(rigDir, v, ref) {
				return fmt.Errorf("HTML link %q in %s has no matching static page or server route; use an in-page anchor for SPA sections", ref, rel)
			}
		}
	}
	if err := validateJSDOMReferencesMatchHTML(rigDir, v); err != nil {
		return err
	}
	if err := validateFrontendJSIntegration(rigDir, v); err != nil {
		return err
	}
	return nil
}

var jsGlobalFuncDefRE = regexp.MustCompile(`(?m)^function\s+([A-Z][A-Za-z0-9_]*)\s*\(`)
var jsGlobalFuncRefRE = regexp.MustCompile(`(?:window\.)?([A-Z][A-Za-z0-9_]*)\s*[\(\.]`)

func validateFrontendJSIntegration(rigDir string, v orchestrator.WorkflowValidation) error {
	defined := make(map[string]string)
	referenced := make(map[string]string)
	for _, rel := range v.ForActivePhase().RequiredFiles {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if !strings.HasSuffix(strings.ToLower(rel), ".js") || !strings.Contains(rel, "/game/") {
			continue
		}
		abs := filepath.Join(rigDir, filepath.FromSlash(rel))
		body, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		text := string(body)
		for _, m := range jsGlobalFuncDefRE.FindAllStringSubmatch(text, -1) {
			if len(m) >= 2 {
				defined[m[1]] = rel
			}
		}
		for _, m := range jsGlobalFuncRefRE.FindAllStringSubmatch(text, -1) {
			name := m[1]
			if name != "Math" && name != "JSON" && name != "Object" && name != "Array" && name != "Date" &&
				!strings.Contains(text, "function "+name+"(") {
				if _, ok := referenced[name]; !ok {
					referenced[name] = rel
				}
			}
		}
	}
	var issues []string
	for name, refFile := range referenced {
		if _, ok := defined[name]; !ok {
			issues = append(issues, fmt.Sprintf("%s references %s() but no file defines function %s", refFile, name, name))
		}
	}
	if len(issues) > 0 {
		return fmt.Errorf("frontend JS integration: %s", strings.Join(issues, "; "))
	}
	return nil
}

var jsGetElementByIDRE = regexp.MustCompile(`(?i)getElementById\s*\(\s*["']([^"']+)["']\s*\)`)

func validateJSDOMReferencesMatchHTML(rigDir string, v orchestrator.WorkflowValidation) error {
	htmlIDs := make(map[string]string)
	for _, rel := range webHTMLRequiredFiles(v) {
		abs := filepath.Join(rigDir, filepath.FromSlash(rel))
		body, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		for _, m := range htmlIDRE.FindAllStringSubmatch(string(body), -1) {
			if len(m) >= 2 {
				htmlIDs[strings.TrimSpace(m[1])] = rel
			}
		}
	}
	for _, rel := range v.RequiredFilesForSmokeScope() {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if !strings.HasSuffix(strings.ToLower(rel), ".js") || !strings.Contains(rel, "/web/") {
			continue
		}
		abs := filepath.Join(rigDir, filepath.FromSlash(rel))
		body, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		for _, m := range jsGetElementByIDRE.FindAllStringSubmatch(string(body), -1) {
			if len(m) >= 2 {
				id := strings.TrimSpace(m[1])
				if _, ok := htmlIDs[id]; !ok {
					return fmt.Errorf("%s references DOM id %q not found in any HTML file; check index.html for the correct id", rel, id)
				}
			}
		}
	}
	return nil
}

var htmlIDRE = regexp.MustCompile(`(?i)\bid\s*=\s*["']([^"']+)["']`)

func webHTMLRequiredFiles(v orchestrator.WorkflowValidation) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range v.RequiredFilesForSmokeScope() {
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

func webRefExists(webRoot, htmlRel, ref string, m orchestrator.WebStaticMapping) bool {
	path := webRefPath(webRoot, htmlRel, ref, m)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func webPageRefExists(webRoot, htmlRel, ref string, m orchestrator.WebStaticMapping) bool {
	path := webRefPath(webRoot, htmlRel, ref, m)
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

func webRefPath(webRoot, htmlRel, ref string, m orchestrator.WebStaticMapping) string {
	return m.WebDiskPathForURLRef(webRoot, htmlRel, ref)
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
		path := orchestrator.ResolveRequiredFileOnDisk(rigDir, rel, v.LayoutRoot)
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("missing %s (implement and commit before success)", path)
		}
		if info.Size() == 0 {
			return fmt.Errorf("%s is empty", path)
		}
		if strings.HasSuffix(filepath.ToSlash(rel), "/go.mod") || rel == "go.mod" {
			if err := orchestrator.ValidateGoModFileForBeadClose(rigDir, v); err != nil {
				return err
			}
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
	if err := orchestrator.ValidatePlanningPhaseGate(townRoot, rig, "design", v); err != nil {
		return err
	}
	return nil
}

func buildOrchestratedSystemPrompt(task *orchestrator.Task, townRoot string) string {
	vars := orchestratorPromptVars(task, townRoot)
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
		if section := task.Hooks.NativeEditPromptSection(); section != "" {
			b.WriteString("1. Edit source files with native tools (see below); use `CMD:` for bd/verify only.\n")
			b.WriteString("2. After work succeeds, send a **separate** message with JSON only (no CMD/native tools in that message):\n")
			b.WriteString(`{"outcome":"<one allowed outcome>","summary":"<brief result>"}`)
			b.WriteString("\n\n")
			b.WriteString(section)
			b.WriteString("\n")
		} else {
			b.WriteString("1. Run shell work as `CMD: <command>` lines (use a single heredoc CMD for multi-line files).\n")
			b.WriteString("2. After commands succeed, send a **separate** message with JSON only (no CMD lines in that message):\n")
			b.WriteString(`{"outcome":"<one allowed outcome>","summary":"<brief result>"}`)
			b.WriteString("\nDo not put JSON on the same line as CMD. Do not use `cat > file` without a heredoc body.\n")
		}
	}
	if footer := task.Hooks.SystemPromptFooterText(vars); footer != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(footer)
	}
	return b.String()
}

func orchestratorPromptVars(task *orchestrator.Task, townRoot string) map[string]string {
	vars := map[string]string{"rig": task.Rig}
	v := taskValidation(townRoot, task)
	for k, val := range v.PromptVars() {
		vars[k] = val
	}
	if townRoot != "" && task.Rig != "" {
		vars["qa_runtime_smoke_block"] = orchestrator.RigFlowQARuntimeSmokeBlock(townRoot, task.Rig, v)
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
