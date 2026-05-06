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
)

// AgentState persists across cycles so the agent doesn't lose count
// or context when the daemon respawns it.
type AgentState struct {
	PatrolCount         int       `json:"patrol_count"`
	ExtraordinaryAction bool      `json:"extraordinary_action"`
	LastActivity        time.Time `json:"last_activity"`
	IdleCycles          int       `json:"idle_cycles"`
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
		} else if roleCanonical == "mayor" || roleCanonical == "deacon" || roleCanonical == "witness" || roleCanonical == "refinery" {
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
	llmModel := os.Getenv("LLM_MODEL")
	if llmModel == "" {
		llmModel = "gpt-4o"
	}
	client := llm.NewClient(llmEndpoint, llmModel, roleCanonical)

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

		if len(workItems) == 0 {
			state.IdleCycles++
			sleep := sleepDuration(state.IdleCycles)
			fmt.Printf("[gt-agent] No work (idle_cycle=%d), sleeping %s\n",
				state.IdleCycles, sleep)

			// Permanent agents (Mayor, Deacon, Witness, Refinery) never exit
			// due to idle cycles. They run continuously and wait for work.
			// Only polecats and crew workers exit when idle.
			if !isPermanentAgent(role) && state.IdleCycles >= maxIdleCycles {
				fmt.Println("[gt-agent] Max idle cycles reached, exiting")
				_ = saveState(stateFile, state)
				return nil
			}
			if isPermanentAgent(role) && state.IdleCycles >= maxIdleCycles {
				fmt.Println("[gt-agent] Permanent agent staying alive (no work, continuing patrol)")
				state.IdleCycles = 0 // reset to keep sleep duration reasonable
			}

			_ = saveState(stateFile, state)
			time.Sleep(sleep)
			continue
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
		systemPrompt := buildSystemPrompt(roleCanonical, state.PatrolCount, string(primeOut))

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
					safeCmd, rewritten := normalizeGeneratedCommand(cmd)
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
			fmt.Println("[gt-agent] Extraordinary action detected in this cycle")
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
	if strings.HasPrefix(trimmed, "bd mol current ") {
		arg := strings.TrimSpace(strings.TrimPrefix(trimmed, "bd mol current "))
		if strings.HasPrefix(arg, "mol-") {
			return "gt mol current", true
		}
	}
	return cmd, false
}

// buildSystemPrompt returns a role-specific system prompt for the LLM.
func buildSystemPrompt(role string, patrolCount int, primeContext string) string {
	baseRules := `You have access to shell commands. Execute work step by step.
Rules:
1. Only run commands that are standard Unix utilities or known to exist (git, ls, cat, grep, etc.)
2. Do NOT invent commands or tools that don't exist
3. When you need to run a command, output it on a line starting with "CMD: " followed by the shell command
4. After all commands, output "DONE:" followed by a summary of what was accomplished
5. If you cannot complete the work, output "DONE: Could not complete because ..."
6. You are patrol cycle #%d for this agent session.
7. NEVER output decorative banners, box-drawing characters (═, █, etc.), or emoji-only lines as commands
8. ONLY output actual executable shell commands after "CMD: " - text that cannot be run in a shell is not a command
9. VERIFY command success before proceeding: if a CMD fails, STOP and report the failure in DONE. Do NOT silently continue.
10. Do NOT guess command flags or filenames. If unsure, use "ls" to verify paths or "<command> --help" to check flags first.
11. Filenames are case-sensitive. Verify exact case with "ls" before referencing files.
12. Do NOT claim success in DONE if any step failed. Report partial or full failure honestly.
13. IMPORTANT: Your working directory is <town_root>/<role>/. For example, the mayor runs from ~/gt/mayor/. To access files in other directories (e.g., rig directories), use absolute paths or "../". NEVER assume you are in the town root.`

	switch role {
	case "deacon":
		return fmt.Sprintf(`You are a Gas Town DEACON. You execute the mol-deacon-patrol formula to monitor and maintain town health.

%s
7. Execute ALL patrol formula steps in order. Do NOT skip steps — even "boring" ones.
8. Run "gt deacon heartbeat" as the FIRST command of every patrol cycle.
9. Include a step audit in your summary: "Steps: heartbeat OK | inbox OK | orphan-cleanup OK | ..."
10. Run "gt patrol report --summary '<brief>'" as the LAST command to close the patrol wisp.
11. If you encounter errors, note them and continue with remaining steps.

Context:
%s`, fmt.Sprintf(baseRules, patrolCount), primeContext)

	case "witness":
		return fmt.Sprintf(`You are a Gas Town WITNESS. You monitor polecats and handle lifecycle requests. You execute the mol-witness-patrol formula for per-rig worker monitoring.

%s
7. Check all polecats with "gt polecat list" and "gt session status".
8. Nudge stuck polecats toward completion with "gt nudge <rig>/<name> 'message'".
9. Process LIFECYCLE requests (shutdown, restart) and SPAWN notifications.
10. Run "gt patrol report --summary '<brief>'" at the end of each cycle.
11. To interact with your patrol molecule, use "gt mol status" (check hook) and "gt mol current" (see current step). Do NOT use "gt mol current <formula>".
12. Do NOT close foreign wisps — only close wisps YOU created.

Context:
%s`, fmt.Sprintf(baseRules, patrolCount), primeContext)

	case "mayor":
		return fmt.Sprintf(`You are a Gas Town MAYOR. You coordinate work across all rigs.

%s
7. Dispatch work with "gt sling <bead-id> <rig>" for code changes. Only sling EXISTING beads — never make up bead IDs.
8. Monitor convoys with "gt convoy list".
9. Handle escalations from witnesses.
10. Undock rigs when work is requested, dock them when idle.
11. Use "gt nudge" for routine communication, "gt mail send" only for escalations/handoffs.
12. Before creating beads/issues, verify the correct bd command syntax with "gt beads create --help". bd does NOT have a --rig flag.
13. Before slinging work, verify the bead exists with "gt show <id>".
14. You run from the mayor/ subdirectory. Use absolute paths (e.g., "/home/stevef/gt/defender/crew/steve/SPEC.md") or "../" to access files outside mayor/.

Context:
%s`, fmt.Sprintf(baseRules, patrolCount), primeContext)

	case "refinery":
		return fmt.Sprintf(`You are a Gas Town REFINERY. You process the merge queue.

%s
7. Check merge queue with "gt refinery queue" or equivalent.
8. Review and merge approved MRs.
9. Address CI failures and retry as needed.
10. Call PostMerge() after successful merges.

Context:
%s`, fmt.Sprintf(baseRules, patrolCount), primeContext)

	default:
		// Default: polecat or generic worker
		return fmt.Sprintf(`You are a Gas Town agent with role: %s.

%s
7. Focus on the assigned work. Do NOT run status-checking commands unless needed for your task.
8. Call "gt done" when your work is complete.

Context:
%s`, role, fmt.Sprintf(baseRules, patrolCount), primeContext)
	}
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
