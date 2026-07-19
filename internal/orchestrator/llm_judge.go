package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/llm"
)

type JudgeConfig struct {
	DocumentName string
	Content      string
	Criteria     []string
	MinLength    int
}

func ValidateDocumentWithJudge(ctx context.Context, client *llm.Client, cfg JudgeConfig) (bool, string, error) {
	if len(strings.TrimSpace(cfg.Content)) < cfg.MinLength {
		return false, fmt.Sprintf("document too short (%d chars, need %d)", len(cfg.Content), cfg.MinLength), nil
	}

	// Build system prompt
	criteriaJSON, _ := json.Marshal(cfg.Criteria)
	systemPrompt := fmt.Sprintf(`You are a strict technical document reviewer.
Evaluate if the document satisfies ALL criteria. Return ONLY a JSON object:
{
  "pass": true|false,
  "reason": "specific explanation of which criteria passed/failed",
  "missing": ["criterion 1", "criterion 2"]
}

CRITERIA:
%s`, string(criteriaJSON))

	userPrompt := fmt.Sprintf("Document: %s\n\nContent:\n%s", cfg.DocumentName, cfg.Content)

	// Use default client if none provided
	if client == nil {
		client = llm.NewClient(
			"http://localhost:11434/v1/chat/completions",
			GetModel("judge"),
			"",
			60*time.Second,
		)
	}

	resp, err := client.Complete(ctx, systemPrompt, userPrompt)
	if err != nil {
		return false, "", fmt.Errorf("LLM call: %w", err)
	}

	// Parse response
	var result struct {
		Pass    bool     `json:"pass"`
		Reason  string   `json:"reason"`
		Missing []string `json:"missing"`
	}
	if err := json.Unmarshal([]byte(resp), &result); err != nil {
		// Try to extract JSON from response
		start := strings.Index(resp, "{")
		end := strings.LastIndex(resp, "}")
		if start >= 0 && end > start {
			if err := json.Unmarshal([]byte(resp[start:end+1]), &result); err != nil {
				return false, "", fmt.Errorf("parse judge response: %w\nraw: %s", err, resp)
			}
		} else {
			return false, "", fmt.Errorf("parse judge response: %w\nraw: %s", err, resp)
		}
	}

	if !result.Pass {
		reason := result.Reason
		if len(result.Missing) > 0 {
			reason += " Missing: " + strings.Join(result.Missing, ", ")
		}
		return false, reason, nil
	}
	return true, result.Reason, nil
}