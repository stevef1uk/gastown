package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/steveyegge/gastown/cmd/gt-agent/internal/llm"
	"github.com/steveyegge/gastown/internal/nudge"
	"github.com/steveyegge/gastown/internal/orchestrator"
	"github.com/steveyegge/gastown/internal/templates"
)

// OrchestratedRetry captures the last failed orchestrator step for cross-attempt LLM feedback.
type OrchestratedRetry struct {
	WorkflowID string    `json:"workflow_id"`
	TemplateID string    `json:"template_id,omitempty"`
	State      string    `json:"state"`
	Outcome    string    `json:"outcome"`
	Summary    string    `json:"summary"`
	Feedback   string    `json:"feedback,omitempty"` // truncated shell/cmd output from the failed attempt
	At         time.Time `json:"at"`
}

// AgentState persists across cycles so the agent doesn't lose count
// or context when the daemon respawns it.
type AgentState struct {
	PatrolCount         int                `json:"patrol_count"`
	ExtraordinaryAction bool               `json:"extraordinary_action"`
	LastActivity        time.Time          `json:"last_activity"`
	IdleCycles          int                `json:"idle_cycles"`
	CmdRetryCount       int                `json:"cmd_retry_count"` // consecutive extraordinary retries
	OrchestratedRetry   *OrchestratedRetry `json:"orchestrated_retry,omitempty"`
}

const (
	baseSleep     = 30 * time.Second
	maxSleep      = 5 * time.Minute // cap at 5 min — never wait longer than this
	maxIdleCycles = 20              // exit after 20 idle cycles (~5-30min depending on backoff)
	stateFileName = "gt-agent-state.json"
)

// permanentAgents are roles that should never exit due to idle cycles.
// They run continuously and wait for work (Mayor, Deacon, Witness, Refinery).
// Polecats and crew workers exit when idle since they are task-specific.
var permanentAgents = map[string]bool{
	"mayor":       true,
	"deacon":      true,
	"witness":     true,
	"refinery":    true,
	"planner":     true,
	"mechanic":    true,
	"architect":   true,
	"qa":          true,
	"deacon/boot": true, // boot is a deacon variant
}

var lastBeadID string
var lastMoleculeID string
var lastStepID string
var lastStepShort string
// currentRole is the canonical role (witness, refinery, planner, mayor, etc.)
// for this gt-agent process. It is set once at startup and is read by
// command-normalization guards so they can refuse role-inappropriate
// invocations (e.g. a witness slinging the `shiny` engineering formula).
var currentRole string
// absPathMarkdownRE matches absolute Unix paths ending in .md with at
// least one directory segment (e.g. /home/gt/rig/mayor/rig/spec.md).
// Used to fix case-only mismatches against on-disk SPEC.md (Fix #116 ext).
var absPathMarkdownRE = regexp.MustCompile(`(/[A-Za-z0-9_.@-]+(?:/[A-Za-z0-9_.@-]+)+\.md)`)
var stepIDRe = regexp.MustCompile(`[a-z0-9_-]+/[a-z0-9_-]+/step-[0-9]+`)
var jsonStepRe = regexp.MustCompile(`"step_id"\s*:\s*"([^"]+)"`)
var gtBinaryPath string
var agentTownRoot string

// mechanicPatrolScript is a hardcoded shell script for the mechanic.
// It does NOT use the LLM to avoid hallucination issues.
const mechanicPatrolScript = `#!/bin/sh
TOWN_ROOT="${GT_ROOT:-${GT_TOWN_ROOT:-${TOWN_ROOT:-.}}}"
LOG_DIR="$TOWN_ROOT/logs/sessions"
GT="${GT:-$(command -v gt 2>/dev/null || echo gt)}"

echo "=== Mechanic patrol starting ==="

# Mechanic is town-level: scan ALL recent agent logs (town + every rig).
# Limit to logs touched in the last 60 minutes so we ignore stale sessions.
logs=$(find "$LOG_DIR" -maxdepth 1 -name "*.log" -mmin -60 -type f 2>/dev/null | sort -r | head -20)
if [ -z "$logs" ]; then
  echo "No recent log files found in $LOG_DIR, sleeping..."
  sleep 30
  exit 0
fi

# Track agents that need attention
high_error_agents=""

for logfile in $logs; do
  [ -f "$logfile" ] || continue
  
  # Check for error patterns
  if grep -qi "exit status" "$logfile" 2>/dev/null; then
    # Extract agent name from logfile
    # Format: hq-qsq-<rig>-refinery.log or <prefix>-<rig>-witness.log
    filename=$(basename "$logfile")
    agent=$(echo "$filename" | sed -E 's/^(hq|te)-([^.]+)-(.+)\.log$/\3/' | sed 's/-/./g')
    rig=$(echo "$filename" | sed -E 's/^(hq|te)-([^.]+)-.*/\2/')
    
    echo "=== Checking $filename (agent=$agent, rig=$rig) ==="
    
    # Count exit status errors
    exit_count=$(grep -c "exit status [1-9]" "$logfile" 2>/dev/null || echo 0)
    echo "Found $exit_count exit status errors"
    
    # Check for specific error types
    if grep -q "No such file" "$logfile" 2>/dev/null; then
      echo "Found missing file errors in $agent log"
    fi
    
    if grep -q "prefix mismatch" "$logfile" 2>/dev/null; then
      echo "Found prefix mismatch in $agent log - running gt doctor --fix"
      "$GT" doctor --fix 2>/dev/null || true
    fi
    
    if grep -q "Extraordinary action" "$logfile" 2>/dev/null; then
      echo "Found Extraordinary action in $agent log"
      # Always try to nudge the agent - include rig prefix
      target="$rig/$agent"
      echo "Nudging $target to recover from Extraordinary action..."
      "$GT" nudge "$target" "Mechanic detected Extraordinary action - please recover" 2>/dev/null || true
      echo "Nudge sent to $target"
    fi
    
    if grep -q "Syntax error" "$logfile" 2>/dev/null; then
      echo "Found shell syntax error in $agent - agent may be in bad state"
    fi
    
    # If high error count, flag for attention
    if [ "$exit_count" -gt 5 ]; then
      high_error_agents="$high_error_agents $agent"
      echo "WARNING: $agent has $exit_count errors - needs review"
    fi
    
    # Show last few errors
    echo "Last 3 errors:"
    grep -i "error\|exit status\|failed" "$logfile" 2>/dev/null | tail -3
  fi
done

# Report summary
if [ -n "$high_error_agents" ]; then
  echo "=== HIGH ERROR AGENTS: $high_error_agents ==="
  echo "These agents may need restart or manual intervention"
  echo "Running gt doctor --fix to repair system issues..."
  "$GT" doctor --fix 2>/dev/null || echo "gt doctor --fix completed (some repairs may require restart)"
fi

echo "Patrol complete, sleeping 30 seconds..."
sleep 30
`

// isPermanentAgent returns true if the role should never exit on idle cycles.
func isPermanentAgent(role string) bool {
	if permanentAgents[role] {
		return true
	}
	// Also check for rig-specific roles like "<rig>/witness"
	parts := strings.Split(role, "/")
	last := parts[len(parts)-1]
	return permanentAgents[last]
}

var (
	shutdownRequested bool
	shutdownMu        sync.Mutex
)

func main() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "[gt-agent] panic: %v\n", r)
			debug.PrintStack()
			os.Exit(2)
		}
	}()

	// Handle SIGTERM for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		shutdownMu.Lock()
		shutdownRequested = true
		shutdownMu.Unlock()
		fmt.Println("[gt-agent] SIGTERM received, shutting down gracefully...")
	}()

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "[gt-agent] Fatal: %v\n", err)
		os.Exit(1)
	}
}

func isShutdownRequested() bool {
	shutdownMu.Lock()
	defer shutdownMu.Unlock()
	return shutdownRequested
}

// findGT locates the gt binary. It checks GT_BIN env var, then PATH,
// then common install locations. Always returns an absolute path if found.
func findGT() string {
	if gtBin := os.Getenv("GT_BIN"); gtBin != "" {
		if filepath.IsAbs(gtBin) {
			return gtBin
		}
		if abs, err := filepath.Abs(gtBin); err == nil {
			return abs
		}
		return gtBin
	}
	if path, err := exec.LookPath("gt"); err == nil {
		if !filepath.IsAbs(path) {
			if abs, err := filepath.Abs(path); err == nil {
				return abs
			}
		}
		return path
	}

	var candidates []string
	if exepath, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exepath), "gt"))
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		candidates = append(candidates, filepath.Join(home, ".local/bin/gt"))
	}
	candidates = append(candidates, "/usr/local/bin/gt", "/usr/bin/gt")

	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c
		}
	}
	return "gt"
}

// statePath returns the path to the agent's state file.
// Layout matches internal/agentconsole: town agents under townRoot/<role>/,
// rig singletons under townRoot/<rig>/<role>/, named polecats under polecats/<name>/.
func statePath(townRoot, role, rig, polecat string) string {
	switch role {
	case "deacon", "mayor", "planner", "mechanic":
		return filepath.Join(townRoot, role, stateFileName)
	}
	if rig != "" && polecat != "" {
		return filepath.Join(townRoot, rig, "polecats", polecat, stateFileName)
	}
	if rig != "" {
		if role == "crew" && polecat != "" {
			return filepath.Join(townRoot, rig, "crew", polecat, stateFileName)
		}
		return filepath.Join(townRoot, rig, role, stateFileName)
	}
	return filepath.Join(townRoot, stateFileName)
}

// loadState reads the persisted agent state.
func loadState(path string) AgentState {
	data, err := os.ReadFile(path)
	if err != nil {
		return AgentState{}
	}
	var s AgentState
	if err := json.Unmarshal(data, &s); err != nil {
		return AgentState{}
	}
	return s
}

// saveState writes the agent state to disk.
func saveState(path string, s AgentState) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// sleepDuration returns the backoff sleep based on idle cycles.
func sleepDuration(idleCycles int) time.Duration {
	d := baseSleep * time.Duration(1<<idleCycles)
	if d > maxSleep {
		return maxSleep
	}
	return d
}

type agentIdentityFile struct {
	Role string `json:"role"`
	Rig  string `json:"rig"`
	Name string `json:"name"`
}

func loadAgentFile(path string) *agentIdentityFile {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var f agentIdentityFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil
	}
	return &f
}

