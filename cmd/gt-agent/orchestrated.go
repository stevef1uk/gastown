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
	"github.com/steveyegge/gastown/internal/orchestrator"
)

const (
	defaultOrchPollInterval = 15 * time.Second
	maxOrchestratedCmdTurns = 5
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

		outcome, summary, runErr := executeOrchestratedTask(ctx, client, townRoot, rig, sessionName, task)
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
		nextState, err := orchestrator.CompleteTask(townRoot, task.WorkflowID, outcome)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[gt-agent] complete_task failed: %v\n", err)
		} else {
			fmt.Printf("[gt-agent] next state: %s\n", nextState)
		}

		state.LastActivity = time.Now()
		_ = saveState(stateFile, state)
		time.Sleep(2 * time.Second)
	}

	return nil
}

func executeOrchestratedTask(ctx context.Context, client *llm.Client, townRoot, rig, sessionName string, task *orchestrator.Task) (outcome, summary string, err error) {
	systemPrompt := buildOrchestratedSystemPrompt(task)
	userPrompt := buildOrchestratedUserPrompt(task)

	messages := []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	var planningHadCmdFailure bool
	var planningBeadCreateOK bool

	for turn := 1; turn <= maxOrchestratedCmdTurns; turn++ {
		response, llmErr := client.CompleteMessages(ctx, messages)
		if llmErr != nil {
			return "fail", "", llmErr
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
				fmt.Printf("[gt-agent] $ %s\n", cmd)
				c := exec.Command("/bin/sh", "-c", cmd)
				c.Env = os.Environ()
				if sessionName != "" {
					c.Env = append(c.Env, "GT_SESSION="+sessionName)
				}
				c.Dir = townRoot
				out, cmdErr := c.CombinedOutput()
				if task.State == "planning" {
					if cmdErr != nil {
						planningHadCmdFailure = true
					}
					if isBeadCreateCommand(cmd) && cmdErr == nil {
						planningBeadCreateOK = true
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
			feedback += "\n\nCommands executed. If the step is complete, reply with JSON only (no CMD lines): {\"outcome\":\"...\",\"summary\":\"...\"}"
			if turn == maxOrchestratedCmdTurns {
				feedback += " Use an allowed outcome."
			}
			// Same turn may include CMD lines and JSON; accept success when artifacts are ready.
			if o, s, ok := parseOrchestratedResult(response, task.AllowedOutcomes); ok {
				if vErr := validateOrchestratedArtifacts(task, townRoot, rig, o, planningHadCmdFailure, planningBeadCreateOK); vErr != nil {
					fmt.Printf("[gt-agent] artifact validation failed: %v\n", vErr)
				} else {
					return o, s, nil
				}
			}
			if o, s, ok := orchestratedArtifactAutoOutcome(task, townRoot, rig, planningHadCmdFailure, planningBeadCreateOK); ok {
				return o, s, nil
			}
			messages = append(messages, llm.Message{Role: "user", Content: feedback})
			continue
		}

		// Outcome is only accepted on a turn with no CMD lines (after work is done).
		if o, s, ok := parseOrchestratedResult(response, task.AllowedOutcomes); ok {
			if vErr := validateOrchestratedArtifacts(task, townRoot, rig, o, planningHadCmdFailure, planningBeadCreateOK); vErr != nil {
				fmt.Printf("[gt-agent] artifact validation failed: %v\n", vErr)
				hint := "Use CMD: with a heredoc to write files, then send JSON outcome."
				if task.State == "design" {
					hint = "Write only architecture.md (heredoc). You may mention backend/ in the doc. Do not create backend/ or run git. Read SPEC with head -n 60."
				}
				if task.State == "planning" {
					hint = "Write plan.md (heredoc) and create beads with `cd RIG/mayor/rig && bd create --type task --title \"...\"`. Do not use gt bd add. Do not write backend/ code or run git."
				}
				messages = append(messages, llm.Message{Role: "user", Content: "Validation failed: " + vErr.Error() + ". " + hint})
				continue
			}
			return o, s, nil
		}

		messages = append(messages, llm.Message{Role: "user", Content: "Use CMD: lines to run shell commands (heredoc for multi-line files). When done, reply with JSON only: {\"outcome\":\"...\",\"summary\":\"...\"}"})
	}

	return "fail", "", fmt.Errorf("no structured outcome after %d turns", maxOrchestratedCmdTurns)
}

// outcomeJSONTailRE strips outcome JSON glued onto the end of a CMD line.
var outcomeJSONTailRE = regexp.MustCompile(`(?i)\s*\{[\s]*"outcome"[\s\S]*$`)

// stripOutcomeLines removes JSON/outcome lines so they are not fed into shell scripts.
func stripOutcomeLinesForCmdParse(response string) string {
	var kept []string
	for _, line := range strings.Split(response, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			kept = append(kept, line)
			continue
		}
		if strings.HasPrefix(t, "{") && strings.Contains(strings.ToLower(t), "outcome") {
			continue
		}
		if strings.HasPrefix(strings.ToUpper(t), "OUTCOME:") {
			continue
		}
		if trimmed := outcomeJSONTailRE.ReplaceAllString(line, ""); trimmed != line {
			line = strings.TrimRight(trimmed, " \t")
			t = strings.TrimSpace(line)
			if t == "" {
				continue
			}
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// parseOrchestratedCommands extracts CMD blocks without treating JSON or outcome lines as shell.
func parseOrchestratedCommands(response string) []string {
	filtered := stripOutcomeLinesForCmdParse(response)
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

func isPlanMDHeredoc(cmd string) bool {
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "plan.md") && strings.Contains(lower, "<<")
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
		// Mentioning backend/ inside architecture.md body is allowed.
		if err := validateDesignShellSideEffects(lower); err != nil {
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

// validatePlanningCommand blocks planner scope creep (polecat implements code).
func validatePlanningCommand(cmd, rig string) error {
	lower := strings.ToLower(cmd)
	rigPrefix := rig
	if rigPrefix == "" {
		rigPrefix = "testgt2"
	}
	rigSlash := strings.ToLower(rigPrefix) + "/"

	if isPlanMDHeredoc(cmd) {
		return validatePlanningShellSideEffects(lower)
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
	return nil
}

const minPlanMDBytes = 200

// validateOrchestratedArtifacts rejects false-success when required files are missing or empty.
func validateOrchestratedArtifacts(task *orchestrator.Task, townRoot, rig, outcome string, planningHadCmdFailure, planningBeadCreateOK bool) error {
	if outcome != "success" && outcome != "task_passed" && outcome != "all_passed" {
		return nil
	}
	switch task.State {
	case "design":
		return validateDesignArtifacts(townRoot, rig)
	case "planning":
		return validatePlanningArtifacts(townRoot, rig, planningHadCmdFailure, planningBeadCreateOK)
	}
	return nil
}

func validatePlanningArtifacts(townRoot, rig string, hadCmdFailure, beadCreateOK bool) error {
	if hadCmdFailure {
		return fmt.Errorf("planning step had failed commands; fix errors before completing")
	}
	if !beadCreateOK {
		return fmt.Errorf("at least one successful `bd create` in %s/mayor/rig is required", rigMayorRigPath(rig))
	}
	path := filepath.Join(rigMayorRigDir(townRoot, rig), "plan.md")
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("plan.md missing at %s", path)
	}
	if info.Size() < minPlanMDBytes {
		return fmt.Errorf("plan.md too small (%d bytes); need ≥%d", info.Size(), minPlanMDBytes)
	}
	return nil
}

func rigMayorRigPath(rig string) string {
	if rig == "" {
		return "testgt2/mayor/rig"
	}
	return rig + "/mayor/rig"
}

func validateDesignArtifacts(townRoot, rig string) error {
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
func orchestratedArtifactAutoOutcome(task *orchestrator.Task, townRoot, rig string, planningHadCmdFailure, planningBeadCreateOK bool) (outcome, summary string, ok bool) {
	var vErr error
	switch task.State {
	case "design":
		vErr = validateDesignArtifacts(townRoot, rig)
	case "planning":
		vErr = validateOrchestratedArtifacts(task, townRoot, rig, "success", planningHadCmdFailure, planningBeadCreateOK)
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
