package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/steveyegge/gastown/cmd/gt-agent/internal/llm"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/orchestrator"
)

const (
	defaultOrchPollInterval        = 15 * time.Second
	maxOrchestratedCmdTurns        = 5
	maxOrchestratedRetryFeedback   = 6000 // chars persisted for next fetch_task attempt
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
	fmt.Printf("[gt-agent] ORCHESTRATED mode agent_id=%q poll=%s (no LLM while idle)\n", agentID, pollEvery)

	for {
		if isShutdownRequested() {
			break
		}

		task, err := orchestrator.FetchTask(townRoot, agentID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[gt-agent] fetch_task error: %v\n", err)
			time.Sleep(pollEvery)
			continue
		}
		if task == nil {
			time.Sleep(pollEvery)
			continue
		}

		fmt.Printf("[gt-agent] Task wf=%s template=%s state=%s\n",
			task.WorkflowID, task.TemplateID, task.State)

		outcome, summary, attemptLog, runErr := executeOrchestratedTask(ctx, client, townRoot, rig, sessionName, task, state.OrchestratedRetry)
		if runErr != nil {
			fmt.Fprintf(os.Stderr, "[gt-agent] task execution: %v\n", runErr)
			if outcome == "" {
				outcome = "fail"
			}
		}
		if outcome == "" {
			outcome = "fail"
		}

		fmt.Printf("[gt-agent] complete_task outcome=%q summary=%q\n", outcome, summary)
		agentID := orchestrator.OrchestratorAgentID(role, rig)
		nextState, err := orchestrator.CompleteTask(townRoot, task.WorkflowID, outcome, agentID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[gt-agent] complete_task failed: %v\n", err)
			updateOrchestratedRetry(&state, task, "failure", err.Error(), attemptLog)
		} else {
			fmt.Printf("[gt-agent] next state: %s\n", nextState)
			updateOrchestratedRetry(&state, task, outcome, summary, attemptLog)
		}

		state.LastActivity = time.Now()
		_ = saveState(stateFile, state)
		time.Sleep(2 * time.Second)
	}

	return nil
}