func run() error {
	ctx := context.Background()

	// Read identity from environment, then merge .gt-agent if present.
	// Fix #93b: per-bead polecat worktrees may ship a stale .gt-agent (e.g. role
	// refinery committed to main). GT_POLECAT and polecat GT_ROLE must win over
	// .gt-agent role so polecats run mol-polecat-work, not refinery patrol.
	role := os.Getenv("GT_ROLE")
	rig := os.Getenv("GT_RIG")
	polecat := os.Getenv("GT_POLECAT")
	envCanonical := canonicalRole(role)
	isPolecatSession := polecat != "" || envCanonical == "polecat"

	if isPolecatSession {
		role = "polecat"
	}

	if identity := loadAgentFile(".gt-agent"); identity != nil {
		if !isPolecatSession && identity.Role != "" {
			role = identity.Role
		}
		if identity.Rig != "" {
			rig = identity.Rig
		}
		if identity.Name != "" && isPolecatSession {
			polecat = identity.Name
		}
	}

	if role == "" {
		role = "worker"
	}
	roleCanonical := canonicalRole(role)
	currentRole = roleCanonical

	townRoot := os.Getenv("GT_ROOT")
	if townRoot == "" {
		var err error
		townRoot, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("cannot determine town root: %w", err)
		}
	}
	agentTownRoot = townRoot

	// Determine session name for nudge queue
	sessionName := os.Getenv("GT_SESSION")
	if sessionName == "" {
		sessionName = os.Getenv("GT_SESSION_NAME")
	}
	if sessionName == "" {
		if rig != "" && polecat != "" {
			sessionName = fmt.Sprintf("gt-%s-%s", rig, polecat)
		} else if roleCanonical == "mayor" || roleCanonical == "deacon" || roleCanonical == "witness" || roleCanonical == "refinery" || roleCanonical == "planner" || roleCanonical == "mechanic" || roleCanonical == "architect" || roleCanonical == "qa" {
			// For rig-level roles, use the prefix if known
			prefix := rig
			if prefix == "" {
				prefix = "hq"
			}
			sessionName = fmt.Sprintf("%s-%s", prefix, roleCanonical)
		}
	}

	// Locate gt binary
	gtBin := findGT()
	gtBinaryPath = gtBin
	gtDir := filepath.Dir(gtBin)

	// Ensure PATH includes gt and common binary directories
	pathEnv := os.Getenv("PATH")
	if pathEnv == "" {
		pathEnv = "/usr/local/bin:/usr/bin:/bin"
	}
	home, _ := os.UserHomeDir()
	if home == "" && gtDir != "" {
		if parent := filepath.Dir(gtDir); parent != "" {
			home = filepath.Dir(parent)
		}
	}
	goBin := "/usr/local/go/bin"
	if home != "" {
		goBin = filepath.Join(home, "go/bin") + ":" + goBin
	}
	newPath := gtDir + ":" + goBin + ":/usr/local/bin:/usr/bin:/bin:/sbin"
	if !strings.Contains(pathEnv, gtDir) {
		newPath = newPath + ":" + pathEnv
	}
	os.Setenv("PATH", newPath)
	os.Setenv("GT_ROLE", role)
	if rig != "" {
		os.Setenv("GT_RIG", rig)
	}
	if polecat != "" {
		os.Setenv("GT_POLECAT", polecat)
	}

	orchestrated := false
	for _, arg := range os.Args {
		if arg == "--orchestrated" {
			orchestrated = true
			break
		}
	}
	if orchestrated {
		setupOrchestratedOutputMirror(townRoot, roleCanonical, rig)
	}

	printStartup := func(format string, args ...interface{}) {
		if orchestrated {
			orchestratedPrintf(format, args...)
		} else {
			fmt.Printf(format, args...)
		}
	}
	printStartup("[gt-agent] Starting as %s", role)
	if rig != "" {
		printStartup(" (rig=%s", rig)
		if polecat != "" {
			printStartup(", polecat=%s", polecat)
		}
		printStartup(")")
	}
	if orchestrated {
		orchestratedPrintf("\n")
	} else {
		fmt.Println()
	}
	printStartup("[gt-agent] Using gt binary: %s\n", gtBin)
	printStartup("[gt-agent] PATH: %s\n", os.Getenv("PATH"))

	// Load LLM config
	llmEndpoint := os.Getenv("LLM_ENDPOINT")
	if llmEndpoint == "" {
		llmEndpoint = "http://localhost:11434/v1/chat/completions"
	}
	llmTimeoutStr := os.Getenv("LLM_TIMEOUT")
	llmTimeout := 600 * time.Second
	if llmTimeoutStr != "" {
		if d, err := time.ParseDuration(llmTimeoutStr); err == nil {
			llmTimeout = d
		}
	}
	llmModel := os.Getenv("LLM_MODEL")
	if llmModel == "" {
		llmModel = "meta-llama/llama-3.2-3b-instruct:free"
	}
	client := llm.NewClient(llmEndpoint, llmModel, roleCanonical, llmTimeout)

	// Load persisted state
	stateFile := statePath(townRoot, role, rig, polecat)
	state := loadState(stateFile)
	printStartup("[gt-agent] State loaded: patrol_count=%d idle_cycles=%d\n",
		state.PatrolCount, state.IdleCycles)

	if orchestrated {
		if roleCanonical == "polecat" {
			identityRig := rig
			if identity := loadAgentFile(".gt-agent"); identity != nil && identity.Rig != "" {
				identityRig = identity.Rig
			}
			if discovered := orchestrator.DiscoverTownPolecatRig(townRoot, rig, identityRig); discovered != "" {
				rig = discovered
				os.Setenv("GT_RIG", rig)
			}
			// Stale hq-polecat sessions may have empty GIT_AUTHOR_NAME (<rig>/polecats/).
			if rig != "" && polecat == "" && os.Getenv("GIT_AUTHOR_NAME") == "" {
				author := rig + "/polecat"
				os.Setenv("GIT_AUTHOR_NAME", author)
				os.Setenv("BD_ACTOR", author)
			}
		}
		return runOrchestrated(ctx, client, townRoot, roleCanonical, rig, sessionName, stateFile, state)
	}

	// Main event loop
	for {
		if isShutdownRequested() {
			fmt.Println("[gt-agent] Shutdown requested, exiting loop")
			break
		}

		// Gather work from all sources
		// Mechanic doesn't check mail - it patrols logs instead
		mailCheck := role != "mechanic"
		workItems := gatherWork(gtBin, townRoot, sessionName, role, mailCheck)

		// Mechanic short-circuit: always run the deterministic patrol script,
		// regardless of nudges or pending work items. Mechanic has no LLM
		// system prompt and other agents' nudges (e.g. "Mechanic detected
		// Extraordinary action - please recover") are telling it to scan
		// logs, not to chat with an LLM. Without this short-circuit, any
		// nudge in the queue drops mechanic into the LLM path where it
		// hangs forever with no template guidance.
		if roleCanonical == "mechanic" {
			state.IdleCycles = 0
			state.LastActivity = time.Now()
			state.PatrolCount++
			state.ExtraordinaryAction = false
			fmt.Printf("[gt-agent] Processing %d work item(s) (patrol #%d)\n",
				len(workItems), state.PatrolCount)
			fmt.Println("[gt-agent] Running mechanic patrol script...")
			cmd := exec.Command("/bin/sh", "-c", mechanicPatrolScript)
			out, err := cmd.CombinedOutput()
			if err != nil {
				fmt.Fprintf(os.Stderr, "[gt-agent] Patrol error: %v\n%s\n", err, string(out))
			} else {
				fmt.Printf("[gt-agent] Patrol output:\n%s\n", string(out))
			}
			_ = saveState(stateFile, state)
			time.Sleep(2 * time.Second)
			continue
		}

		effortLevel := "full"
		if len(workItems) == 0 {
			if !isPermanentAgent(role) {
				state.IdleCycles++
				sleep := sleepDuration(state.IdleCycles)
				fmt.Printf("[gt-agent] No work (idle_cycle=%d), sleeping %s\n",
					state.IdleCycles, sleep)

				if state.IdleCycles >= maxIdleCycles {
					fmt.Println("[gt-agent] Max idle cycles reached, exiting")
					_ = saveState(stateFile, state)
					return nil
				}
				_ = saveState(stateFile, state)
				time.Sleep(sleep)
				continue
			}

			// Permanent agents use gt mol await-signal for effort tuning
			fmt.Println("[gt-agent] No immediate work, awaiting signal...")
			effortLevel = callAwaitSignal(gtBin, sessionName)

			// Re-gather work after signal (or timeout)
			workItems = gatherWork(gtBin, townRoot, sessionName, roleCanonical, mailCheck)
			if len(workItems) == 0 {
				// Routine patrol cycle on timeout
				workItems = append(workItems, "Perform a routine patrol cycle.")
			}
		}

		// Reset idle counter when work is found
		state.IdleCycles = 0
		state.LastActivity = time.Now()
		state.PatrolCount++
		// Reset extraordinary flag at start of each cycle — previous cycle's
		// errors should not cause infinite handoff loops.
		state.ExtraordinaryAction = false

		fmt.Printf("[gt-agent] Processing %d work item(s) (patrol #%d)\n",
			len(workItems), state.PatrolCount)

		// Load context via gt prime --hook
		primeOut, _ := exec.Command(gtBin, "prime", "--hook").Output()

		// Build role-specific system prompt
		systemPrompt := buildSystemPrompt(roleCanonical, rig, polecat, townRoot, state.PatrolCount, string(primeOut), effortLevel)

		// Build user prompt from work items - explicitly tell LLM to do ALL items in one response
		userPrompt := "Execute ALL of the following work items and report results. Output a CMD for EACH item:\n\n"
		for i, item := range workItems {
			userPrompt += fmt.Sprintf("%d. %s\n", i+1, item)
		}

		extraordinary := false
		// Multi-turn conversation loop within a single patrol cycle.
		// This allows the agent to see command output and proceed immediately.
		messages := []llm.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		}

		maxTurns := 10
		var summary string
		for turn := 1; turn <= maxTurns; turn++ {
			if turn > 1 {
				fmt.Printf("[gt-agent] Calling LLM (turn %d)...\n", turn)
			} else {
				fmt.Println("[gt-agent] Calling LLM...")
			}

			response, err := client.CompleteMessages(ctx, messages)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[gt-agent] LLM completion failed: %v\n", err)
				break
			}

			// Record raw response
			fmt.Printf("[gt-agent] LLM response (%d chars):\n%s\n---\n", len(response), response)
			recordMoleculeState("LLM RESPONSE", response)
			messages = append(messages, llm.Message{Role: "assistant", Content: response})

			cmdBlocks, doneSummary, hallucinated := parseLLMResponse(response)
			if doneSummary != "" {
				summary = doneSummary
			}

			if hallucinated {
				fmt.Println("[gt-agent] \u26a0 REJECTED hallucinated output.")
				messages = append(messages, llm.Message{Role: "user", Content: "ERROR: Do not simulate 'Output:'. Output 'CMD: [command]' and STOP. The system will provide the output."})
				continue
			}

			if len(cmdBlocks) == 0 {
				fmt.Println("[gt-agent] No commands found. Ending patrol cycle.")
				break
			}

		// Execute ALL command blocks
		var combinedOutput strings.Builder
		for _, fullCmd := range cmdBlocks {
			cmd := strings.TrimSpace(fullCmd)
			// Some LLMs over-escape quotes in single-line commands
			// (e.g. emit `gt mail send -s \"hi\"` when they really
			// meant `gt mail send -s "hi"`). Unescape \' and \" so
			// the shell sees the intended literal quote.
			//
			// EXCEPTION (Fix #113): multi-line scripts that wrap a
			// `python3 -c "...\"x\"..."` or similar nested-quote
			// invocation NEED the inner `\"` to survive into
			// `/bin/sh -c` because sh's double-quote parser is what
			// turns `\"` into `"`. Stripping `\"` here destroys the
			// nesting and sh sees an unbalanced `"`. We detect that
			// case by looking for a single-quoted multi-line block
			// (`sh -c '` or `bash -c '` at the start, with an
			// embedded newline) and skip the unescape for it.
			if !isMultilineQuotedScript(cmd) {
				cmd = strings.ReplaceAll(cmd, "\\'", "'")
				cmd = strings.ReplaceAll(cmd, "\\\"", "\"")
			}
			if strings.HasPrefix(cmd, "`") && strings.HasSuffix(cmd, "`") && !strings.Contains(cmd, "\n") {
				cmd = strings.Trim(cmd, "`")
			}

				safeCmd, rewritten := normalizeGeneratedCommand(cmd)
				if rewritten {
					fmt.Printf("[gt-agent] Rewrote command: %q -> %q\n", cmd, safeCmd)
				}
				fmt.Printf("[gt-agent] $ %s\n", safeCmd)

				if diag, ok := checkShellSyntax(safeCmd); !ok {
					fmt.Fprintf(os.Stderr, "[gt-agent] REJECTED invalid shell (syntax check): %s\n", diag)
					extraordinary = true
					state.ExtraordinaryAction = true
					combinedOutput.WriteString(fmt.Sprintf(
						"Command: %s\nError: invalid shell script (syntax check failed): %s\n\n",
						safeCmd, diag))
					continue
				}

				c := exec.Command("/bin/sh", "-c", safeCmd)
				c.Env = os.Environ()
				c.Env = append(c.Env, "GT_SESSION=" + sessionName)
				out, err := c.CombinedOutput()
				recordMoleculeState(safeCmd, string(out))

				if err != nil {
					cmdFailed := true
					if strings.Contains(string(out), "circuit breaker is open") {
						fmt.Println("[gt-agent] Dolt circuit breaker open, retrying in 5s...")
						time.Sleep(5 * time.Second)
						out, err = exec.Command("/bin/sh", "-c", safeCmd).CombinedOutput()
						recordMoleculeState(safeCmd, string(out))
						if err == nil {
							cmdFailed = false
						}
					}
					if cmdFailed {
						fmt.Fprintf(os.Stderr, "[gt-agent] Error: %v\n%s\n", err, string(out))
						extraordinary = true
						combinedOutput.WriteString(fmt.Sprintf("Command: %s\nError: %v\nOutput: %s\n\n", safeCmd, err, string(out)))
					} else {
						fmt.Printf("[gt-agent] Output (retry OK):\n%s\n", string(out))
						combinedOutput.WriteString(fmt.Sprintf("Command: %s\nOutput: %s\n\n", safeCmd, string(out)))
					}
				} else {
					fmt.Printf("[gt-agent] Output:\n%s\n", string(out))
					combinedOutput.WriteString(fmt.Sprintf("Command: %s\nOutput: %s\n\n", safeCmd, string(out)))
				}

				if extraordinary {
					state.ExtraordinaryAction = true
				}
			}

			// Feed output back to LLM for the next turn
			messages = append(messages, llm.Message{
				Role:    "user",
				Content: fmt.Sprintf("Output:\n%s", combinedOutput.String()),
			})

			// Stop if we saw gt done, gt handoff, or a summary
			if summary != "" || strings.Contains(response, "gt done") || strings.Contains(response, "gt handoff") {
				if summary != "" {
					fmt.Printf("[gt-agent] Summary: %s\n", summary)
				}
				break
			}
		}

		// Call role-specific post-work command
		postCmd := postWorkCommand(roleCanonical, summary)
		if postCmd == "" {
			fmt.Println("[gt-agent] No post-work command for this role")
		} else {
			fmt.Printf("[gt-agent] Calling %s...\n", postCmd)
		}
		parts := strings.Fields(postCmd)
		if len(parts) > 0 {
			cmd := exec.Command(gtBin, parts...)
			out, err := cmd.CombinedOutput()
			if err != nil {
				// Dolt circuit breaker errors are transient during startup.
				// Retry once after a short delay before marking as extraordinary.
				if strings.Contains(string(out), "circuit breaker is open") {
					fmt.Println("[gt-agent] Dolt circuit breaker open, retrying in 5s...")
					time.Sleep(5 * time.Second)
					cmd = exec.Command(gtBin, parts...)
					out, err = cmd.CombinedOutput()
					if err == nil {
						fmt.Printf("[gt-agent] %s (retry OK): %s\n", postCmd, strings.TrimSpace(string(out)))
						goto postCmdSuccess
					}
				}
				fmt.Fprintf(os.Stderr, "[gt-agent] %s failed: %v\n%s\n", postCmd, err, string(out))
				extraordinary = true
			} else {
				fmt.Printf("[gt-agent] %s: %s\n", postCmd, strings.TrimSpace(string(out)))
			}
		postCmdSuccess:
		}

		// Periodic mail drain — keep this agent's inbox tidy.
		//
		// Without this, read mail accumulates forever. Observed in
		// production: mayor's inbox grew to 78 messages within hours
		// (47 read, 31 unread), made worse by the handoff loops fixed in
		// #70/#73 but present even after those fixes. `gt mail drain`
		// archives read wisps and protocol/handoff notifications that
		// are older than --max-age (default 30m). The handoff-subject
		// drain is gated on msg.Read inside mail_drain.go so we won't
		// silently drop a critical "Architecture Ready" mail that mayor
		// hasn't acted on yet.
		//
		// Non-fatal: if drain errors, we log and continue. The patrol
		// must never block on inbox hygiene.
		drainOut, drainErr := exec.Command(gtBin, "mail", "drain", "--max-age", "10m").CombinedOutput()
		if drainErr != nil {
			fmt.Fprintf(os.Stderr, "[gt-agent] mail drain (non-fatal): %v\n%s\n", drainErr, string(drainOut))
		} else if strings.Contains(string(drainOut), "Drained") {
			fmt.Printf("[gt-agent] %s", string(drainOut))
		}

		// Auto-unhook for handoff-style roles after a handoff summary.
		//
		// Planner / Architect / QA each have a "do once, hand off, stop"
		// contract: they take a single bead, produce structured output
		// (child tasks, design doc, review notes), notify the next agent,
		// and are done. If we don't actively clear the hook after handoff
		// the bead remains in [hooked] state forever — every subsequent
		// patrol the agent sees the same bead, redoes the work, and
		// emits ANOTHER "Plan Complete" mail. This caused the planner to
		// generate 41 duplicate child tasks under hq-bbn and flood mayor's
		// inbox with 9+ identical handoff messages.
		//
		// We intentionally do NOT gate on `!extraordinary` here. A cycle
		// can be marked extraordinary from any single failing sub-command
		// (e.g. a typo, a temporary dolt blip) while the overall handoff
		// — the mail and the nudge to mayor — still succeeded. Observed
		// in production: planner emitted `Summary: handed off to mayor`
		// but extraordinary=true from an earlier minor failure suppressed
		// the unhook, and the loop kept going. The summary-keyword filter
		// in shouldAutoUnhookAfterHandoff is strict enough on its own:
		// "blocked", "error", "failed", "missing", "investigating" etc.
		// already prevent unhooking from a true-failure summary.
		if shouldAutoUnhookAfterHandoff(roleCanonical, summary) {
			fmt.Println("[gt-agent] Handoff-style role finished — auto-unhooking to prevent re-planning loop")
			// `--force` is required for handoff roles (architect/planner/qa)
			// because their slice of work is intentionally incomplete: the
			// architect designs but doesn't implement, the planner plans
			// but doesn't code, etc. Without --force, `gt unhook` rejects
			// with "hooked work <bead> is incomplete" and the agent loops.
			out, err := exec.Command(gtBin, "unhook", "--force").CombinedOutput()
			if err != nil {
				fmt.Fprintf(os.Stderr, "[gt-agent] auto-unhook failed (non-fatal): %v\n%s\n", err, string(out))
			} else {
				fmt.Printf("[gt-agent] auto-unhook: %s\n", strings.TrimSpace(string(out)))
			}
		}

		// Mark extraordinary if any error occurred
		if extraordinary {
			state.ExtraordinaryAction = true
			state.CmdRetryCount++
			fmt.Printf("[gt-agent] Extraordinary action detected in this cycle (retry #%d)\n", state.CmdRetryCount)

			// After 5 consecutive extraordinary cycles, try to recover
			if state.CmdRetryCount >= 5 {
				fmt.Printf("[gt-agent] WARNING: 5 consecutive extraordinary cycles detected. Attempting recovery...\n")
				recoverCmd := fmt.Sprintf("gt hook clear --force 2>/dev/null; gt mail archive --stale 2>/dev/null; echo RECOVERED")
				out, err := exec.Command("/bin/sh", "-c", recoverCmd).CombinedOutput()
				if err == nil || strings.Contains(string(out), "RECOVERED") {
					fmt.Printf("[gt-agent] Recovery: cleared hook and archived stale mail\n")
					state.CmdRetryCount = 0
				} else {
					fmt.Printf("[gt-agent] Recovery failed, will retry next cycle\n")
				}
			}
		} else {
			// Reset retry count on successful cycle
			state.CmdRetryCount = 0
		}

		// Save state after each cycle
		_ = saveState(stateFile, state)

		// Stay in the loop - don't exit on command errors. The agent should
		// continuously patrol. Only daemon restart or SIGTERM should stop us.
		// This prevents crash loop backoffs from rapid exit/restart cycles.

		// Brief pause before next poll cycle
		time.Sleep(2 * time.Second)
	}

	fmt.Println("[gt-agent] Event loop exited cleanly")
	return nil
}

