package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/steveyegge/gastown/cmd/gt-agent/internal/llm"
	"github.com/steveyegge/gastown/internal/nudge"
	"github.com/steveyegge/gastown/internal/templates"
)

// AgentState persists across cycles so the agent doesn't lose count
// or context when the daemon respawns it.
type AgentState struct {
	PatrolCount         int       `json:"patrol_count"`
	ExtraordinaryAction bool      `json:"extraordinary_action"`
	LastActivity        time.Time `json:"last_activity"`
	IdleCycles          int       `json:"idle_cycles"`
	CmdRetryCount       int       `json:"cmd_retry_count"` // consecutive extraordinary retries
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
	"mayor":     true,
	"deacon":    true,
	"witness":   true,
	"refinery":  true,
	"planner":   true,
	"mechanic":  true,
	"deacon/boot": true, // boot is a deacon variant
}

// isPermanentAgent returns true if the role should never exit on idle cycles.
func isPermanentAgent(role string) bool {
	if permanentAgents[role] {
		return true
	}
	// Also check for rig-specific roles like "testgt1/witness"
	parts := strings.Split(role, "/")
	last := parts[len(parts)-1]
	return permanentAgents[last]
}

var (
	shutdownRequested bool
	shutdownMu        sync.Mutex
)

func main() {
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
func statePath(townRoot, role, rig, polecat string) string {
	var dir string
	switch role {
	case "deacon":
		dir = filepath.Join(townRoot, "deacon")
	case "mayor":
		dir = filepath.Join(townRoot, "mayor")
	default:
		if rig != "" && polecat != "" {
			dir = filepath.Join(townRoot, rig, "polecats", polecat)
		} else if rig != "" {
			dir = filepath.Join(townRoot, rig, "witness")
		} else {
			dir = townRoot
		}
	}
	return filepath.Join(dir, stateFileName)
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

func run() error {
	ctx := context.Background()

	// Read identity from environment
	role := os.Getenv("GT_ROLE")
	if role == "" {
		role = "worker"
	}
	roleCanonical := canonicalRole(role)
	rig := os.Getenv("GT_RIG")
	polecat := os.Getenv("GT_POLECAT")
	townRoot := os.Getenv("GT_ROOT")
	if townRoot == "" {
		var err error
		townRoot, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("cannot determine town root: %w", err)
		}
	}

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

	fmt.Printf("[gt-agent] Starting as %s", role)
	if rig != "" {
		fmt.Printf(" (rig=%s", rig)
		if polecat != "" {
			fmt.Printf(", polecat=%s", polecat)
		}
		fmt.Print(")")
	}
	fmt.Println()
	fmt.Printf("[gt-agent] Using gt binary: %s\n", gtBin)
	fmt.Printf("[gt-agent] PATH: %s\n", os.Getenv("PATH"))

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
	fmt.Printf("[gt-agent] State loaded: patrol_count=%d idle_cycles=%d\n",
		state.PatrolCount, state.IdleCycles)

	// Main event loop
	for {
		if isShutdownRequested() {
			fmt.Println("[gt-agent] Shutdown requested, exiting loop")
			break
		}

		// Gather work from all sources
		workItems := gatherWork(gtBin, townRoot, sessionName)

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
			workItems = gatherWork(gtBin, townRoot, sessionName)
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

		// Build user prompt from work items
		userPrompt := "Execute the following work and report results:\n\n"
		for i, item := range workItems {
			userPrompt += fmt.Sprintf("%d. %s\n", i+1, item)
		}

		// Call LLM
		fmt.Println("[gt-agent] Calling LLM...")
		response, err := client.Complete(ctx, systemPrompt, userPrompt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[gt-agent] LLM completion failed: %v\n", err)
			// Don't exit on LLM error — save state and sleep
			_ = saveState(stateFile, state)
			time.Sleep(baseSleep)
			continue
		}

		// DEBUG: Log LLM response
		debugLen := len(response)
		if debugLen > 500 {
			debugLen = 500
		}
		fmt.Printf("[gt-agent] LLM response (%d chars):\n%s\n---\n", len(response), response[:debugLen])

		// Parse and execute commands
		lines := strings.Split(response, "\n")
		var summary string
		extraordinary := false
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "CMD:") {
				cmd := strings.TrimPrefix(line, "CMD:")
				cmd = strings.TrimSpace(cmd)
				if cmd != "" {
					fmt.Printf("[gt-agent] DEBUG: Original cmd: %q\n", cmd)
					safeCmd, rewritten := normalizeGeneratedCommand(cmd)
					fmt.Printf("[gt-agent] DEBUG: safeCmd: %q, rewritten: %v\n", safeCmd, rewritten)
					if rewritten {
						fmt.Printf("[gt-agent] Rewrote command: %q -> %q\n", cmd, safeCmd)
					}
					fmt.Printf("[gt-agent] $ %s\n", safeCmd)
					out, err := exec.Command("/bin/sh", "-c", safeCmd).CombinedOutput()
					if err != nil {
						// Dolt circuit breaker errors are transient during startup.
						// Retry once after a short delay before marking as extraordinary.
						cmdFailed := true
						if strings.Contains(string(out), "circuit breaker is open") {
							fmt.Println("[gt-agent] Dolt circuit breaker open, retrying in 5s...")
							time.Sleep(5 * time.Second)
							out, err = exec.Command("/bin/sh", "-c", safeCmd).CombinedOutput()
							if err == nil {
								fmt.Printf("[gt-agent] Output (retry OK):\n%s\n", string(out))
								cmdFailed = false
							}
						}
						if cmdFailed {
							fmt.Fprintf(os.Stderr, "[gt-agent] Error: %v\n%s\n", err, string(out))
							extraordinary = true
							// CRITICAL: Stop executing subsequent commands if one fails.
							// Models like Llama-3.3 will hallucinate the entire script. If step 1 fails,
							// executing step 2 is dangerous and guaranteed to fail or corrupt state.
							break
						}
					} else {
						fmt.Printf("[gt-agent] Output:\n%s\n", string(out))
					}
				}
			} else if strings.HasPrefix(line, "DONE:") {
				summary = strings.TrimPrefix(line, "DONE:")
				summary = strings.TrimSpace(summary)
			}
		}

		if summary != "" {
			fmt.Printf("[gt-agent] Summary: %s\n", summary)
		}

		// Override LLM-generated DONE if commands actually failed.
		// The LLM predicts DONE before seeing real command output, so it
		// often claims success despite errors. We correct this here.
		if extraordinary && summary != "" && !strings.Contains(summary, "failed") && !strings.Contains(summary, "error") && !strings.Contains(summary, "could not") {
			summary = "Could not complete: one or more commands failed. Check the output above for details."
			fmt.Printf("[gt-agent] Corrected summary (commands failed): %s\n", summary)
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

// normalizeGeneratedCommand rewrites known-invalid LLM command patterns into
// safe canonical forms to avoid guaranteed molecule lookup failures.
func normalizeGeneratedCommand(cmd string) (string, bool) {
	trimmed := strings.TrimSpace(cmd)
	if strings.HasPrefix(trimmed, "bd mol current") {
		return "gt mol current", true
	}
	return cmd, false
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
			data.WorkDir = filepath.Join(townRoot, rig, "witness")
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
	case "polecat":
		return "done"
	default:
		// Mayor, refinery, crew: no post-work command needed
		return ""
	}
}

// gatherWork collects nudges, hook, and mail into work items.
func gatherWork(gtBin, townRoot, sessionName string) []string {
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
		if hookStr != "" && hookStr != "No hook" {
			workItems = append(workItems, fmt.Sprintf("[HOOK] %s", hookStr))
		}
	}

	// 3. Check mail
	mailOut, err := exec.Command(gtBin, "mail", "check", "--inject").Output()
	if err == nil && len(mailOut) > 0 {
		mailStr := strings.TrimSpace(string(mailOut))
		if mailStr != "" {
			workItems = append(workItems, fmt.Sprintf("[MAIL] %s", mailStr))
		}
	}

	return workItems
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
