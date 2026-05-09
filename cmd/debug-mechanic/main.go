// Standalone test to simulate mechanic agent LLM call
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/steveyegge/gastown/cmd/debug-mechanic/llm"
	"github.com/steveyegge/gastown/internal/templates"
)

func main() {
	fmt.Println("=== Mechanic Agent LLM Test ===")

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Println("ERROR: Set OPENAI_API_KEY to run this test")
		os.Exit(1)
	}

	tmpl, err := templates.New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating template engine: %v\n", err)
		os.Exit(1)
	}

	data := templates.RoleData{
		Role:          "mechanic",
		RigName:       "testgt2",
		TownRoot:      "/home/stevef/gt",
		WorkDir:       "/home/stevef/gt/mechanic",
		DefaultBranch: "main",
		Polecat:       "",
		MayorSession:  "hq-mayor",
		DeaconSession: "hq-deacon",
	}

	baseOutput, _ := tmpl.RenderRole("base", data)
	mechanicOutput, _ := tmpl.RenderRole("mechanic", data)
	combined := baseOutput + "\n\n" + mechanicOutput

	systemPrompt := combined + "\n\nFormat: CMD: <command> or DONE: <summary>"

	// Simulate the hook work item - what the mechanic receives
	userPrompt := `Execute the following work and report results:

1. [HOOK] Check inbox for work assignments

You are patrol cycle #1 for this agent session.`

	fmt.Println("=== SYSTEM PROMPT (mechanic role) ===")
	fmt.Println(systemPrompt)
	fmt.Println()

	fmt.Println("=== USER PROMPT ===")
	fmt.Println(userPrompt)
	fmt.Println()

	// Get LLM config from environment
	endpoint := os.Getenv("LLM_ENDPOINT")
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1/chat/completions"
	}
	model := os.Getenv("LLM_MODEL")
	if model == "" {
		model = "gpt-4o"
	}

	// Make LLM call
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	client := llm.NewClient(endpoint, model, "", 60*time.Second)

	resp, err := client.Complete(ctx, systemPrompt, userPrompt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "LLM error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("=== LLM RESPONSE ===")
	fmt.Println(resp)
	fmt.Println()

	// Analyze response
	if strings.Contains(resp, "gt mail inbox") {
		fmt.Println("❌ PROBLEM: LLM still wants to run 'gt mail inbox'")
	} else if strings.Contains(resp, "ls -rt") || strings.Contains(resp, "log") {
		fmt.Println("✅ GOOD: LLM is scanning logs")
	} else {
		respLen := len(resp)
		if respLen > 100 {
			respLen = 100
		}
		fmt.Printf("⚠️  UNKNOWN: Response starts with: %s\n", resp[:respLen])
	}
}