// canonicalRole maps rig-qualified or compound GT_ROLE values to the base role
// used by prompting and post-work logic.
// heredocStartRE matches a shell heredoc opener like `<<EOF`, `<< 'EOF'`,
// `<<-"EOF"`, optionally followed by additional shell syntax such as a
// redirection (`> file`) or pipe.
//
// We capture the terminator so the parser knows when to close the block.
var heredocStartRE = regexp.MustCompile(`<<-?\s*['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?`)

// trailingMarkdownBoldRE matches markdown-bold prose tacked onto the end
// of a CMD line, e.g.
//
//	CMD: bd search "F23" --status "open" **Rationale for Command Change:**
//
// The model loves to append a bold-headed rationale paragraph on the same
// line as the command. Anything from `<whitespace>**<text>**` onward is
// not part of the shell command and must be stripped before execution.
//
// Constraints to avoid false positives:
//   - Requires at least one whitespace character before the opening `**`,
//     so shell globstars `**/*.go` (no space before) are NOT matched.
//   - Requires 2+ characters between the `**` markers and at least one
//     ASCII letter, so balanced `**` inside (unusual) shell args are
//     less likely to match.
var trailingMarkdownBoldRE = regexp.MustCompile(`\s+\*\*[^*\n]*[A-Za-z][^*\n]*\*\*.*$`)

// parseLLMResponse extracts CMD blocks and a DONE summary from an LLM
// response. It returns:
//   - cmds: the list of commands the agent should execute, in order
//   - doneSummary: the text after the first "DONE:" marker (if any)
//   - hallucinated: true if the model tried to simulate "Output:" itself,
//     in which case the caller should reject the response and re-prompt
//
// Parsing rules (designed to balance safety against the model's tendency
// to emit one CMD: followed by an entire multi-line shell SCRIPT — a
// pattern we observed in the architect role where heredoc + mail + nudge
// + unhook were all crammed under a single CMD: prefix):
//
//  1. A line beginning with "CMD:" starts a shell script. The script
//     STARTS with the rest of the CMD: line and continues with subsequent
//     non-empty, non-prose lines (this is the change from the old
//     strict-single-line behavior).
//  2. The current script is "flushed" (joined with newlines and emitted
//     as one cmd) on any of these terminators:
//     • a blank line (most common script-end marker)
//     • a markdown structural line (`### `, `## `, `# `, ```` ``` ````,
//       `> `) — these never appear inside real shell
//     • a new `CMD:` line (starts a fresh script)
//     • a `DONE:` line (captures the patrol summary)
//     • a `Output:` line (hallucination, abort)
//  3. If a script line opens a heredoc (e.g. `cat <<'EOF' > file`), the
//     parser switches to heredoc mode and collects subsequent lines
//     verbatim until it sees a line whose trimmed contents equal the
//     heredoc terminator. An unterminated heredoc causes the whole
//     script to be discarded.
//  4. Markdown fences on the CMD: line (```bash, ```sh, ```) are stripped.
//  5. Inline `CMD:` markers (`CMD: a CMD: b CMD: c`) split the first
//     CMD: line into multiple separate commands.
//  6. A `Output:` line outside a heredoc means the model hallucinated a
//     terminal session. We abort parsing and signal the caller to re-
//     prompt.
//
// The script-collection design exists so the LLM can emit a single
// CMD: block containing e.g.:
//
//	CMD: mkdir -p /work
//	cat > /work/file <<'EOF'
//	... content ...
//	EOF
//	gt mail send mayor/ -s "ready" -m "done"
//	gt unhook
//	DONE: handed off
//
// /bin/sh -c handles the multi-line script natively (heredocs, multi-line
// quoted args, semicolons, etc.), so the right behavior is to forward
// the whole script as one shell invocation.
func parseLLMResponse(response string) (cmds []string, doneSummary string, hallucinated bool) {
	lines := strings.Split(response, "\n")

	var scriptBuf []string // accumulating lines for the current shell script
	var heredocTerm string // empty when not inside a heredoc body
	var heredocJustClosed bool
	inScript := false      // true if we're collecting continuation lines for an active CMD:

	flushScript := func() {
		if len(scriptBuf) == 0 {
			return
		}
		if heredocTerm != "" {
			// Unterminated heredoc — discard the whole script so we
			// never execute a fragment that may contain prose.
			scriptBuf = nil
			heredocTerm = ""
			return
		}
		cmds = append(cmds, strings.Join(scriptBuf, "\n"))
		scriptBuf = nil
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Inside a heredoc body — collect verbatim until terminator.
		if heredocTerm != "" {
			scriptBuf = append(scriptBuf, line)
			if trimmed == heredocTerm {
				heredocTerm = ""
				heredocJustClosed = true
			}
			continue
		}

		lowerTrimmed := strings.ToLower(trimmed)
		if strings.HasPrefix(lowerTrimmed, "output:") {
			hallucinated = true
			return nil, "", true
		}

		if strings.HasPrefix(trimmed, "DONE:") {
			flushScript()
			inScript = false
			if doneSummary == "" {
				doneSummary = strings.TrimSpace(strings.TrimPrefix(trimmed, "DONE:"))
			}
			continue
		}

		if strings.HasPrefix(trimmed, "CMD:") {
			// Flush any in-flight script from a previous CMD: before
			// starting a new one.
			flushScript()
			inScript = false

			cmd := strings.TrimSpace(strings.TrimPrefix(trimmed, "CMD:"))
			// Small models emit `CMD: DONE: …` instead of a bare `DONE:` line.
			// Treat like DONE so the patrol ends cleanly (Fix #121).
			if strings.HasPrefix(cmd, "DONE:") {
				if doneSummary == "" {
					doneSummary = strings.TrimSpace(strings.TrimPrefix(cmd, "DONE:"))
				}
				continue
			}

			// Strip a leading markdown fence on the same line as CMD:.
			if strings.HasPrefix(cmd, "```") {
				cmd = strings.TrimPrefix(cmd, "```")
				cmd = strings.TrimPrefix(cmd, "bash")
				cmd = strings.TrimPrefix(cmd, "sh")
				cmd = strings.TrimSpace(cmd)
			}
			// Strip a trailing markdown fence if the model closed on the same line.
			cmd = strings.TrimSuffix(cmd, "```")
			cmd = strings.TrimSpace(cmd)

			if cmd == "" {
				continue
			}

			// Split on inline `CMD:` markers FIRST, then strip
			// `**bold**` prose from each segment.
			//
			// Fix #91 (parse-order bug): we used to strip
			// `trailingMarkdownBoldRE` on the whole line BEFORE
			// splitting on `\s+CMD:\s+`. The regex matches greedily
			// to end-of-line (`.*$`), so a response like
			//
			//   CMD: gt patrol new **Patrol Cycle #531 Initiated** CMD: gt patrol scan --notify **...** CMD: gt polecat list
			//
			// collapsed to just `gt patrol new` — every subsequent
			// `CMD:` segment was discarded along with the trailing
			// markdown. The witness was looping for hours, executing
			// only the first command of each multi-command response.
			// Now we split first, then strip bold prose per segment,
			// so each segment keeps its real shell command.
			subs := splitInlineCMDs(cmd)
			for i := range subs {
				subs[i] = trailingMarkdownBoldRE.ReplaceAllString(subs[i], "")
				subs[i] = strings.TrimSpace(subs[i])
			}
			for i := 0; i < len(subs)-1; i++ {
				s := strings.TrimSpace(subs[i])
				if s == "" {
					continue
				}
				if strings.HasPrefix(s, "DONE:") {
					if doneSummary == "" {
						doneSummary = strings.TrimSpace(strings.TrimPrefix(s, "DONE:"))
					}
					continue
				}
				cmds = append(cmds, s)
			}
			last := strings.TrimSpace(subs[len(subs)-1])
			if last == "" {
				continue
			}
			if strings.HasPrefix(last, "DONE:") {
				if doneSummary == "" {
					doneSummary = strings.TrimSpace(strings.TrimPrefix(last, "DONE:"))
				}
				continue
			}

			scriptBuf = []string{last}
			inScript = true
			if term := detectHeredocTerm(last); term != "" {
				heredocTerm = term
			}
			continue
		}

		if !inScript {
			// Not collecting — ignore prose, markdown, etc. outside CMD: blocks.
			continue
		}

		// We're after a CMD: line, accumulating the script body.
		if trimmed == "" {
			// Blank line terminates the script — UNLESS we're inside an
			// open single- or double-quoted string (typical for the
			// Mayor's Stage 0 atomic `sh -c '...'` pipeline whose body
			// contains paragraph-style blank lines for readability).
			// Without this guard the parser orphans the opening `sh -c '`
			// and `/bin/sh` errors out with "Unterminated quoted string"
			// at the first blank line — observed in mayor patrol #3 with
			// the Stage 0 atomic pipeline (Fix #113).
			if hasOpenShellQuote(scriptBuf) {
				scriptBuf = append(scriptBuf, line)
				continue
			}
			flushScript()
			inScript = false
			continue
		}

		if isMarkdownStructure(trimmed) {
			// Markdown also terminates — but only if quotes are balanced.
			// Inside an open `sh -c '...'` body, a `# comment` line is
			// just a shell comment, not a markdown heading.
			if hasOpenShellQuote(scriptBuf) {
				scriptBuf = append(scriptBuf, line)
				continue
			}
			flushScript()
			inScript = false
			continue
		}

		// Fix #96 (architect prose-leak crash): the LLM sometimes
		// emits English narration directly after a CMD: line without
		// a blank line in between, e.g.
		//
		//   CMD: gt hook | cat
		//   Now I'll re-execute the necessary commands with the correct bead ID extraction.
		//   Let me first check the hook output properly, then redo the steps.
		//
		// The old script-collection logic appended every non-blank
		// non-markdown line to the script body, so the prose got
		// piped into /bin/sh where the apostrophe in "I'll" opened
		// an unterminated quoted string and the architect looped
		// forever on the same syntax error.
		if looksLikeProseLine(trimmed) {
			// Same exception as for blank/markdown lines: prose-shaped
			// content inside an open shell quote is part of the quoted
			// string, not LLM narration. Don't kill the script body.
			if hasOpenShellQuote(scriptBuf) {
				scriptBuf = append(scriptBuf, line)
				continue
			}
			flushScript()
			inScript = false
			continue
		}

		if looksLikeMarkdownStepLine(trimmed) {
			if hasOpenShellQuote(scriptBuf) && !heredocJustClosed {
				scriptBuf = append(scriptBuf, line)
				continue
			}
			flushScript()
			inScript = false
			heredocJustClosed = false
			continue
		}

		// Model hallucinated directory listings pasted after a CMD (QA local LLMs).
		if heredocTerm == "" && looksLikeHallucinatedShellOutput(trimmed) {
			if hasOpenShellQuote(scriptBuf) {
				scriptBuf = append(scriptBuf, line)
				continue
			}
			flushScript()
			inScript = false
			heredocJustClosed = false
			continue
		}

		// Outcome JSON glued after a heredoc (common architect/planner mistake)
		// must not be appended to the shell script (Fix: outcome:: not found).
		if isOrchestratedOutcomeLine(trimmed) ||
			(strings.HasPrefix(trimmed, "{") && strings.Contains(strings.ToLower(trimmed), "outcome")) {
			flushScript()
			inScript = false
			heredocTerm = ""
			continue
		}

		// Append this line to the current script body.
		scriptBuf = append(scriptBuf, line)
		heredocJustClosed = false
		// If this continuation line opens a heredoc, enter heredoc mode
		// so subsequent lines are collected verbatim until the terminator.
		if term := detectHeredocTerm(line); term != "" {
			heredocTerm = term
		}
	}

	// Trailing script (no DONE: or blank line at end of input).
	flushScript()
	return cmds, doneSummary, false
}

// isMultilineQuotedScript reports whether cmd looks like a multi-line
// shell wrapper such as `sh -c '...'` or `bash -c '...'` whose body
// contains nested quoting that the gt-agent's blanket `\"` -> `"`
// unescape would destroy. Used to skip the unescape for those cases.
// Fix #113 (matches the Mayor's Stage 0 atomic pipeline).
func isMultilineQuotedScript(cmd string) bool {
	if !strings.Contains(cmd, "\n") {
		return false
	}
	trim := strings.TrimSpace(cmd)
	prefixes := []string{
		"sh -c '",
		"sh -c \"",
		"bash -c '",
		"bash -c \"",
		"/bin/sh -c '",
		"/bin/sh -c \"",
		"/bin/bash -c '",
		"/bin/bash -c \"",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(trim, p) {
			return true
		}
	}
	return false
}

// hasOpenShellQuote reports whether the accumulated script body has an
// unbalanced (open) single- or double-quoted string. Used by
// parseLLMResponse to decide whether a blank/markdown/prose-shaped line
// inside a CMD: block is genuine script termination (closed quotes) or
// just paragraph spacing inside a multi-line `sh -c '...'` body
// (open quote — keep collecting). Fix #113.
//
// The scan is intentionally simple:
//   - tracks two states: in-single-quote, in-double-quote
//   - inside single quotes nothing escapes; we wait for the matching `'`
//   - inside double quotes `\"` escapes the close; we honour it
//   - outside any quote, `\` followed by `'` or `"` is also honoured so
//     a literal backslash-quote in code doesn't open a fake string
//
// Heredoc bodies are handled by the dedicated heredocTerm state in
// parseLLMResponse, so we don't have to teach this function about them.
func hasOpenShellQuote(scriptBuf []string) bool {
	if len(scriptBuf) == 0 {
		return false
	}
	inSingle := false
	inDouble := false
	for _, line := range scriptBuf {
		for i := 0; i < len(line); i++ {
			c := line[i]
			switch {
			case inSingle:
				if c == '\'' {
					inSingle = false
				}
			case inDouble:
				if c == '\\' && i+1 < len(line) {
					i++
					continue
				}
				if c == '"' {
					inDouble = false
				}
			default:
				if c == '\\' && i+1 < len(line) {
					i++
					continue
				}
				if c == '\'' {
					inSingle = true
				} else if c == '"' {
					inDouble = true
				}
			}
		}
		// A newline does NOT close either kind of quote in POSIX shell,
		// so we just keep scanning across lines.
	}
	return inSingle || inDouble
}

// isMarkdownStructure reports whether trimmed looks like the start of a
// markdown structural element that the LLM commonly emits between
// command blocks. These lines should terminate any active CMD: script
// because they are never valid shell.
func isMarkdownStructure(trimmed string) bool {
	if strings.HasPrefix(trimmed, "### ") ||
		strings.HasPrefix(trimmed, "## ") ||
		strings.HasPrefix(trimmed, "# ") {
		return true
	}
	if strings.HasPrefix(trimmed, "```") {
		// Both opening (```bash) and closing (```) fences.
		return true
	}
	if strings.HasPrefix(trimmed, "> ") {
		return true
	}
	return false
}