func executeOrchestratedTask(ctx context.Context, client *llm.Client, townRoot, rig, sessionName string, task *orchestrator.Task, priorRetry *OrchestratedRetry) (outcome, summary, attemptLog string, err error) {
	systemPrompt := buildOrchestratedSystemPrompt(task)
	userPrompt := buildOrchestratedUserPrompt(task)
	if block := formatOrchestratedRetryBlock(priorRetry, task, rig); block != "" {
		userPrompt = block + "\n\n" + userPrompt
		fmt.Printf("[gt-agent] injecting prior failure context for %s/%s\n", task.WorkflowID, task.State)
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
	var implementationHadCmdFailure bool
	var implementationBeadCloseOK bool

	for turn := 1; turn <= maxOrchestratedCmdTurns; turn++ {
		response, llmErr := client.CompleteMessages(ctx, messages)
		if llmErr != nil {
			return "fail", "", lastAttemptFeedback.String(), llmErr
		}
		fmt.Printf("[gt-agent] LLM response (turn %d):\n%s\n", turn, response)
		messages = append(messages, llm.Message{Role: "assistant", Content: response})

		cmdBlocks := parseOrchestratedCommands(response)
		if len(cmdBlocks) > 0 {
			var combined strings.Builder
			for _, cmd := range cmdBlocks {
				if task.State == "design" {
					if err := validateDesignCommand(cmd, rig); err != nil {
						fmt.Fprintf(os.Stderr, "[gt-agent] rejected command: %v\n", err)
						combined.WriteString(fmt.Sprintf("Command REJECTED (architect scope): %s\nReason: %v\n\n", cmd, err))
						continue
					}
				}
				if task.State == "planning" {
					if err := validatePlanningCommand(cmd, rig); err != nil {
						fmt.Fprintf(os.Stderr, "[gt-agent] rejected command: %v\n", err)
						combined.WriteString(fmt.Sprintf("Command REJECTED (planner scope): %s\nReason: %v\n\n", cmd, err))
						continue
					}
				}
				if task.State == "implementation" {
					if err := validateImplementationCommand(cmd, rig); err != nil {
						fmt.Fprintf(os.Stderr, "[gt-agent] rejected command: %v\n", err)
						combined.WriteString(fmt.Sprintf("Command REJECTED (polecat scope): %s\nReason: %v\n\n", cmd, err))
						continue
					}
				}
				if fixed, ok := rewriteSpecMDPathCaseInsensitive(cmd); ok {
					cmd = fixed
				}
				if task.State == "planning" {
					if fixed, ok := rewritePlanMDPathAfterCD(cmd, rig); ok {
						fmt.Printf("[gt-agent] rewrote plan.md path after cd: %s\n", fixed)
						cmd = fixed
					}
				}
				if task.State == "implementation" {
					if fixed, ok := rewriteBackendPathAfterCD(cmd, rig); ok {
						fmt.Printf("[gt-agent] rewrote backend path after cd: %s\n", fixed)
						cmd = fixed
					}
				}
				fmt.Printf("[gt-agent] $ %s\n", cmd)
				cmdEnv := orchestratedCommandEnv(townRoot, rig, task.State, os.Environ())
				if needsOrchestratedScriptFile(cmd) {
					fmt.Printf("[gt-agent] running multiline/heredoc via temp script\n")
				}
				out, cmdErr := runOrchestratedCommand(cmd, townRoot, sessionName, cmdEnv)
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
				}
				if cmdErr != nil {
					fmt.Fprintf(os.Stderr, "[gt-agent] command failed: %v\n%s\n", cmdErr, string(out))
					combined.WriteString(fmt.Sprintf("Command: %s\nError: %v\nOutput: %s\n\n", cmd, cmdErr, string(out)))
				} else {
					fmt.Printf("[gt-agent] output:\n%s\n", string(out))
					combined.WriteString(string(out))
				}
			}
			feedback := combined.String()
			recordAttemptFeedback(feedback)
			feedback += "\n\nCommands executed. If the step is complete, reply with JSON only (no CMD lines): {\"outcome\":\"...\",\"summary\":\"...\"}"
			if turn == maxOrchestratedCmdTurns {
				feedback += " Use an allowed outcome."
			}
			// Same turn may include CMD lines and JSON; accept success when artifacts are ready.
			if o, s, ok := parseOrchestratedResult(response, task.AllowedOutcomes); ok {
				if vErr := validateOrchestratedArtifacts(task, townRoot, rig, o, designArchWrittenThisRun, planningHadCmdFailure, planningBeadCreateOK, implementationHadCmdFailure, implementationBeadCloseOK); vErr != nil {
					fmt.Printf("[gt-agent] artifact validation failed: %v\n", vErr)
					recordAttemptFeedback("Validation failed: " + vErr.Error() + "\n")
				} else {
					return o, s, lastAttemptFeedback.String(), nil
				}
			}
			if o, s, ok := orchestratedArtifactAutoOutcome(task, townRoot, rig, designArchWrittenThisRun, planningHadCmdFailure, planningBeadCreateOK); ok {
				return o, s, lastAttemptFeedback.String(), nil
			}
			messages = append(messages, llm.Message{Role: "user", Content: feedback})
			continue
		}

		// Outcome is only accepted on a turn with no CMD lines (after work is done).
		if o, s, ok := parseOrchestratedResult(response, task.AllowedOutcomes); ok {
			if vErr := validateOrchestratedArtifacts(task, townRoot, rig, o, designArchWrittenThisRun, planningHadCmdFailure, planningBeadCreateOK, implementationHadCmdFailure, implementationBeadCloseOK); vErr != nil {
				fmt.Printf("[gt-agent] artifact validation failed: %v\n", vErr)
				hint := "Use CMD: with a heredoc to write files, then send JSON outcome."
				if task.State == "design" {
					hint = "Write architecture.md with a heredoc CMD in this session (stale files from prior runs do not count). Read SPEC with head -n 60."
				}
				if task.State == "planning" {
					hint = "Write plan.md (heredoc) and create beads with `export BEADS_DIR=$GT_ROOT/RIG/.beads && cd RIG/mayor/rig && bd create --type task --title \"...\"`. Do not use gt bd add. Do not write backend/ code or run git."
				}
				if task.State == "implementation" {
					hint = "Use bare `bd` from RIG/mayor/rig: list open beads, implement under backend/, `bd close` at least one bead, then success. Never use gt bd."
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

	return "fail", "", lastAttemptFeedback.String(), fmt.Errorf("no structured outcome after %d turns", maxOrchestratedCmdTurns)
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
	b.WriteString(orchestratedRetryHintsForState(task.State, rig))
	return b.String()
}

// orchestratedRetryHintsForState returns step-specific guidance after a failed attempt.
func orchestratedRetryHintsForState(state, rig string) string {
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
		return "One CMD: per line. Run `mkdir -p backend` before creating backend files. " +
			"Use real te-xxx bead IDs from bd list output — do not invent IDs.\n"
	case "qa_review":
		return "One CMD: per line. Review closed beads against SPEC and architecture; use allowed QA outcomes only.\n"
	default:
		return "One CMD: per line.\n"
	}
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
	// Un-glue EOF'CMD: and similar before line-oriented parsing (polecat heredoc bursts).
	filtered = normalizeGluedCMDMarkers(filtered)
	cmds, _, _ := parseLLMResponse(filtered)
	return cmds
}

func rigMayorRigDir(townRoot, rig string) string {
	rigName := rig
	if rigName == "" {
		rigName = "testgt2"
	}
	return filepath.Join(townRoot, rigName, "mayor", "rig")
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
	return info.Size() >= minPlanMDBytes
}

// rewriteBackendPathAfterCD fixes polecat writing rig/mayor/rig/backend/... after cd into worktree.
func rewriteBackendPathAfterCD(cmd, rig string) (string, bool) {
	rigName := rig
	if rigName == "" {
		rigName = "testgt2"
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
	rigName := rig
	if rigName == "" {
		rigName = "testgt2"
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
	rigPrefix := rig
	if rigPrefix == "" {
		rigPrefix = "testgt2"
	}
	rigSlash := strings.ToLower(rigPrefix) + "/"

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
			return fmt.Errorf("may only write architecture.md under %s/mayor/rig/", rigPrefix)
		}
	}
	return nil
}

func isPlanningReadOnlyCmd(cmd string) bool {
	lower := strings.ToLower(strings.TrimSpace(cmd))
	if strings.Contains(lower, "<<") || strings.Contains(lower, ">") {
		return false
	}
	readPrefixes := []string{"head ", "tail ", "cat ", "wc ", "ls ", "stat ", "test ", "grep ", "less ", "more ", "find "}
	for _, p := range readPrefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}

func commandWritesBackend(lower string) bool {
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
	rigPrefix := rig
	if rigPrefix == "" {
		rigPrefix = "testgt2"
	}
	rigSlash := strings.ToLower(rigPrefix) + "/"

	if isPlanMDHeredoc(cmd) {
		return validatePlanningPlanHeredoc(lower)
	}

	if strings.Contains(lower, "gt bd add") || strings.Contains(lower, "bd add") {
		return fmt.Errorf("use `cd %s/mayor/rig && bd create --type task --title \"...\"` (gt bd is not the bd CLI)", rigPrefix)
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
		if strings.Contains(lower, rigSlash) || strings.Contains(lower, "mayor/rig/") {
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

const minPlanMDBytes = 200

// validateOrchestratedArtifacts rejects false-success when required files are missing or empty.
func validateOrchestratedArtifacts(task *orchestrator.Task, townRoot, rig, outcome string, designArchWrittenThisRun, planningHadCmdFailure, planningBeadCreateOK, implementationHadCmdFailure, implementationBeadCloseOK bool) error {
	if outcome != "success" && outcome != "task_passed" && outcome != "all_passed" {
		return nil
	}
	switch task.State {
	case "design":
		return validateDesignArtifacts(townRoot, rig, designArchWrittenThisRun)
	case "planning":
		return validatePlanningArtifacts(townRoot, rig, planningHadCmdFailure, planningBeadCreateOK)
	case "implementation":
		return validateImplementationArtifacts(townRoot, rig, implementationHadCmdFailure, implementationBeadCloseOK)
	}
	return nil
}

func validatePlanningArtifacts(townRoot, rig string, hadCmdFailure, beadCreateOK bool) error {
	if hadCmdFailure {
		return fmt.Errorf("planning step had failed commands; fix errors before completing")
	}
	if !beadCreateOK {
		return fmt.Errorf("at least one successful `bd create` in %s is required", rigMayorRigPath(rig))
	}
	path := filepath.Join(rigMayorRigDir(townRoot, rig), "plan.md")
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("plan.md missing at %s", path)
	}
	if info.Size() < minPlanMDBytes {
		return fmt.Errorf("plan.md too small (%d bytes); need ≥%d", info.Size(), minPlanMDBytes)
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
	if rig == "" || townRoot == "" {
		return base
	}
	switch taskState {
	case "planning", "implementation":
	default:
		return base
	}
	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	return withEnvKey(base, "BEADS_DIR", beadsDir)
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
	cmd := exec.Command("bd", "list", "--status=open", "--json")
	cmd.Env = withEnvKey(os.Environ(), "BEADS_DIR", beadsDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("rig beads check (BEADS_DIR=%s): %w: %s", beadsDir, err, strings.TrimSpace(string(out)))
	}
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

func validateImplementationArtifacts(townRoot, rig string, hadCmdFailure, beadCloseOK bool) error {
	if hadCmdFailure {
		return fmt.Errorf("implementation step had failed commands; fix errors before completing")
	}
	if !beadCloseOK {
		return fmt.Errorf("at least one successful `bd close` in %s is required before success", rigMayorRigPath(rig))
	}
	return nil
}

func rigMayorRigPath(rig string) string {
	if rig == "" {
		return "testgt2/mayor/rig"
	}
	return rig + "/mayor/rig"
}

func validateDesignArtifacts(townRoot, rig string, writtenThisRun bool) error {
	if !writtenThisRun {
		return fmt.Errorf("architecture.md must be written in this design step (heredoc CMD); stale files from prior runs are ignored")
	}
	rigDir := rigMayorRigDir(townRoot, rig)
	archPath := filepath.Join(rigDir, "architecture.md")
	info, err := os.Stat(archPath)
	if err != nil {
		return fmt.Errorf("architecture.md missing at %s", archPath)
	}
	if info.Size() < 200 {
		return fmt.Errorf("architecture.md too small (%d bytes); need ≥200", info.Size())
	}
	// Stale backend/*.py from a prior polecat run must not block design completion.
	// Reject implementation files if architect dropped them next to architecture.md
	for _, name := range []string{"fizzbuzz.py", "main.py", "test_fizzbuzz.py"} {
		if _, err := os.Stat(filepath.Join(rigDir, name)); err == nil {
			return fmt.Errorf("implementation file %q must not exist in mayor/rig/ (only architecture.md)", name)
		}
	}
	return nil
}

// orchestratedArtifactAutoOutcome completes a step when required files exist after CMD work,
// even if the model never sends a clean JSON-only turn (common with small local LLMs).
func orchestratedArtifactAutoOutcome(task *orchestrator.Task, townRoot, rig string, designArchWrittenThisRun, planningHadCmdFailure, planningBeadCreateOK bool) (outcome, summary string, ok bool) {
	var vErr error
	switch task.State {
	case "design":
		vErr = validateDesignArtifacts(townRoot, rig, designArchWrittenThisRun)
	case "planning":
		vErr = validateOrchestratedArtifacts(task, townRoot, rig, "success", designArchWrittenThisRun, planningHadCmdFailure, planningBeadCreateOK, false, false)
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
	fmt.Printf("[gt-agent] auto-completing %s: artifacts satisfied\n", task.State)
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
	candidates := []string{response}
	if m := jsonBlockRE.FindStringSubmatch(response); len(m) > 1 {
		candidates = append([]string{m[1]}, candidates...)
	}
	for _, raw := range candidates {
		raw = strings.TrimSpace(raw)
		var r orchestratedTaskResult
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			continue
		}
		if r.Outcome != "" {
			return r.Outcome, r.Summary, true
		}
	}
	return "", "", false
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
