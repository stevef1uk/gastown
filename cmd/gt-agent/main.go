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
	baseSleep        = 30 * time.Second
	maxSleep         = 15 * time.Minute
	maxIdleCycles    = 20 // exit after 20 idle cycles (~5-30min depending on backoff)
	stateFileName    = "gt-agent-state.json"
)

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
	sessionName := os.Getenv("GT_SESSION_NAME")
	if sessionName == "" && rig != "" && polecat != "" {
		sessionName = fmt.Sprintf("gt-%s-%s", rig, polecat)
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
	client := llm.NewClient(llmEndpoint, llmModel)

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

			if state.IdleCycles >= maxIdleCycles {
				fmt.Println("[gt-agent] Max idle cycles reached, exiting")
				_ = saveState(stateFile, state)
				return nil
			}

			_ = saveState(stateFile, state)
			time.Sleep(sleep)
			continue
		}

		// Reset idle counter when work is found
		state.IdleCycles = 0
		state.LastActivity = time.Now()
		state.PatrolCount++

		fmt.Printf("[gt-agent] Processing %d work item(s) (patrol #%d)\n",
			len(workItems), state.PatrolCount)

		// Load context via gt prime --hook
		primeOut, _ := exec.Command(gtBin, "prime", "--hook").Output()

		// Build system prompt with state
		systemPrompt := fmt.Sprintf(`You are a Gas Town agent with role: %s.

You have access to shell commands. Execute work step by step.
Rules:
1. Only run commands that are standard Unix utilities or known to exist (git, ls, cat, grep, etc.)
2. Do NOT invent commands or tools that don't exist
3. Do NOT run "gt mail inbox" or other status-checking commands — focus on the assigned work
4. When you need to run a command, output it on a line starting with "CMD: " followed by the shell command
5. After all commands, output "DONE:" followed by a summary of what was accomplished
6. If you cannot complete the work, output "DONE: Could not complete because ..."
7. You are patrol cycle #%d for this agent session.

Context:
%s`, role, state.PatrolCount, string(primeOut))

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
					fmt.Printf("[gt-agent] $ %s\n", cmd)
					out, err := exec.Command("/bin/sh", "-c", cmd).CombinedOutput()
					if err != nil {
						fmt.Fprintf(os.Stderr, "[gt-agent] Error: %v\n%s\n", err, string(out))
						extraordinary = true
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

		// Call gt done
		fmt.Println("[gt-agent] Calling gt done...")
		if out, err := exec.Command(gtBin, "done").CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "[gt-agent] gt done failed: %v\n%s\n", err, string(out))
			extraordinary = true
		} else {
			fmt.Printf("[gt-agent] gt done: %s\n", strings.TrimSpace(string(out)))
		}

		// Mark extraordinary if any error occurred
		if extraordinary {
			state.ExtraordinaryAction = true
			fmt.Println("[gt-agent] Extraordinary action detected, will handoff soon")
		}

		// Save state after each cycle
		_ = saveState(stateFile, state)

		// If extraordinary action, exit after this cycle so daemon respawns fresh
		if state.ExtraordinaryAction {
			fmt.Println("[gt-agent] Handoff triggered after extraordinary action")
			break
		}

		// Brief pause before next poll cycle
		time.Sleep(2 * time.Second)
	}

	fmt.Println("[gt-agent] Event loop exited cleanly")
	return nil
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