// llmProseStartRE matches the leading word/phrase of an LLM reasoning
// sentence the model sometimes emits BETWEEN commands without ever
// closing the prior CMD: script with a blank line. These lines look
// like normal English ("Now I'll re-execute...", "Let me first check
// the hook output...") but get fed into /bin/sh by the script-
// collection logic, where the apostrophe in "I'll" or "Let's" opens an
// unterminated quoted string and the whole script aborts.
//
// We deliberately match only the most common LLM thinking-prose
// starters. The list is narrow on purpose: real shell commands almost
// never begin with these multi-word capitalized phrases, and the
// trailing-period + no-shell-metacharacter constraint in
// looksLikeProseLine adds further safety.
var llmProseStartRE = regexp.MustCompile(`^(Now (I'?ll|I'?m|let|let'?s|we'?ll|we'?re|that|this|the)\b|Let me\b|Let'?s\b|First[,:\s]|Next[,:\s]|Then I'?ll\b|Finally[,:\s]|I'?ll (now|first|need|then|also|go)\b|I'?m going to\b|I need to\b|I will\b|We need to\b|We'?ll (now|first)\b|This will\b|That will\b|Here'?s\b|Note[,:\s]|Actually[,:\s]|However[,:\s]|So\b|Because\b|Since\b)`)

// looksLikeProseLine reports whether trimmed looks like LLM narration
// rather than shell continuation, and should terminate any in-flight
// CMD: script. Heuristic:
//
//   - Starts with a known LLM thinking-prose phrase (llmProseStartRE).
//   - Contains NO shell metacharacters that would make it a real
//     command ($, |, &, ;, <, >, =, backtick, parentheses).
//   - Ends with sentence-terminating punctuation (. ! ? :).
//
// All three must hold. The combination is conservative enough that
// real shell continuation lines (`&& gt mail send`, `| jq`, `> file`,
// `for i in ...; do`, etc.) never trigger it.
func looksLikeProseLine(trimmed string) bool {
	if trimmed == "" {
		return false
	}
	if llmLogLineRE.MatchString(trimmed) || llmHeaderLineRE.MatchString(trimmed) {
		return true
	}
	if strings.ContainsAny(trimmed, "$|&;<>=`()") {
		return false
	}
	last := trimmed[len(trimmed)-1]
	if last != '.' && last != '!' && last != '?' && last != ':' {
		return false
	}
	return llmProseStartRE.MatchString(trimmed)
}

// markdownOrderedStepRE matches "1. Load context" style lines the model
// often emits after a shell command without a blank line. Feeding those
// into /bin/sh produces junk like `1.: not found` or, with stray `<`
// tokens from XML-ish leaks, `Syntax error: newline unexpected` on dash.
// Real shell almost never starts a continuation line with `N. ` + letter.
var markdownOrderedStepRE = regexp.MustCompile(`^\d+\.\s+[A-Za-z#*\-]`)

// markdownBulletStepRE matches "- **Do**" / "- Check" markdown bullets.
var markdownBulletStepRE = regexp.MustCompile(`^-\s+(\*\*|[[#]|[A-Za-z])`)

// llmLogLineRE matches "[2026-05-14] 23:00 ..." style lines the model
// appends after a shell command.
var llmLogLineRE = regexp.MustCompile(`^\[\d{4}-\d{2}-\d{2}\]`)

// llmHeaderLineRE matches "--- Depth 6 ---" style dividers.
var llmHeaderLineRE = regexp.MustCompile(`^--- .* ---$`)

var hallucinatedLsTotalRE = regexp.MustCompile(`^total\s+\d+`)
var hallucinatedLsEntryRE = regexp.MustCompile(`^(?:drwx|[\-d][rwxwx-]{9})\s`)

// looksLikeHallucinatedShellOutput reports fake `ls` output the model pastes as text.
func looksLikeHallucinatedShellOutput(trimmed string) bool {
	if trimmed == "" {
		return false
	}
	if hallucinatedLsTotalRE.MatchString(trimmed) {
		return true
	}
	if hallucinatedLsEntryRE.MatchString(trimmed) {
		return true
	}
	return false
}

// looksLikeMarkdownStepLine reports narration the model appends after CMD:
// lines (numbered/bulleted steps from its system prompt). These must not
// be concatenated into the shell script when quotes are balanced.
func looksLikeMarkdownStepLine(trimmed string) bool {
	return markdownOrderedStepRE.MatchString(trimmed) ||
		markdownBulletStepRE.MatchString(trimmed)
}

// checkShellSyntax runs /bin/sh -n on script. If this fails, the script
// must not be executed: dash parses incrementally and will happily run
// early commands (side effects) before a syntax error on a later line.
func checkShellSyntax(script string) (diag string, ok bool) {
	c := exec.Command("/bin/sh", "-n", "-c", script)
	out, err := c.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(out)), false
	}
	return "", true
}

// inlineCMDMarkerRE matches space- or newline-separated inline CMD markers.
var inlineCMDMarkerRE = regexp.MustCompile(`(?:\s+CMD:|\nCMD:)\s*`)

var gluedPathCMDRE = regexp.MustCompile(`/CMD:\s*`)
var gluedExtCMDRE = regexp.MustCompile(`(\.[a-zA-Z0-9]{2,8})CMD:\s*`)
var gluedQuoteCMDRE = regexp.MustCompile(`'CMD:\s*`)
var gluedEOFCMDRE = regexp.MustCompile(`(?i)EOF\s*'?\s*CMD:\s*`)
var gluedOutcomeAfterQuoteRE = regexp.MustCompile(`'\s*\{[\s]*"outcome"`)

// normalizeGluedCMDMarkers turns model glitches like rig/CMD:, SPEC.mdCMD:, or
// 'open'CMD: into newline-separated CMD markers so splitInlineCMDs can separate
// them without eating filename extensions or shell quoting.
func normalizeGluedCMDMarkers(cmd string) string {
	cmd = gluedPathCMDRE.ReplaceAllString(cmd, "\nCMD: ")
	cmd = gluedExtCMDRE.ReplaceAllString(cmd, "$1\nCMD: ")
	cmd = gluedQuoteCMDRE.ReplaceAllString(cmd, "'\nCMD: ")
	cmd = gluedEOFCMDRE.ReplaceAllString(cmd, "EOF\nCMD: ")
	cmd = gluedOutcomeAfterQuoteRE.ReplaceAllString(cmd, "'\n")
	return cmd
}

// splitInlineCMDs splits cmd on inline `CMD:` markers. If cmd contains
// no inline markers, it returns []string{cmd}.
func splitInlineCMDs(cmd string) []string {
	cmd = normalizeGluedCMDMarkers(cmd)
	if !inlineCMDMarkerRE.MatchString(cmd) {
		return []string{strings.TrimSpace(cmd)}
	}
	parts := inlineCMDMarkerRE.Split(cmd, -1)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return []string{cmd}
	}
	return out
}

// detectHeredocTerm returns the heredoc terminator word if cmd opens a
// heredoc (e.g. `cat <<EOF`, `cat << 'EOF' > file.md`). Otherwise it
// returns an empty string.
func detectHeredocTerm(cmd string) string {
	m := heredocStartRE.FindStringSubmatch(cmd)
	if len(m) == 2 {
		return m[1]
	}
	return ""
}

func canonicalRole(role string) string {
	r := strings.TrimSpace(role)
	if r == "" {
		return "worker"
	}
	if strings.Contains(r, "/polecats/") {
		return "polecat"
	}
	if strings.Contains(r, "/crew/") {
		return "crew"
	}
	parts := strings.Split(r, "/")
	if len(parts) > 1 {
		return parts[len(parts)-1]
	}
	return r
}

// findCaseInsensitiveNameInDir returns the joined path for the first
// regular file in dir whose name equals wantFile under strings.EqualFold.
func findCaseInsensitiveNameInDir(dir, wantFile string) (full string, ok bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.EqualFold(e.Name(), wantFile) {
			return filepath.Join(dir, e.Name()), true
		}
	}
	return "", false
}

// rewriteSpecMDPathCaseInsensitive replaces any absolute path whose final
// component is "spec.md" under case-insensitive comparison when that exact
// path does not exist on disk, but the parent directory contains a file
// whose name matches "spec.md" case-insensitively (e.g. SPEC.md). This fixes
// recurring planner/architect `cat .../mayor/rig/spec.md` failures on Linux.
func rewriteSpecMDPathCaseInsensitive(cmd string) (string, bool) {
	if !strings.Contains(cmd, ".md") {
		return cmd, false
	}
	out := cmd
	changed := false
	seen := make(map[string]bool)
	for _, m := range absPathMarkdownRE.FindAllString(cmd, -1) {
		if seen[m] {
			continue
		}
		seen[m] = true
		base := filepath.Base(m)
		if !strings.EqualFold(base, "spec.md") {
			continue
		}
		if _, err := os.Stat(m); err == nil {
			continue
		}
		dir := filepath.Dir(m)
		resolved, ok := findCaseInsensitiveNameInDir(dir, "spec.md")
		if !ok || resolved == m {
			continue
		}
		out = strings.ReplaceAll(out, m, resolved)
		changed = true
	}
	return out, changed
}

