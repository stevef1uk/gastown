package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/cmd/gt-agent/internal/llm"
	"github.com/steveyegge/gastown/internal/nudge"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "[gt-agent] Fatal: %v\n", err)
		os.Exit(1)
	}
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

	// Build candidate list. Use our own directory as a hint (gt is typically
	// installed alongside gt-agent), plus common system paths.
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

	// Ensure PATH includes gt and common binary directories so that
	// shell commands (including those spawned by gt itself) work.
	pathEnv := os.Getenv("PATH")
	if pathEnv == "" {
		pathEnv = "/usr/local/bin:/usr/bin:/bin"
	}
	// Resolve home for go/bin even if HOME is not set.
	// Infer from gt binary location (e.g. /home/USER/.local/bin/gt).
	home, _ := os.UserHomeDir()
	if home == "" && gtDir != "" {
		// gtDir like /home/stevef/.local/bin -> home = /home/stevef
		if parent := filepath.Dir(gtDir); parent != "" {
			home = filepath.Dir(parent) // go up two levels from .local/bin
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

	// Gather work from all sources
	workItems := []string{}

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

	if len(workItems) == 0 {
		fmt.Println("[gt-agent] No work found, exiting")
		return nil
	}

	fmt.Printf("[gt-agent] Processing %d work item(s)\n", len(workItems))

	// Load context via gt prime --hook (enables session ID handling,
	// agent-ready signaling, and work context injection)
	primeOut, _ := exec.Command(gtBin, "prime", "--hook").Output()

	// Build system prompt
	systemPrompt := fmt.Sprintf(`You are a Gas Town agent with role: %s.

You have access to shell commands. Execute work step by step.
Rules:
1. Only run commands that are standard Unix utilities or known to exist (git, ls, cat, grep, etc.)
2. Do NOT invent commands or tools that don't exist
3. Do NOT run "gt mail inbox" or other status-checking commands — focus on the assigned work
4. When you need to run a command, output it on a line starting with "CMD: " followed by the shell command
5. After all commands, output "DONE:" followed by a summary of what was accomplished
6. If you cannot complete the work, output "DONE: Could not complete because ..."

Context:
%s`, role, string(primeOut))

	// Build user prompt from work items
	userPrompt := "Execute the following work and report results:\n\n"
	for i, item := range workItems {
		userPrompt += fmt.Sprintf("%d. %s\n", i+1, item)
	}

	// Call LLM
	fmt.Println("[gt-agent] Calling LLM...")
	response, err := client.Complete(ctx, systemPrompt, userPrompt)
	if err != nil {
		return fmt.Errorf("LLM completion failed: %w", err)
	}

	// Parse and execute commands
	lines := strings.Split(response, "\n")
	var summary string
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
	} else {
		fmt.Printf("[gt-agent] gt done: %s\n", strings.TrimSpace(string(out)))
	}

	fmt.Println("[gt-agent] Work complete, exiting")
	return nil
}
