// Standalone test to simulate mechanic agent LLM call
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/steveyegge/gastown/cmd/debug-mechanic/llm"
	"github.com/steveyegge/gastown/internal/agentllm"
	"github.com/steveyegge/gastown/internal/dotenv"
	"github.com/steveyegge/gastown/internal/templates"
	"github.com/steveyegge/gastown/internal/workspace"
)

func main() {
	fmt.Println("=== Mechanic Agent LLM Test ===")
	fmt.Println("(Dev-only: production mechanic uses the shell patrol script, not the LLM.)")

	if loaded, err := dotenv.LoadFromCwd(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: .env: %v\n", err)
	} else if loaded != "" {
		fmt.Printf("Loaded %s\n", loaded)
	}

	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Town root not found: %v\n", err)
		fmt.Fprintln(os.Stderr, "Set GT_ROOT in the environment, or copy .env.example to .env in the gastown repo with GT_ROOT=~/gt")
		os.Exit(1)
	}

	tmpl, err := templates.New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating template engine: %v\n", err)
		os.Exit(1)
	}

	rigName := strings.TrimSpace(os.Getenv("GT_RIG"))
	workDir := filepath.Join(townRoot, "mechanic")
	if wd := strings.TrimSpace(os.Getenv("GT_WORKDIR")); wd != "" {
		workDir = wd
	}

	fmt.Printf("Using town root: %s\n", townRoot)
	if rigName != "" {
		fmt.Printf("Using rig (GT_RIG): %s\n", rigName)
	}
	fmt.Printf("Work dir: %s\n", workDir)

	data := templates.RoleData{
		Role:          "mechanic",
		RigName:       rigName,
		TownRoot:      townRoot,
		WorkDir:       workDir,
		DefaultBranch: "main",
		Polecat:       "",
		MayorSession:  "hq-mayor",
		DeaconSession: "hq-deacon",
	}

	baseOutput, _ := tmpl.RenderRole("base", data)
	mechanicOutput, _ := tmpl.RenderRole("mechanic", data)
	combined := baseOutput + "\n\n" + mechanicOutput

	systemPrompt := combined + "\n\nFormat: CMD: <command> or DONE: <summary>"

	// Simulate a patrol-cycle nudge (mechanic ignores mail; runs log/dolt patrol).
	userPrompt := `Execute the following work and report results:

1. [PATROL] Run mechanic patrol cycle #1 (dolt orphans, then session logs).

You are patrol cycle #1 for this agent session.`

	fmt.Println("=== SYSTEM PROMPT (mechanic role) ===")
	fmt.Println(systemPrompt)
	fmt.Println()

	fmt.Println("=== USER PROMPT ===")
	fmt.Println(userPrompt)
	fmt.Println()

	endpoint := agentllm.ResolveEndpoint()
	model := agentllm.ResolveModel()
	if agentllm.RequiresAuthToken(endpoint) {
		fmt.Println("ERROR: remote LLM_ENDPOINT requires OPENAI_API_KEY or ANTHROPIC_API_KEY")
		fmt.Println("For freeride/local proxy, set LLM_ENDPOINT=http://localhost:11434/v1/chat/completions (default)")
		os.Exit(1)
	}
	fmt.Printf("LLM endpoint: %s\n", endpoint)
	fmt.Printf("LLM model: %s\n", model)

	ctx, cancel := context.WithTimeout(context.Background(), agentllm.ResolveTimeout(60*time.Second))
	defer cancel()

	client := llm.NewClient(endpoint, model, "mechanic", agentllm.ResolveTimeout(60*time.Second))

	resp, err := client.Complete(ctx, systemPrompt, userPrompt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "LLM error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("=== LLM RESPONSE ===")
	fmt.Println(resp)
	fmt.Println()

	analyzeMechanicResponse(resp)
}

// analyzeMechanicResponse prints a dev verdict on whether the model followed mechanic rules.
func analyzeMechanicResponse(resp string) {
	lower := strings.ToLower(resp)
	switch {
	case strings.Contains(lower, "gt mail inbox"), strings.Contains(lower, "mail send"), strings.Contains(lower, "mail read"):
		fmt.Println("❌ PROBLEM: LLM used forbidden mail commands")
	case strings.Contains(lower, "zap-orphans"), strings.Contains(lower, "dolt zap"):
		fmt.Println("✅ GOOD: LLM started patrol with gt dolt zap-orphans (step 1)")
	case strings.Contains(lower, "ls -rt"), strings.Contains(lower, "logs/sessions"):
		fmt.Println("✅ GOOD: LLM is scanning session logs (step 2)")
	case strings.Contains(lower, "tail ") && strings.Contains(lower, ".log"):
		fmt.Println("✅ GOOD: LLM is tailing a session log (step 3)")
	case strings.HasPrefix(strings.TrimSpace(resp), "CMD:") && !strings.Contains(lower, "mail"):
		fmt.Println("✅ GOOD: LLM emitted CMD without mail (acceptable patrol command)")
	default:
		preview := strings.TrimSpace(resp)
		if len(preview) > 120 {
			preview = preview[:120] + "..."
		}
		fmt.Printf("⚠️  REVIEW: unexpected response: %s\n", preview)
	}
}