// normalizeGeneratedCommand rewrites known-invalid LLM command patterns into
// safe canonical forms to avoid guaranteed molecule lookup failures.
func normalizeGeneratedCommand(cmd string) (string, bool) {
	trimmed := strings.TrimSpace(cmd)
	rewritten := false

	// Pre-flight check: Block git commit without actual implementation
	// The polecat keeps trying to commit without creating files
	if strings.HasPrefix(trimmed, "git commit") {
		// Check if there are actual files to commit (not just bead notes)
		checkCmd := exec.Command("/bin/sh", "-c", "git status --short 2>/dev/null | grep -v '^.beads' | grep -v '^.claude' | grep -v 'SPEC.md' | grep -v '^D ' | head -5")
		checkOut, _ := checkCmd.CombinedOutput()
		changes := strings.TrimSpace(string(checkOut))
		if changes == "" {
			// No real files changed - block the commit
			fmt.Printf("[gt-agent] ⚠ BLOCKED: git commit with no implementation files (only bead metadata)\n")
			return "true", true
		}
	}

	// Pre-flight check: Block gt done without commits
	if strings.HasPrefix(trimmed, "gt done") {
		// Verify at least one real commit exists (not auto-save)
		checkCmd := exec.Command("/bin/sh", "-c", "git log --oneline origin/main..HEAD 2>/dev/null | wc -l")
		checkOut, _ := checkCmd.CombinedOutput()
		commits, _ := strconv.Atoi(strings.TrimSpace(string(checkOut)))
		if commits == 0 {
			fmt.Printf("[gt-agent] ⚠ BLOCKED: gt done with no implementation commits\n")
			return "true", true
		}
	}

	// Models often wrap commands in backticks (e.g. `gt prime`)
	if strings.HasPrefix(trimmed, "`") && strings.HasSuffix(trimmed, "`") {
		trimmed = strings.Trim(trimmed, "`")
		rewritten = true
	}

	if fixed, ok := rewriteSpecMDPathCaseInsensitive(trimmed); ok {
		trimmed = fixed
		rewritten = true
	}

	// `gd …` is a recurring typo for `gt …`. There is no `gd` binary on
	// the agent PATH, and the model frequently emits `gd patrol report`,
	// `gd bd show …`, etc. Rewrite the leading `gd ` to `gt ` so the
	// agent does the right thing instead of failing with `gd: not found`.
	if strings.HasPrefix(trimmed, "gd ") {
		trimmed = "gt " + strings.TrimPrefix(trimmed, "gd ")
		rewritten = true
	}

	// Ensure mkdir is idempotent to avoid exit status 1 on existing dirs
	if strings.HasPrefix(trimmed, "mkdir ") && !strings.Contains(trimmed, " -p") {
		trimmed = strings.Replace(trimmed, "mkdir ", "mkdir -p ", 1)
		rewritten = true
	}

	if hasCommandPrefix(trimmed, "gt handoff") {
		// `gt handoff <role> -s SUBJ -m BODY` is a recurring miscommand: the
		// CLI's remote-handoff path doesn't ship the mail (the -s/-m flags are
		// silently dropped for cross-role handoff) and the path errors out
		// with "provider does not support remote handoff" under NATS. What
		// the agent actually means is "send mail to that role and wake them
		// up", so rewrite to `gt mail send <role>/ -s SUBJ -m BODY`.
		if rewritten, ok := rewriteHandoffToMail(trimmed); ok {
			return rewritten, true
		}
		if !strings.Contains(trimmed, " -y") && !strings.Contains(trimmed, " --yes") {
			trimmed += " -y"
			rewritten = true
		}
	}
	if hasCommandPrefix(trimmed, "gt prime") {
		return "gt prime", true
	}
	// `gt hook` has real subcommands (show, attach, detach, clear,
	// status). Only collapse `gt hook` when it's bare or followed by
	// noise the agent invented (placeholders, raw bead IDs as
	// positional args, etc.). A recognized subcommand passes through
	// untouched. Pipelines/redirections that consume `gt hook`'s
	// output (e.g. `gt hook | grep -oE 'hq-wisp-...'`) ALSO pass
	// through — those are legitimate scripts.
	if hasCommandPrefix(trimmed, "gt hook") {
		fields := strings.Fields(trimmed)
		if len(fields) <= 2 {
			return "gt hook", true
		}
		validHookSubcmds := map[string]bool{
			"show":   true,
			"attach": true,
			"detach": true,
			"clear":  true,
			"status": true,
		}
		if validHookSubcmds[fields[2]] {
			// Preserve the real subcommand and its arguments.
			return trimmed, rewritten
		}
		// Shell pipeline / redirection / chaining operators after
		// `gt hook` mean the agent is using hook's output in a
		// script, e.g.
		//   gt hook | grep -oE 'hq-wisp-...' | head -1 > /tmp/x
		// or
		//   gt hook && gt mail inbox
		// Pass these through untouched.
		shellPipeOps := map[string]bool{
			"|": true, "||": true,
			"&": true, "&&": true,
			";":  true,
			">":  true, ">>": true,
			"<":  true,
			"2>": true, "2>>": true,
			"|&": true,
		}
		if shellPipeOps[fields[2]] {
			return trimmed, rewritten
		}
		// Unknown trailing args (model hallucination) — collapse to bare.
		return "gt hook", true
	}
	if hasCommandPrefix(trimmed, "nudge") {
		return "gt " + trimmed, true
	}
	if hasCommandPrefix(trimmed, "mail") {
		return "gt " + trimmed, true
	}
	if hasCommandPrefix(trimmed, "bd show") {
		return "gt " + trimmed, true
	}
	if hasCommandPrefix(trimmed, "bd read") {
		return "gt " + trimmed, true
	}
	// NOTE: We intentionally do NOT rewrite `bd update <id> ...` to
	// `gt bd update <id> ...`. `gt bd` (alias for `gt bead`) is a
	// cross-repo router that only exposes `show`/`read`/`move`/`mol`
	// — there is no `gt bd update`. Rewriting bare `bd update` here
	// produced `gt bd update <id> --status=in_progress` which fails
	// with `unknown flag: --status` and bricks every polecat trying
	// to mark itself in_progress. The formula text already uses bare
	// `bd update`, and the polecat's worktree has `bd` on PATH
	// operating in the correct beads directory, so passing through
	// unchanged is correct. See Fix #87.
	if hasCommandPrefix(trimmed, "bd mol current") {
		return rewriteMolCurrent(trimmed), true
	}
	if strings.HasPrefix(trimmed, "bd close ") {
		args := strings.Fields(trimmed)[2:]
		isShortcut := false
		for _, arg := range args {
			if isNumeric(arg) || strings.HasPrefix(arg, "step-") {
				isShortcut = true
				break
			}
		}
		if isShortcut {
			if lastStepID != "" {
				rewritten = true
				trimmed = prependBeadToStep(trimmed)
			} else {
				fmt.Fprintf(os.Stderr, "[gt-agent] ⚠ REJECTED shortcut close command %q because no step is currently active (molecule may be naked)\n", trimmed)
				return "true", true
			}
		}
	}
	if hasInvalidMolExecute(trimmed) {
		fmt.Fprintf(os.Stderr, "[gt-agent] ⚠ REJECTED hallucinatory mol execute command: %q\n", trimmed)
		return "true", true
	}
	if hasInvalidPatrolCommand(trimmed) {
		fmt.Fprintf(os.Stderr, "[gt-agent] ⚠ REJECTED hallucinatory patrol command: %q\n", trimmed)
		return "true", true
	}
	if reason, ok := hasContentFreeMailSend(trimmed); ok {
		// Fix #90: small LLMs repeatedly emit content-free status mails
		// like `gt mail send <rig>/witness -s "mol-refinery-patrol" -m
		// "Reply to witness regarding mol-refinery-patrol"`. Each one
		// creates a permanent bead + Dolt commit, no operator value,
		// and the receiving agent then spends its next cycle reading
		// and archiving them. We've observed this drive the witness
		// inbox from 40 → 100 → 195+ in a few hours of refinery
		// patrol cycles. Reject these before they reach `gt mail send`.
		fmt.Fprintf(os.Stderr, "[gt-agent] ⚠ REJECTED content-free `gt mail send` (Fix #90: %s): %q\n", reason, trimmed)
		return "true", true
	}
	if hasInvalidShinyFormula(trimmed, currentRole) {
		fmt.Fprintf(os.Stderr, "[gt-agent] ⚠ REJECTED inappropriate `--formula shiny` sling for role %q: %q\n", currentRole, trimmed)
		return "true", true
	}
	if hasInvalidSlingTarget(trimmed) {
		fmt.Fprintf(os.Stderr, "[gt-agent] ⚠ REJECTED hallucinatory `gt sling` with leading-slash target (rig name is missing): %q\n", trimmed)
		return "true", true
	}
	// Rewrite `gt sling … <rig>/polecats [--create] …` to
	// `gt sling … <rig> [--create] …`. The CLI rejects the bare
	// `<rig>/polecats` form ("polecats requires a polecat name; or use
	// just '<rig>' to auto-spawn") but the LLM keeps emitting it. The
	// rewrite preserves intent (auto-spawn a polecat in that rig) and
	// saves a wasted LLM turn.
	if rewrote, ok := rewriteBareRigPolecats(trimmed); ok {
		fmt.Fprintf(os.Stderr, "[gt-agent] ↻ Rewrote `<rig>/polecats` → `<rig>` (CLI requires a specific polecat name or bare rig for auto-spawn): %q -> %q\n", trimmed, rewrote)
		return rewrote, true
	}

	// 4. Reject commands containing unreplaced placeholders like <bead-id>, [bead], or [rig].
	if containsPlaceholder(trimmed) {
		// Log it so the user can see why it failed
		fmt.Fprintf(os.Stderr, "[gt-agent] ⚠ REJECTED hallucinatory command with placeholders: %q\n", trimmed)
		return "true", true
	}

	// 4. Reject stage-wraparound slings: mayor slinging an upstream agent's
	// own handoff mail BACK to that agent. The classic case is mayor reading
	// an "Architecture Ready" mail (subject + wisp owned by <rig>/architect)
	// and then running `gt sling shiny --on hq-wisp-XXXX <rig>/architect`.
	// That sends the architect its own past notification, which is treated
	// as a fresh design assignment, and the architect re-mails mayor
	// "Architecture Ready" — infinite loop. Observed in production
	// generating 62+ duplicate mails in mayor's inbox; see fix #76.
	//
	// Detection is purely syntactic: any `gt sling shiny --on hq-wisp-*
	// <rig>/architect` triggers a synchronous `bd show` to read the bead's
	// title. If the title starts with "Architecture Ready" (case-insensitive)
	// we reject. We don't try to detect wraparound for other stages here —
	// that's job for the routing-table-driven template — this normalizer is
	// the safety net for THE specific loop that has actually bitten us.
	if blockReason := stageWraparoundReason(trimmed); blockReason != "" {
		fmt.Fprintf(os.Stderr, "[gt-agent] ⚠ REJECTED stage-wraparound sling (%s): %q\n", blockReason, trimmed)
		return "true", true
	}

	// 4a. Reject heredocs writing important design/spec files with empty or
	// placeholder-only bodies. The architect template (and others) instruct
	// the LLM to write `cat > .../architecture.md <<'EOF' ... EOF` with the
	// `...` replaced by real content. Observed in production: the LLM took
	// `...` literally (or omitted the body entirely) and overwrote a real
	// 157-byte architecture spec with a 0-byte file. From outside there is
	// no recovery — the spec is just gone.
	//
	// Block at the agent layer: if the heredoc target is a known content
	// file (architecture.md, design.md, plan.md, SPEC.md) and the heredoc
	// body is empty, whitespace-only, or contains only literal placeholder
	// tokens (`...`, `<INSERT-...>`, `<TODO>`), reject the command.
	if isEmptyContentFileHeredoc(trimmed) {
		fmt.Fprintf(os.Stderr, "[gt-agent] ⚠ REJECTED heredoc writing an empty/placeholder-only body to a content file: %q\n", trimmed)
		return "true", true
	}
	if hasInvalidSlingOnBead(trimmed) {
		return "true", true
	}

	// Fix #122 / #123: Mayor must not delete kickoff / project-assignment mail
	// (Fix #122) or planner "BLOCKED:" status mail (Fix #123 — deleting hides
	// actionable routing failures). Resolve subjects from `gt mail inbox --json`
	// and reject deletes on protected subjects. Fail open when lookup is empty
	// or the ID is not in the snapshot.
	if mayorKickoffMailDeleteBlocked(trimmed) {
		fmt.Fprintf(os.Stderr, "[gt-agent] ⚠ REJECTED mayor `gt mail delete` on protected mail (Fix #122/#123): %q\n", trimmed)
		return "true", true
	}

	// 5. Handle "checking in" announcements as NOPs (frequently hallucinated from Startup Protocol)
	if strings.Contains(trimmed, "checking in") {
		return "true", true // 'true' is a shell NOP that exits 0
	}

	return trimmed, rewritten
}

func hasCommandPrefix(cmd, prefix string) bool {
	if cmd == prefix {
		return true
	}
	return strings.HasPrefix(cmd, prefix+" ")
}

// knownHandoffRoles enumerates the cross-role handoff targets the LLM
// commonly types. Only these are rewritten to `gt mail send`. A handoff
// of a bead id (e.g. `gt handoff hq-wisp-foo`) or a fully-qualified
// session name is left untouched.
var knownHandoffRoles = map[string]bool{
	"mayor":     true,
	"planner":   true,
	"architect": true,
	"qa":        true,
	"witness":   true,
	"refinery":  true,
	"mechanic":  true,
	"deacon":    true,
	"crew":      true,
}

// rewriteHandoffToMail converts `gt handoff <role> [-s X] [-m Y]` into
// `gt mail send <role>/ [-s X] [-m Y]`. Returns (newCmd, true) on rewrite
// or ("", false) if cmd is not in the expected cross-role-handoff shape.
//
// We deliberately preserve everything after `-s` / `-m`, including the
// shell quoting the LLM emitted, by working on token boundaries via
// strings.Fields and then re-quoting subject/message values that contain
// whitespace.
func rewriteHandoffToMail(cmd string) (string, bool) {
	parts := strings.Fields(cmd)
	if len(parts) < 3 || parts[0] != "gt" || parts[1] != "handoff" {
		return "", false
	}
	role := strings.TrimSuffix(parts[2], "/")
	if !knownHandoffRoles[role] {
		return "", false
	}

	// Parse remaining tokens looking for -s / -m / --subject / --message
	// values. We pass everything else through untouched (e.g. --yes, -c).
	subject := ""
	message := ""
	var extra []string
	for i := 3; i < len(parts); i++ {
		tok := parts[i]
		switch tok {
		case "-s", "--subject":
			if i+1 < len(parts) {
				subject, i = joinQuotedValue(parts, i+1)
			}
		case "-m", "--message":
			if i+1 < len(parts) {
				message, i = joinQuotedValue(parts, i+1)
			}
		case "-y", "--yes":
			// gt mail send doesn't need a confirmation flag — drop it.
		default:
			extra = append(extra, tok)
		}
	}

	out := []string{"gt", "mail", "send", role + "/"}
	if subject != "" {
		out = append(out, "-s", shellQuote(subject))
	}
	if message != "" {
		out = append(out, "-m", shellQuote(message))
	}
	out = append(out, extra...)
	return strings.Join(out, " "), true
}

// joinQuotedValue is a small helper that consumes one logical "value"
// from a tokenized command line, re-joining tokens that were originally
// inside a quoted string. It returns the unquoted string and the index
// of the last token consumed.
//
// Example: tokens [`"Architecture`, `Ready"`] starting at i=0 returns
// ("Architecture Ready", 1).
func joinQuotedValue(parts []string, start int) (string, int) {
	tok := parts[start]
	// Single token, no opening quote → return as-is.
	if !(strings.HasPrefix(tok, "\"") || strings.HasPrefix(tok, "'")) {
		return trimQuotes(tok), start
	}
	quote := tok[:1]
	// Same-token quoted value, e.g. `"Architecture"`.
	if strings.HasSuffix(tok, quote) && len(tok) > 1 {
		return strings.Trim(tok, quote), start
	}
	// Multi-token quoted value — find the closing quote.
	buf := []string{strings.TrimPrefix(tok, quote)}
	for j := start + 1; j < len(parts); j++ {
		t := parts[j]
		if strings.HasSuffix(t, quote) {
			buf = append(buf, strings.TrimSuffix(t, quote))
			return strings.Join(buf, " "), j
		}
		buf = append(buf, t)
	}
	// No closing quote found — return what we have.
	return strings.Join(buf, " "), len(parts) - 1
}

// shellQuote wraps s in double quotes, escaping any embedded `"` so that
// the resulting token is safe to splice into a /bin/sh -c command line.
func shellQuote(s string) string {
	if s == "" {
		return "\"\""
	}
	if !strings.ContainsAny(s, " \t\"'\\") {
		return s
	}
	return "\"" + strings.ReplaceAll(s, "\"", "\\\"") + "\""
}

func rewriteMolCurrent(cmd string) string {
	parts := strings.Fields(cmd)
	args := parts[3:]
	if len(args) == 0 {
		return "gt mol current"
	}
	return fmt.Sprintf("gt mol current %s", strings.Join(args, " "))
}

var placeholderRe = regexp.MustCompile(`<[a-zA-Z0-9_-]+>`)

// snakeBracketPlaceholderRe matches the classic snake_case-style LLM
// hallucination `[foo_bar]`, `[command_for_item_X]`, `[task_name]`, etc.
// We deliberately require at least one underscore so we don't trample
// legitimate shell uses of `[foo]` (test brackets, array access, etc.).
var snakeBracketPlaceholderRe = regexp.MustCompile(`\[[A-Za-z][A-Za-z0-9]*(?:_[A-Za-z0-9]+)+\]`)

// contentFileHeredocOpenRE matches just the OPENING of a heredoc whose
// redirect target is a known content file the agents must never overwrite
// with an empty or placeholder-only body. Captures:
//
//	[1] the heredoc delimiter (e.g. EOF, MARK, END)
//
// We only match the basename of the file (architecture.md, design.md,
// plan.md, SPEC.md, README.md) so any path prefix is accepted.
//
// Go's RE2 engine does not support backreferences, so we deliberately
// match only the opening here and locate the closing delimiter with a
// separate string scan in isEmptyContentFileHeredoc.
var contentFileHeredocOpenRE = regexp.MustCompile(
	`cat\s*>\s*\S*?(?:architecture|design|plan|SPEC|README)\.md\s*<<-?\s*['"]?([A-Za-z0-9_]+)['"]?\s*\n`,
)

// heredocBodyIsPlaceholder returns true if `body` contains nothing
// substantive — only whitespace, only the literal ellipsis `...`, or only
// unreplaced template placeholders like `<INSERT-…>`, `<TODO>`, or
// `<FIXME>`. Reject such heredocs at the agent layer so an LLM that
// pastes the placeholder verbatim cannot blow away a real spec file.
func heredocBodyIsPlaceholder(body string) bool {
	// Empty / whitespace-only body.
	stripped := strings.TrimSpace(body)
	if stripped == "" {
		return true
	}
	// Body is just literal `...` (with optional surrounding whitespace).
	if stripped == "..." {
		return true
	}
	// Strip every line that is empty, a placeholder marker, or pure `...`.
	// If nothing of substance remains, the body is effectively empty.
	placeholderLineRE := regexp.MustCompile(`^\s*(?:\.\.\.|#?\s*<\s*(?:INSERT[-_A-Za-z0-9]*|TODO|FIXME|PLACEHOLDER|YOUR[-_A-Za-z0-9]*)\s*[^>]*>\s*)\s*$`)
	var substantive int
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if placeholderLineRE.MatchString(line) {
			continue
		}
		// Markdown headings alone (e.g. `## API`) are not substantive content
		// without any following prose. They count, but only weakly — require
		// at least one non-heading non-placeholder line OR a heading >40
		// chars in total. This avoids accepting `## API\n## Data Model` etc.
		// as "real architecture".
		if strings.HasPrefix(t, "#") && len(t) < 40 {
			continue
		}
		substantive++
	}
	return substantive == 0
}

// isEmptyContentFileHeredoc returns true for a shell command that writes
// an empty or placeholder-only heredoc to a known content file
// (architecture.md / design.md / plan.md / SPEC.md / README.md).
//
// Implementation: locate every heredoc opening with contentFileHeredocOpenRE,
// then scan forward from end-of-line to find the matching closing
// delimiter (a line that, after trimming whitespace, equals the captured
// delimiter exactly). Return true if any such heredoc has a
// placeholder-only body per heredocBodyIsPlaceholder. We scan every
// occurrence because a chained command (`cat > a.md <<EOF ... EOF; cat
// > b.md <<EOF ... EOF`) might write multiple content files in one shell
// invocation.
func isEmptyContentFileHeredoc(cmd string) bool {
	locs := contentFileHeredocOpenRE.FindAllStringSubmatchIndex(cmd, -1)
	for _, loc := range locs {
		// loc[0]:loc[1] is the full match (the heredoc opening line).
		// loc[2]:loc[3] is the captured delimiter.
		delim := cmd[loc[2]:loc[3]]
		bodyStart := loc[1] // first char after the opening's trailing newline
		// Find the closing delimiter. It must appear at the start of a line
		// (allowing leading whitespace for `<<-` style heredocs) and be the
		// entire line after trimming.
		closeIdx := -1
		// We scan line by line from bodyStart.
		offset := bodyStart
		for offset < len(cmd) {
			nl := strings.IndexByte(cmd[offset:], '\n')
			var line string
			if nl < 0 {
				line = cmd[offset:]
			} else {
				line = cmd[offset : offset+nl]
			}
			if strings.TrimSpace(line) == delim {
				closeIdx = offset
				break
			}
			if nl < 0 {
				break
			}
			offset += nl + 1
		}
		if closeIdx < 0 {
			// Unterminated heredoc — don't reject; let the shell error out.
			continue
		}
		body := cmd[bodyStart:closeIdx]
		if heredocBodyIsPlaceholder(body) {
			return true
		}
	}
	return false
}

func containsPlaceholder(cmd string) bool {
	if placeholderRe.MatchString(cmd) {
		return true
	}
	if snakeBracketPlaceholderRe.MatchString(cmd) {
		return true
	}
	for _, placeholder := range []string{
		"[bead]",
		"[bead-id]",
		"[command]",
		"[epic-id]",
		"[id]",
		"[msg]",
		"[parent]",
		"[rig]",
		"[summary]",
		"[target]",
		"[task-id]",
	} {
		if strings.Contains(cmd, placeholder) {
			return true
		}
	}
	return false
}

// validPatrolVerbs lists the subcommands `gt patrol` actually accepts.
// Anything else (e.g. `gt patrol start`, `gt patrolling …`) is a model
// hallucination and should be NOP'd rather than executed against the CLI.
var validPatrolVerbs = map[string]bool{
	"new":    true,
	"report": true,
	"scan":   true,
	"digest": true,
}

// mailSendFormulaSubjectRE matches mail subjects that are obviously
// content-free formula-name pings, with or without `RE:` prefix and
// with or without quotes. Examples that match:
//
//	-s "mol-refinery-patrol"
//	-s 'RE: mol-refinery-patrol'
//	-s mol-witness-patrol
//	-s "RE: mol-polecat-work"
//
// Formula names are agent-internal molecule identifiers, never
// human-meaningful subjects. When an LLM emits one as a subject it is
// always hallucinating a status ping. See Fix #90.
var mailSendFormulaSubjectRE = regexp.MustCompile(`^(?i)(re:\s*)?mol-[a-z0-9-]+$`)

// mailSendVaguePolecatAlertRE matches subjects that are vague
// polecat-status alerts ("Polecat appears stalled", "polecat stuck",
// "Polecat is dead"). Such an alert is only legitimate when the
// subject or body also names a specific polecat (rig/name) or wisp
// ID. See Fix #92.
var mailSendVaguePolecatAlertRE = regexp.MustCompile(
	`^(?i)(re:\s*)?polecat(s)?(\s+(appears|is|seems|looks|may\s+be))?\s+(stalled|stuck|dead|hung|hanged|frozen|unresponsive|stopped)\s*$`)

// mailSendPolecatAddressRE matches a `<rig>/<polecat-name>` address in
// subject or body. The address proves the alert is about a specific
// polecat, not a generic status ping. See Fix #92.
var mailSendPolecatAddressRE = regexp.MustCompile(`\b[a-z][a-z0-9-]*\/[a-z][a-z0-9_-]*\b`)

// mailSendWispIDRE matches a wisp ID (`hq-wisp-<suffix>` or
// `<rig>-<id>`). A wisp ID also satisfies the "concrete reference"
// requirement for polecat-status alerts. See Fix #92.
var mailSendWispIDRE = regexp.MustCompile(`\b(hq-wisp-[a-z0-9]+|hq-[a-z0-9]+\.\d+|hq-[a-z0-9]{4,})\b`)

// mailSendPatrolStatusRE matches subjects that are patrol-cycle
// status pings ("Patrol Cycle #531 Complete", "Patrol Initiated",
// "Patrol Cycle Started", "Patrol Complete"). The recipient already
// knows the agent patrols on a timer; an inbox notification adds
// nothing. See Fix #92.
var mailSendPatrolStatusRE = regexp.MustCompile(
	`^(?i)(re:\s*)?patrol(\s+cycle)?(\s+#?\d+)?\s+(complete(d)?|initiated|started|finished|ended|done|nominal|ok)\s*$`)

// mailSendMergeSignalRE matches subjects of the merge-coordination
// protocol between witness and refinery: `MERGE_READY <id>`,
// `MERGE_FAILED <id>`, `MERGE_SKIPPED <id>`, `MERGE_STALE <id>`,
// `MERGE_CONFLICT <id>`, `MERGE_COMPLETE <id>`, plus `RE:`-prefixed
// variants. A legitimate mail of this shape always carries a non-empty
// body with branch info / failure reason / merge timestamp (see the
// "MERGE_FAILED with real content" / "MERGED announcement" test cases
// in TestHasContentFreeMailSend). Small LLMs hallucinate the subject
// with NO body every patrol cycle, naming patrol-wisp IDs as if they
// were polecat branches (e.g. `MERGE_READY hq-wisp-n460`). Without
// content there is nothing for the recipient to act on.
//
// See Fix #113 (witness/refinery feedback-loop regression of Fix #90/#92).
var mailSendMergeSignalRE = regexp.MustCompile(
	`^(?i)(re:\s*)?merged?(_(ready|failed|skipped|stale|conflict|complete|done|noop))?\b`)

// mailSendInvestigateForwardRE matches the Mayor's hallucinated
// "investigate this BLOCKED" / "check the canonical locations" /
// "please create architecture" forwards that turn a real BLOCKED
// signal into a chain of useless mails between agents. Fix #114.
//
// Observed in the hq-mayor log on 2026-05-12 immediately after a
// `BLOCKED: missing architecture file` mail from planner: Mayor
// emitted
//   gt mail send planner/ -s "Investigate BLOCKED: missing architecture file" \
//       -m "Check canonical locations for architecture file and verify mol-planner-patrol for details."
// then later
//   gt mail send <rig>/architect -s "Create architecture" \
//       -m "Please create the architecture file in /home/stevef/gt/..."
// Both produce nothing but more BLOCKED replies. The mayor template
// (Critical Rule #6, Fix #114) forbids them — this regex enforces it
// at the gt-agent boundary so a misbehaving LLM cannot bypass the
// rule by phrasing things slightly differently.
var mailSendInvestigateForwardRE = regexp.MustCompile(
	`^(?i)(re:\s*)?(investigate|check|verify|review|look\s+into|please\s+(create|investigate|check|fix|resolve))\b`)

// mailSendCreateArchitectureRE matches a Mayor mail that asks an
// architect (or anyone) to "create architecture" / "create the
// architecture file" / "write architecture.md". The architect's
// design step is already started via `gt sling shiny` at Stage 0;
// mailing the same agent to "create architecture" is the Mayor's
// way of trying to recover when it (wrongly) thinks the sling
// didn't work. The right response is to investigate why the
// architect went idle, not to re-mail the request — re-mailing
// just delivers redundant work that the role template rejects.
// Fix #114.
var mailSendCreateArchitectureRE = regexp.MustCompile(
	`^(?i)(re:\s*)?(please\s+)?(create|write|generate|produce|make)\s+(an?\s+|the\s+)?architecture(\.md|\s+file|\s+document)?\s*$`)

// mailSendREStatusRE matches replies to IDLE / REPORT: idle status
// pings. The Mayor (and other coordinators) sometimes hallucinate a
// chain of `gt mail send <sender>/ -s "RE: IDLE" -m "Acknowledged"`
// after every IDLE notification, even though Fix #90's echo-body
// rule catches some variants. This regex catches the residual:
// any `RE: IDLE` / `RE: REPORT: idle` / `RE: IDLE: no work` etc.
// with a body shorter than 80 chars is an echo. Fix #114.
var mailSendREIdleRE = regexp.MustCompile(
	`^(?i)re:\s*(idle|report:\s*idle|idle:\s*no\s+work)\b`)

// mailSendProtocolNoiseRE matches internal protocol/status chatter that
// should not be sent as normal inbox mail between agents. These subjects
// were observed flooding witness inboxes from refinery patrol loops.
// They are transport-level/reporting artifacts, not actionable work
// requests. Fix #118.
var mailSendProtocolNoiseRE = regexp.MustCompile(
	`^(?i)(re:\s*)?(patrol_finish|patrol_clear|action_received|hook_error|mail_read_error|mail_error_report_ack|reply_to_nudge)\b`)

// mailSendEchoBodyRE matches bodies that just echo the subject, e.g.
//
//	"Reply to witness regarding mol-refinery-patrol"
//	"Status update: mol-refinery-patrol"
//	"<subject>"   (literal echo)
//	"RE: <subject>"
//
// These are the canonical small-LLM "I should send a status mail"
// hallucinations: 1-line, no operational data, the body is just a
// rephrasing of the subject. See Fix #90.
var mailSendEchoBodyPhrases = []string{
	"reply to ",
	"status update",
	"acknowledged",
	"acknowledgement",
	"acknowledgment",
	"received",
	"noted",
	"ack",
}

// hasContentFreeMailSend reports whether `cmd` is a `gt mail send` /
// `gt mail reply` invocation whose subject + body combination indicate
// a content-free LLM hallucination. Returns the reason string for
// logging when the rejection fires.
//
// Heuristics (any one is enough to reject):
//
//  1. Subject is a formula name (`mol-*`, optionally `RE:`-prefixed).
//     Formula names are internal identifiers and never legitimate mail
//     subjects.
//  2. Subject starts with `RE:` AND there is no `-m`/`--message`/`--stdin`
//     content provided. A reply with no actual reply text is noise.
//  3. Subject is non-empty, body is short (<= 80 chars) AND consists
//     entirely of a generic ack phrase + a literal echo of the
//     subject. This is the "Reply to <rig>/<role> regarding <subj>"
//     signature.
//
// Returns (reason, true) when rejecting, ("", false) when the command
// looks legitimate. Heredoc bodies (`--stdin <<EOF ... EOF`) are
// treated as legitimate because no observed hallucination uses them.
func hasContentFreeMailSend(cmd string) (string, bool) {
	trimmed := strings.TrimSpace(cmd)
	if !strings.HasPrefix(trimmed, "gt mail send") && !strings.HasPrefix(trimmed, "gt mail reply") {
		return "", false
	}
	// Heredoc bodies are always legitimate — observed hallucinations
	// are single-line `-m "..."`. Don't second-guess multi-line.
	if strings.Contains(trimmed, "<<") {
		return "", false
	}
	subject, body, ok := extractMailSubjectAndBody(trimmed)
	if !ok {
		return "", false
	}
	subj := strings.TrimSpace(subject)
	if subj == "" {
		return "", false
	}
	if mailSendFormulaSubjectRE.MatchString(subj) {
		return "subject is a bare formula name", true
	}
	bodyTrim := strings.TrimSpace(body)
	if strings.HasPrefix(strings.ToLower(subj), "re:") && bodyTrim == "" {
		return "RE: reply with empty body", true
	}
	// Vague stalled/stuck/dead polecat alert with no concrete address.
	//
	// The witness template historically included a literal example
	// `gt mail send mayor/ -s "Polecat appears stalled" -m "..."`.
	// Small LLMs copy this verbatim every patrol cycle, producing
	// 10+ identical alerts per minute to the mayor with no actual
	// polecat identified. Fix #85 principle: example text in a
	// prompt becomes a regurgitation pattern.
	//
	// To be legitimate, this kind of subject MUST mention either a
	// rig/name polecat address or a specific wisp ID in the subject
	// OR body. If neither is present, the alert is content-free.
	if mailSendVaguePolecatAlertRE.MatchString(subj) {
		combined := strings.ToLower(subj + " " + bodyTrim)
		hasAddress := mailSendPolecatAddressRE.MatchString(combined) ||
			mailSendWispIDRE.MatchString(combined)
		if !hasAddress {
			return "polecat-status alert without concrete address (Fix #92)", true
		}
	}
	// Patrol cycle status with no concrete finding.
	//
	// "Patrol Cycle #N Complete" / "Patrol Complete" / "Patrol
	// Initiated" — these are operational chatter the LLM emits at
	// the start and end of every patrol turn. They carry zero
	// signal to the recipient (the mayor / witness / refinery
	// already know patrols are running; they show up in `gt
	// patrol report`). The body is invariably "all systems
	// nominal" / "no actionable items" / "sweep complete".
	if mailSendPatrolStatusRE.MatchString(subj) {
		return "patrol-cycle status ping (Fix #92)", true
	}
	// MERGE_* coordination subject with no body. The witness ↔
	// refinery merge protocol REQUIRES the body to carry branch /
	// failure / timestamp data — a bare `MERGE_READY hq-wisp-n460`
	// with no -m flag is the hallucinated form that flooded HQ with
	// 30+ junk beads per kickoff before this rule landed (observed
	// in an earlier trial). Real `MERGE_FAILED <id>`
	// and `MERGED <id>` mails always carry a body, so they pass.
	// See Fix #113.
	if bodyTrim == "" && mailSendMergeSignalRE.MatchString(subj) {
		return "MERGE_* coordination mail with empty body (Fix #113)", true
	}
	// Mayor's BLOCKED-fan-out hallucinations (Fix #114). When a mayor
	// receives a `BLOCKED: ...` mail from planner/architect/etc., its
	// template tells it to DELETE and stop. The LLM sometimes routes
	// around the rule by mailing the same sender back with a slightly
	// reworded subject ("Investigate BLOCKED: ...", "Please create
	// architecture", "Check canonical locations"). All of these are
	// noise — the upstream agent has already told the mayor exactly
	// what is wrong and re-mailing it just delivers another BLOCKED.
	if mailSendInvestigateForwardRE.MatchString(subj) {
		return "investigate/please-do forward of a BLOCKED signal (Fix #114)", true
	}
	if mailSendCreateArchitectureRE.MatchString(subj) {
		return "Create-architecture mail duplicates the Stage 0 sling (Fix #114)", true
	}
	// RE: IDLE / RE: REPORT: idle ack — same shape as Fix #90's echo
	// guard but specifically the IDLE status family that small LLMs
	// have been known to reply to in a loop. Body is irrelevant; the
	// only legitimate "RE: IDLE" reply is no reply at all.
	if mailSendREIdleRE.MatchString(subj) {
		return "RE: IDLE acknowledgement noise (Fix #114)", true
	}
	// Internal protocol/status chatter should not be mailed into agent
	// inboxes; it creates persistent bead spam and no actionable signal.
	if mailSendProtocolNoiseRE.MatchString(subj) {
		return "protocol/status subject noise (Fix #118)", true
	}
	// Body just restates the subject (e.g. subject `NO_POLECATS_FOUND`,
	// body `No polecats found`). This is the second-most-common
	// content-free pattern: the LLM writes a SHOUTY_SUBJECT and then
	// rephrases it in lowercase prose as the body. Every refinery
	// patrol cycle was sending one of these per state it observed
	// (`NO_POLECATS_FOUND`, `MERGE_QUEUE_EMPTY`, `MERGE_QUEUE_NONEMPTY`,
	// `REFINERY_STATUS`) and flooding the witness inbox.
	//
	// Heuristic: if every meaningful body token also appears in the
	// subject, the body adds no information.
	if bodyTrim != "" && len(bodyTrim) <= 160 {
		subjectTokens := map[string]bool{}
		for _, tok := range tokenizeAlnum(strings.TrimPrefix(strings.ToLower(subj), "re:")) {
			subjectTokens[tok] = true
		}
		// Generic filler that doesn't count toward "real content".
		filler := map[string]bool{
			"to": true, "from": true, "the": true, "a": true, "an": true,
			"of": true, "for": true, "in": true, "on": true, "is": true,
			"are": true, "was": true, "were": true, "be": true,
			"this": true, "that": true, "these": true, "those": true,
			"and": true, "or": true, "but": true, "no": true, "not": true,
			"any": true, "some": true, "all": true, "regarding": true,
			"about": true, "re": true,
			// Role/agent names — never carry content on their own.
			"witness": true, "mayor": true, "refinery": true,
			"polecat": true, "polecats": true, "deacon": true,
			"architect": true, "qa": true, "planner": true, "mechanic": true,
		}
		bodyTokens := tokenizeAlnum(bodyTrim)
		extra := 0
		for _, tok := range bodyTokens {
			if filler[tok] || subjectTokens[tok] {
				continue
			}
			extra++
		}
		// Up to 1 "extra" token is tolerated (typos, "this is X" filler).
		if len(bodyTokens) > 0 && extra <= 1 {
			return "body restates the subject (no new information)", true
		}
	}
	// Short body that's just an ack phrase + subject echo.
	//
	// We tokenize on word boundaries (NOT substring replace) so that
	// stripping "a" doesn't carve "regarding" into "regrding" and let
	// real-looking-but-empty content slip through. The body is
	// considered content-free when, after removing:
	//   - the subject tokens (case-insensitive)
	//   - any ack phrase (`reply to`, `acknowledged`, `noted`, ...)
	//   - generic filler words (`to`, `the`, `regarding`, `witness`,
	//     `mayor`, `refinery`, role names, etc.)
	// there are <= 1 meaningful tokens left. That correctly catches
	//
	//   "Reply to witness regarding NO_POLECATS_FOUND"  → 0 tokens
	//   "acknowledged"                                  → 0 tokens
	//   "Status update: mol-refinery-patrol"            → 0 tokens
	//
	// while preserving real content such as
	//
	//   "Branch tests failed: 3 errors in build_test.go" → 5+ tokens.
	if bodyTrim != "" && len(bodyTrim) <= 120 {
		lower := strings.ToLower(bodyTrim)
		hasAck := false
		for _, phrase := range mailSendEchoBodyPhrases {
			if strings.Contains(lower, phrase) {
				hasAck = true
				break
			}
		}
		if hasAck {
			meaningful := residualMeaningfulTokens(lower, subj)
			if len(meaningful) <= 1 {
				return "body just echoes subject with ack phrase", true
			}
		}
	}
	return "", false
}

// residualMeaningfulTokens returns the body tokens that remain after
// stripping subject tokens, ack phrases, and generic filler. Tokens
// are lowercase alphanumeric runs separated by punctuation/whitespace.
//
// We never substring-replace; this avoids the carving bug where
// stripping `a` from "regarding" produces a bogus "regrding" token.
// See Fix #90.
func residualMeaningfulTokens(lowerBody, subject string) []string {
	skip := map[string]bool{}
	// Ack phrases as whole tokens (multi-word phrases will be matched
	// before tokenizing).
	for _, phrase := range mailSendEchoBodyPhrases {
		for _, tok := range tokenizeAlnum(phrase) {
			skip[tok] = true
		}
	}
	// Subject tokens (after stripping `re:`).
	subjTok := strings.TrimSpace(strings.TrimPrefix(strings.ToLower(subject), "re:"))
	for _, tok := range tokenizeAlnum(subjTok) {
		skip[tok] = true
	}
	// Generic English filler + the agent role names we see in
	// hallucinated bodies.
	for _, w := range []string{
		"to", "from", "the", "a", "an", "of", "for", "in", "on",
		"regarding", "about", "re", "and", "or", "is", "this", "that",
		"witness", "mayor", "refinery", "polecat", "polecats", "rust",
		"deacon", "architect", "qa", "planner", "mechanic",
	} {
		skip[w] = true
	}

	var out []string
	for _, tok := range tokenizeAlnum(lowerBody) {
		if skip[tok] {
			continue
		}
		out = append(out, tok)
	}
	return out
}

// tokenizeAlnum splits s into lowercase alphanumeric runs. Runs of
// non-alphanumeric chars act as separators. Empty tokens are dropped.
func tokenizeAlnum(s string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

// extractMailSubjectAndBody parses `-s/--subject` and `-m/--message`
// flag values out of a `gt mail send` command line, handling single
// and double quotes. Returns ("", "", false) if no subject is found.
func extractMailSubjectAndBody(cmd string) (subject, body string, ok bool) {
	fields, err := splitShellCmd(cmd)
	if err != nil {
		return "", "", false
	}
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "-s", "--subject":
			if i+1 < len(fields) {
				subject = fields[i+1]
				ok = true
				i++
			}
		case "-m", "--message", "--body":
			if i+1 < len(fields) {
				body = fields[i+1]
				i++
			}
		default:
			if strings.HasPrefix(fields[i], "-s=") {
				subject = strings.TrimPrefix(fields[i], "-s=")
				ok = true
			} else if strings.HasPrefix(fields[i], "--subject=") {
				subject = strings.TrimPrefix(fields[i], "--subject=")
				ok = true
			} else if strings.HasPrefix(fields[i], "-m=") {
				body = strings.TrimPrefix(fields[i], "-m=")
			} else if strings.HasPrefix(fields[i], "--message=") {
				body = strings.TrimPrefix(fields[i], "--message=")
			}
		}
	}
	return subject, body, ok
}

// splitShellCmd is a tiny shell-style tokenizer that handles single
// and double quotes (no escapes, no command substitution). It is good
// enough for inspecting flag args; it does NOT execute anything.
func splitShellCmd(cmd string) ([]string, error) {
	var (
		fields  []string
		cur     strings.Builder
		inSingle, inDouble bool
	)
	flush := func() {
		if cur.Len() > 0 {
			fields = append(fields, cur.String())
			cur.Reset()
		}
	}
	for _, r := range cmd {
		switch {
		case r == '\'' && !inDouble:
			inSingle = !inSingle
		case r == '"' && !inSingle:
			inDouble = !inDouble
		case (r == ' ' || r == '\t') && !inSingle && !inDouble:
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	if inSingle || inDouble {
		return nil, fmt.Errorf("unterminated quote")
	}
	return fields, nil
}

// hasInvalidPatrolCommand returns true for `gt patrol*` invocations the
// CLI cannot satisfy: unknown verbs (`start`, `cycle`, `loop`) and the
// invented `gt patrolling …` form that the planner has been generating.
// A bare `gt patrol` (which prints help) is left alone so the agent can
// learn the right syntax from stderr.
func hasInvalidPatrolCommand(cmd string) bool {
	trimmed := strings.TrimSpace(cmd)
	parts := strings.Fields(trimmed)
	if len(parts) < 2 || parts[0] != "gt" {
		return false
	}
	// `gt patrolling …` is never a real command.
	if parts[1] == "patrolling" {
		return true
	}
	if parts[1] != "patrol" {
		return false
	}
	if len(parts) < 3 {
		// `gt patrol` alone is fine — prints help.
		return false
	}
	verb := parts[2]
	if strings.HasPrefix(verb, "-") {
		// `gt patrol --help`, `gt patrol -h`, etc. — let the CLI handle it.
		return false
	}
	if !validPatrolVerbs[verb] {
		return true
	}
	// Catch the recurring `gt patrol new -f <url>` / `--httpf` /
	// `-c <n>` hallucinations. `gt patrol new` only accepts --role.
	if verb == "new" {
		for _, p := range parts[3:] {
			switch p {
			case "-f", "--file", "--httpf", "-c", "--cycle":
				return true
			}
		}
	}
	// Catch the recurring `gt patrol report --incident --status
	// --recommendation --sirius …` hallucinations. The real command
	// only accepts --summary (required) and --steps. Any other flag
	// indicates the model is fabricating an API.
	if verb == "report" {
		validReportFlags := map[string]bool{
			"--summary": true, "-s": true,
			"--steps": true,
			"--help":  true, "-h": true,
			"--json": true, "--verbose": true, "-v": true,
		}
		for _, p := range parts[3:] {
			if !strings.HasPrefix(p, "-") {
				continue
			}
			flag := p
			if i := strings.Index(p, "="); i >= 0 {
				flag = p[:i]
			}
			if !validReportFlags[flag] {
				return true
			}
		}
	}
	return false
}

// shinyRestrictedRoles are the roles that should never kick off the
// `shiny` engineering formula (design → implement → review → test →
// submit). Patrol/governor roles slung shiny at their own patrol bead
// is the recurring witness/refinery/deacon hallucination this guard
// catches.
//
// Deacon was added (Fix #115) after a session-13 deacon patrol bonded
// `shiny` to its own `mol-deacon-patrol` bead with
// `feature=mol-deacon-patrol`, instantiated five child wisps with
// un-substituted `{{feature}}` placeholders (the substitution only
// resolves cleanly when the formula is bonded with a substantive
// `feature` value, and template-literal "{{feature}}" survived through
// the wisp titles), and mailed Mayor a chain of `Design {{feature}}`,
// `Implement {{feature}}`, `Test {{feature}}`, `URGENT: Merge
// {{feature}}` mails plus a bogus `Architecture Ready` — classic
// downstream noise that the Mayor then tried to route.
var shinyRestrictedRoles = map[string]bool{
	"witness":  true,
	"refinery": true,
	"mechanic": true,
	"qa":       true,
	"planner":  true,
	"deacon":   true, // Fix #115: deacon is a patrol role, not a project kickoff role
}

// hasInvalidShinyFormula returns true when a role that has no business
// running the `shiny` engineering workflow tries to sling it, either via
// `gt sling … --formula shiny …` (legacy syntax) or
// `gt sling shiny …` (modern positional syntax). Only Mayor / Architect /
// Crew / Polecats should ever do that.
func hasInvalidShinyFormula(cmd, role string) bool {
	if !hasCommandPrefix(cmd, "gt sling") {
		return false
	}
	parts := strings.Fields(cmd)
	// Modern positional syntax: `gt sling shiny [args...]` —
	// parts[0]="gt", parts[1]="sling", parts[2]="shiny".
	if len(parts) >= 3 && trimQuotes(parts[2]) == "shiny" {
		return shinyRestrictedRoles[role]
	}
	// Legacy/explicit syntax: `gt sling <bead> --formula shiny …`.
	for i, p := range parts {
		if p != "--formula" || i+1 >= len(parts) {
			continue
		}
		if trimQuotes(parts[i+1]) == "shiny" {
			return shinyRestrictedRoles[role]
		}
	}
	return false
}

// hasInvalidSlingTarget catches a recurring mayor hallucination where the
// LLM copies an unrendered template like `gt sling … /architect` or
// `gt sling … /polecats --create` — i.e. a target with a leading slash
// and no rig prefix. The CLI rejects those with "empty path segment at
// position 0", and the model retries the same bad syntax. We rewrite
// such commands to a harmless `true` so the agent's next turn sees the
// rejection log and corrects course.
//
// We intentionally do NOT try to guess the right rig name — the Mayor
// must learn to substitute the real rig.
func hasInvalidSlingTarget(cmd string) bool {
	if !hasCommandPrefix(cmd, "gt sling") {
		return false
	}
	parts := strings.Fields(cmd)
	// Skip well-known flags + their values so we only inspect positional
	// arguments for the bad leading-slash pattern.
	skip := false
	for i := 2; i < len(parts); i++ {
		p := parts[i]
		if skip {
			skip = false
			continue
		}
		if strings.HasPrefix(p, "--") {
			// Long flags with values: --on X, --formula Y, --merge Z, --crew W,
			// --base-branch B, --message M, --subject S, --agent A, --account C,
			// --max-concurrent N, --args A.
			switch p {
			case "--on", "--formula", "--merge", "--crew", "--base-branch",
				"--message", "--subject", "--agent", "--account",
				"--max-concurrent", "--args", "--var":
				skip = true
			}
			continue
		}
		if strings.HasPrefix(p, "-") {
			// Short flags. Only -a / -m / -s / -n take a value.
			switch p {
			case "-a", "-m", "-s", "-n":
				skip = true
			}
			continue
		}
		// Positional argument: leading slash is the bug we are catching.
		if strings.HasPrefix(p, "/") {
			return true
		}
	}
	return false
}

// slingShinyArchitectRE matches the specific stage-wraparound shape
// `gt sling shiny --on <bead> <rig>/architect`. We deliberately do not
// match other formulas or other targets — the wraparound bug observed
// in production was uniquely the shiny→architect loop after an
// Architecture Ready handoff. Keep this narrow to avoid blocking
// legitimate slings.
var slingShinyArchitectRE = regexp.MustCompile(
	`^gt\s+sling\s+shiny\s+(?:.*\s+)?--on\s+(\S+)\s+\S+/architect(?:\s|$)`,
)

// architectHandoffTitlePrefixes are case-insensitive prefixes that mark
// a bead as an "architect handoff" — i.e. a wisp the architect itself
// created when notifying mayor that design is finished. Slinging shiny
// on such a bead back to the architect creates a stage-wraparound loop.
var architectHandoffTitlePrefixes = []string{
	"architecture ready",
	"architecture complete",
	"architecture location",
	"architecture plan submission",
}

// isArchitectHandoffTitle returns true if title (case-insensitive) starts
// with any of the architect-handoff prefixes.
func isArchitectHandoffTitle(title string) bool {
	t := strings.ToLower(strings.TrimSpace(title))
	for _, p := range architectHandoffTitlePrefixes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// mayorMailInboxSubjectsLookup returns id→subject for the mayor's mailbox,
// used only to block destructive deletes on kickoff-shaped mail (Fix #122).
// Tests stub this; production uses lookupMayorMailInboxSubjectsJSON.
var mayorMailInboxSubjectsLookup = lookupMayorMailInboxSubjectsJSON

// mailInboxSubjectRow is the minimal JSON shape from `gt mail inbox --json`.
type mailInboxSubjectRow struct {
	ID      string `json:"id"`
	Subject string `json:"subject"`
}

func lookupMayorMailInboxSubjectsJSON() map[string]string {
	if gtBinaryPath == "" {
		return nil
	}
	out, err := exec.Command(gtBinaryPath, "mail", "inbox", "--json").CombinedOutput()
	if err != nil || len(out) == 0 {
		return nil
	}
	var rows []mailInboxSubjectRow
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil
	}
	m := make(map[string]string, len(rows))
	for i := range rows {
		if rows[i].ID != "" {
			m[rows[i].ID] = rows[i].Subject
		}
	}
	return m
}

func stripReplyFwdPrefixes(s string) string {
	t := strings.TrimSpace(s)
	for {
		lower := strings.ToLower(t)
		switch {
		case strings.HasPrefix(lower, "re:"):
			t = strings.TrimSpace(t[3:])
		case strings.HasPrefix(lower, "fwd:"):
			t = strings.TrimSpace(t[4:])
		default:
			return t
		}
	}
}

// isKickoffLikeMailSubject matches human / operator kickoff subjects. Keep this
// aligned with mayor.md "kickoff rescue" wording — not every `Project:` bead
// title, only mail subjects we expect on assignment traffic.
func isKickoffLikeMailSubject(subject string) bool {
	t := strings.ToLower(stripReplyFwdPrefixes(subject))
	if t == "" {
		return false
	}
	if strings.HasPrefix(t, "new project") {
		return true
	}
	if strings.HasPrefix(t, "kickoff") {
		return true
	}
	if strings.HasPrefix(t, "project:") {
		return true
	}
	return false
}

// isBlockedStatusMailSubject matches planner (or upstream) "BLOCKED:" subjects.
// Mayor must not delete these as "noise" — they signal missing inputs or bad
// paths and need routing or fix-up, not archive-via-delete (Fix #123).
func isBlockedStatusMailSubject(subject string) bool {
	t := strings.ToLower(stripReplyFwdPrefixes(subject))
	return strings.HasPrefix(t, "blocked:")
}

func mayorKickoffMailDeleteBlocked(cmd string) bool {
	if currentRole != "mayor" {
		return false
	}
	trimmed := strings.TrimSpace(cmd)
	if !hasCommandPrefix(trimmed, "gt mail delete") {
		return false
	}
	parts := strings.Fields(trimmed)
	if len(parts) < 4 || parts[0] != "gt" || parts[1] != "mail" || parts[2] != "delete" {
		return false
	}
	subjectsByID := mayorMailInboxSubjectsLookup()
	if len(subjectsByID) == 0 {
		return false
	}
	for _, id := range parts[3:] {
		subj, ok := subjectsByID[id]
		if !ok || subj == "" {
			continue
		}
		if isKickoffLikeMailSubject(subj) || isBlockedStatusMailSubject(subj) {
			return true
		}
	}
	return false
}

// beadTitleLookup is injected by stageWraparoundReason so tests can
// stub out the `bd show` call. Production sets this to lookupBeadTitle.
var beadTitleLookup = lookupBeadTitle

// lookupBeadTitle shells out to `bd show -o json <bead-id>` and returns
// the bead's title (or "" on any failure). Failures fail OPEN — we'd
// rather let a marginal sling through than strand legitimate work.
func lookupBeadTitle(beadID string) string {
	out, err := exec.Command("bd", "show", "-o", "json", beadID).CombinedOutput()
	if err != nil {
		return ""
	}
	body := string(out)
	idx := strings.Index(body, `"title":`)
	if idx < 0 {
		return ""
	}
	rest := strings.TrimLeft(body[idx+len(`"title":`):], " \t")
	if !strings.HasPrefix(rest, `"`) {
		return ""
	}
	end := strings.IndexByte(rest[1:], '"')
	if end < 0 {
		return ""
	}
	return rest[1 : 1+end]
}

// stageWraparoundReason returns a non-empty string describing the
// wraparound when cmd looks like mayor re-slinging an architect's own
// handoff mail back to the architect. Returns "" if the command is
// either not a sling-shiny-to-architect, or is one but with a legitimate
// (non-handoff) target bead.
//
// Costs one `bd show` per candidate sling command, which is fine — slings
// are rare. If the bd lookup fails for any reason we fail OPEN.
func stageWraparoundReason(cmd string) string {
	m := slingShinyArchitectRE.FindStringSubmatch(cmd)
	if m == nil {
		return ""
	}
	beadID := m[1]
	if strings.ContainsAny(beadID, "<>[]") {
		return ""
	}
	title := beadTitleLookup(beadID)
	if title == "" {
		return ""
	}
	if isArchitectHandoffTitle(title) {
		return "sling shiny→architect on an architect handoff (Stage 2 should sling mol-idea-to-plan to planner, not Stage 1 back to architect)"
	}
	return ""
}

// handoffRoles are the roles whose contract is "do once, hand off,
// stop". When they emit a DONE summary indicating successful handoff,
// the agent should auto-clear its hook so it doesn't keep re-planning
// the same bead next patrol.
//
// Mayor / Witness / Refinery / Mechanic / Deacon are NOT in this list:
// they are coordinators with rolling workloads and should keep their
// hook between cycles.
var handoffRoles = map[string]bool{
	"planner":   true,
	"architect": true,
	"qa":        true,
}

// handoffSummaryKeywords are substrings in a DONE summary that
// indicate the agent has handed off and the hook should be cleared.
// Conservative on purpose: only fire when the model explicitly states
// it has handed off / completed / finished. A summary like
// "still investigating X" must NOT trigger the unhook.
var handoffSummaryKeywords = []string{
	"handed off",
	"hand off",
	"handoff",
	"plan complete",
	"plan ready",
	"design complete",
	"design ready",
	"architecture ready",
	"architecture complete",
	"review complete",
	"qa complete",
	"qa ready",
}

// shouldAutoUnhookAfterHandoff returns true if the (role, summary) pair
// indicates a clean handoff cycle for a single-shot role. Returns false
// for empty summary, non-handoff roles, or summaries that look like
// failure / progress reports.
func shouldAutoUnhookAfterHandoff(role, summary string) bool {
	if !handoffRoles[role] {
		return false
	}
	if summary == "" {
		return false
	}
	lower := strings.ToLower(summary)
	// Negative keywords: don't unhook if the summary signals the work
	// is NOT really finished. The agent is still investigating, blocked,
	// stalled, or reporting an error.
	for _, neg := range []string{
		"unable to proceed",
		"missing",
		"stalled",
		"blocked",
		"error",
		"failure",
		"failed",
		"investigating",
		"in progress",
	} {
		if strings.Contains(lower, neg) {
			return false
		}
	}
	for _, kw := range handoffSummaryKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// rewriteBareRigPolecats rewrites `gt sling … <rig>/polecats …` to
// `gt sling … <rig> …`. Returns (newCmd, true) on rewrite, (cmd, false)
// otherwise.
//
// Targets like `<rig>/polecats/<name>` (a specific polecat) are NOT
// rewritten — those are valid CLI targets.
//
// Because `strings.Fields` would split a quoted multi-word flag value
// (e.g. `--message "see <rig>/polecats for context"`) into separate
// tokens and falsely match the polecats segment as a positional, we
// track quoted-string state and only consider tokens outside quotes.
func rewriteBareRigPolecats(cmd string) (string, bool) {
	if !hasCommandPrefix(cmd, "gt sling") {
		return cmd, false
	}
	parts := strings.Fields(cmd)
	skipNext := false
	insideQuote := false
	rewrote := false
	for i := 2; i < len(parts); i++ {
		p := parts[i]
		// Maintain quote state by counting unescaped " in this token.
		// A token like `"see` opens a quote that closes on a later
		// token containing `context"`. Tokens fully inside a quoted
		// value (no `"`) must be skipped from positional analysis.
		wasInside := insideQuote
		if strings.Count(p, `"`)%2 == 1 {
			insideQuote = !insideQuote
		}
		if wasInside {
			continue
		}
		if skipNext {
			skipNext = false
			continue
		}
		if strings.HasPrefix(p, "--") {
			switch p {
			case "--on", "--formula", "--merge", "--crew", "--base-branch",
				"--message", "--subject", "--agent", "--account",
				"--max-concurrent", "--args", "--var":
				skipNext = true
			}
			continue
		}
		if strings.HasPrefix(p, "-") && !strings.HasPrefix(p, "--") {
			switch p {
			case "-a", "-m", "-s", "-n":
				skipNext = true
			}
			continue
		}
		// Positional. Match bare `<rig>/polecats` (no further /name).
		if strings.HasSuffix(p, "/polecats") && strings.Count(p, "/") == 1 {
			rig := strings.TrimSuffix(p, "/polecats")
			if rig != "" {
				parts[i] = rig
				rewrote = true
			}
		}
	}
	if !rewrote {
		return cmd, false
	}
	return strings.Join(parts, " "), true
}

func hasInvalidSlingOnBead(cmd string) bool {
	if !hasCommandPrefix(cmd, "gt sling") {
		return false
	}
	beadID := extractSlingOnBeadID(cmd)
	if beadID == "" {
		return false
	}
	if !looksLikeBeadID(beadID) {
		fmt.Fprintf(os.Stderr, "[gt-agent] ⚠ REJECTED hallucinatory sling command (invalid bead id): %q\n", beadID)
		return true
	}
	if ok, reason := beadExists(beadID); !ok {
		reason = strings.TrimSpace(reason)
		if reason == "" {
			reason = "bd show failed"
		}
		fmt.Fprintf(os.Stderr, "[gt-agent] ⚠ REJECTED hallucinatory sling command for missing bead %q: %s (cmd=%q)\n", beadID, reason, cmd)
		return true
	}
	return false
}

func extractSlingOnBeadID(cmd string) string {
	parts := strings.Fields(cmd)
	for i, part := range parts {
		if part == "--on" && i+1 < len(parts) {
			return trimQuotes(parts[i+1])
		}
		if strings.HasPrefix(part, "--on=") {
			return trimQuotes(strings.TrimPrefix(part, "--on="))
		}
	}
	return ""
}

func trimQuotes(value string) string {
	if len(value) >= 2 {
		first := value[0]
		last := value[len(value)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

func beadExists(beadID string) (bool, string) {
	if gtBinaryPath == "" {
		return true, ""
	}
	cmd := exec.Command(gtBinaryPath, "bead", "show", beadID, "--json")
	if agentTownRoot != "" {
		cmd.Dir = agentTownRoot
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, string(out)
	}
	return true, ""
}

func looksLikeBeadID(id string) bool {
	if !strings.Contains(id, "-") {
		return false
	}
	for _, r := range id {
		if r == '-' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func updateLastBeadID(cmd string) {
	parts := strings.Fields(cmd)
	if len(parts) < 4 {
		return
	}
	candidate := parts[3]
	if slash := strings.Index(candidate, "/"); slash != -1 {
		candidate = candidate[:slash]
	}
	if strings.HasPrefix(candidate, "te-") {
		lastBeadID = candidate
	}
}

func prependBeadToStep(cmd string) string {
	if lastStepID == "" {
		return cmd
	}
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return cmd
	}
	for i, field := range parts {
		if strings.HasPrefix(field, "step-") || isNumeric(field) {
			parts[i] = lastStepID
			break
		}
	}
	return strings.Join(parts, " ")
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func hasInvalidMolExecute(cmd string) bool {
	if !hasCommandPrefix(cmd, "gt mol execute") {
		return false
	}
	return strings.Contains(cmd, " --on ")
}

// buildSystemPrompt returns a role-specific system prompt for the LLM.
func buildSystemPrompt(role, rig, polecat, townRoot string, patrolCount int, primeContext, effortLevel string) string {
	tmpl, err := templates.New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[gt-agent] Error creating template engine: %v\n", err)
		return "You are a Gas Town agent. Error loading detailed instructions."
	}

	data := templates.RoleData{
		Role:          role,
		RigName:       rig,
		TownRoot:      townRoot,
		WorkDir:       filepath.Join(townRoot, role), // Default assumption
		DefaultBranch: "main",                        // Default assumption
		Polecat:       polecat,
		MayorSession:  "hq-mayor",
		DeaconSession: "hq-deacon",
	}

	// Update WorkDir for specific roles
	switch role {
	case "mayor", "deacon", "planner", "mechanic":
		data.WorkDir = filepath.Join(townRoot, role)
	default:
		if rig != "" && polecat != "" {
			data.WorkDir = filepath.Join(townRoot, rig, "polecats", polecat)
		} else if rig != "" {
			data.WorkDir = filepath.Join(townRoot, rig, role)
		}
	}

	baseOutput, _ := tmpl.RenderRole("base", data)
	output, err := tmpl.RenderRole(role, data)
	if err != nil {
		// Fallback for missing templates or rendering errors
		fmt.Fprintf(os.Stderr, "[gt-agent] Error rendering template for role %q: %v\n", role, err)
		return fmt.Sprintf("You are a Gas Town agent with role: %s.\n\nContext:\n%s", role, primeContext)
	}

	finalOutput := baseOutput + "\n\n" + output

	// Append effort level and prime context if not already in template
	if effortLevel != "" && effortLevel != "full" {
		finalOutput += fmt.Sprintf("\n\nEFFORT LEVEL: %s", effortLevel)
	}
	if primeContext != "" {
		finalOutput += fmt.Sprintf("\n\nContext:\n%s", primeContext)
	}
	finalOutput += fmt.Sprintf("\n\nYou are patrol cycle #%d for this agent session.", patrolCount)

	return finalOutput
}

// postWorkCommand returns the role-specific command to call after work completes.
func postWorkCommand(role, summary string) string {
	switch role {
	case "deacon", "witness":
		if summary == "" {
			summary = "Patrol cycle complete"
		}
		return fmt.Sprintf("patrol report --summary %q", summary)
	default:
		// Mayor, refinery, crew: no post-work command needed
		return ""
	}
}

// gatherWork collects nudges, hook, and mail into work items.
// mailCheck controls whether to include mail (skip for Mechanic).
func gatherWork(gtBin, townRoot, sessionName, role string, mailCheck bool) []string {
	var workItems []string

	// 1. Drain nudge queue
	if sessionName != "" && townRoot != "" {
		nudges, err := nudge.Drain(townRoot, sessionName)
		if err == nil && len(nudges) > 0 {
			fmt.Printf("[gt-agent] Drained %d nudge(s)\n", len(nudges))
			for _, n := range nudges {
				workItems = append(workItems, fmt.Sprintf("[NUDGE from %s] %s", n.Sender, n.Message))
			}
		}
	}

	// 2. Check gt hook for assigned work
	hookOut, err := exec.Command(gtBin, "hook").Output()
	if err == nil && len(hookOut) > 0 {
		hookStr := strings.TrimSpace(string(hookOut))
		// Skip if hook output indicates no work
		if hookStr != "" && hookStr != "No hook" && !strings.Contains(hookStr, "Nothing on hook") {
			workItems = append(workItems, fmt.Sprintf("[HOOK] %s", hookStr))
		}
	}

	// 3. Check mail ONLY for roles that need it
	if mailCheck {
		mailOut, err := exec.Command(gtBin, "mail", "check", "--inject").Output()
		if err == nil && len(mailOut) > 0 {
			mailStr := strings.TrimSpace(string(mailOut))
			if mailStr != "" {
				workItems = append(workItems, fmt.Sprintf("[MAIL] %s", mailStr))
			}
		}
	}

	return workItems
}

func recordMoleculeState(cmd, output string) {
	if !strings.Contains(cmd, "gt mol current") {
		return
	}
	id := parseMoleculeID(output)
	if id != "" {
		lastMoleculeID = id
	}
	if step := parseCurrentStepID(output); step != "" {
		lastStepID = step
	}
}

func parseMoleculeID(output string) string {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Molecule:") {
			parts := strings.Fields(trimmed)
			if len(parts) >= 2 {
				return parts[1]
			}
		}
	}
	return ""
}

func parseCurrentStepID(output string) string {
	if match := jsonStepRe.FindStringSubmatch(output); len(match) == 2 {
		return match[1]
	}
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.Contains(lower, "current step") || strings.Contains(lower, "step:") {
			if m := stepIDRe.FindString(trimmed); m != "" {
				return m
			}
		}
	}
	return ""
}

// callAwaitSignal calls gt mol await-signal and returns the effort level.
func callAwaitSignal(gtBin, agentBead string) string {
	args := []string{"mol", "await-signal", "--agent-bead", agentBead,
		"--backoff-base", "30s", "--backoff-mult", "2", "--backoff-max", "15m"}

	cmd := exec.Command(gtBin, args...)
	out, _ := cmd.CombinedOutput()
	output := string(out)

	if strings.Contains(output, "EFFORT: reduced") {
		return "abbreviated"
	}
	return "full"
}
