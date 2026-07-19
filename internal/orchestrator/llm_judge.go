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

type JudgeResult struct {
	Pass    bool     `json:"pass"`
	Reason  string   `json:"reason"`
	Missing []string `json:"missing"`
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
		// If LLM is unavailable, treat as skipped (not an error)
		if strings.Contains(err.Error(), "connection refused") || 
			strings.Contains(err.Error(), "connection refused") ||
			strings.Contains(err.Error(), "no such host") {
			return true, "LLM judge unavailable (connection refused), skipping", nil
		}
		return false, "", fmt.Errorf("LLM call: %w", err)
	}

	// Parse response
	var result JudgeResult
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

// TriadValidationConfig holds the three documents for SPEC/Architecture/Plan validation
type TriadValidationConfig struct {
	SPEC         string
	Architecture string
	Plan         string
	MinLength    int
}

// ValidateTriadWithJudge validates SPEC/Architecture/Plan coherence using LLM judge
func ValidateTriadWithJudge(ctx context.Context, client *llm.Client, cfg TriadValidationConfig) (bool, string, error) {
	if len(strings.TrimSpace(cfg.SPEC)) < cfg.MinLength ||
		len(strings.TrimSpace(cfg.Architecture)) < cfg.MinLength ||
		len(strings.TrimSpace(cfg.Plan)) < cfg.MinLength {
		return false, "one or more documents too short", nil
	}

	client = getOrCreateJudgeClient(client)

	systemPrompt := `You are a strict technical architect reviewing document coherence.
Evaluate if the three documents (SPEC, Architecture, Plan) are semantically consistent.
Return ONLY a JSON object:
{
  "pass": true|false,
  "reason": "specific explanation of inconsistencies found",
  "missing": ["issue 1", "issue 2"]
}

Check for:
1. HTTP routes in SPEC exactly match routes in Architecture HTTP table
2. Store/API function names in SPEC match Architecture store API section
3. Plan bead map paths exactly match Architecture planned file layout
4. Plan acceptance bullets map to SPEC functional requirements
5. No paths in Plan that aren't in Architecture or SPEC
6. No Architecture paths missing from Plan (for active phase)
7. Module/package names consistent across all three`

	userPrompt := fmt.Sprintf(`SPEC:
%s

ARCHITECTURE:
%s

PLAN:
%s`, cfg.SPEC, cfg.Architecture, cfg.Plan)

	client = getOrCreateJudgeClient(nil)

	resp, err := client.Complete(ctx, systemPrompt, userPrompt)
	if err != nil {
		return false, "", fmt.Errorf("LLM call: %w", err)
	}

	var result JudgeResult
	if err := json.Unmarshal([]byte(resp), &result); err != nil {
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

// TestQualityConfig for test file validation
type TestQualityConfig struct {
	TestFileContent string
	SpecSection     string
	ArchSection     string
	FilePath        string
	MinLength       int
}

// ValidateTestQualityWithJudge evaluates test file quality against SPEC/Architecture
func ValidateTestQualityWithJudge(ctx context.Context, client *llm.Client, cfg TestQualityConfig) (bool, string, error) {
	if len(strings.TrimSpace(cfg.TestFileContent)) < cfg.MinLength {
		return false, fmt.Sprintf("test file too short (%d chars)", len(cfg.TestFileContent)), nil
	}

	client = getOrCreateJudgeClient(client)

	systemPrompt := `You are a strict test quality reviewer.
Evaluate if the test file substantively tests the functional requirements.
Return ONLY a JSON object:
{
  "pass": true|false,
  "reason": "specific explanation of quality issues",
  "missing": ["issue 1", "issue 2"]
}

Check for:
1. Test names map to SPEC functional requirements (FR IDs or behavior descriptions)
2. Tests cover happy path, error cases, and edge cases from SPEC/Architecture
3. No trivial tests (assert True, empty test bodies, import-only tests)
4. Tests use real assertions against expected behavior, not just imports
5. Test cases cover acceptance bullets from Architecture for this file
6. Mocks/stubs are appropriate, not over-mocked
7. Test data is realistic per Architecture data model`

	userPrompt := fmt.Sprintf(`SPEC Section:
%s

Architecture Section:
%s

Test File: %s
Content:
%s`, cfg.SpecSection, cfg.ArchSection, cfg.FilePath, cfg.TestFileContent)

	client = getOrCreateJudgeClient(nil)

	resp, err := client.Complete(ctx, systemPrompt, userPrompt)
	if err != nil {
		return false, "", fmt.Errorf("LLM call: %w", err)
	}

	var result JudgeResult
	if err := json.Unmarshal([]byte(resp), &result); err != nil {
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

// IntegrationContractConfig for integration contract validation
type IntegrationContractConfig struct {
	ArchSection   string
	SpecHTTPTable string
	PlanSection   string
	MinLength     int
}

// ValidateIntegrationContractWithJudge validates integration contract completeness
func ValidateIntegrationContractWithJudge(ctx context.Context, client *llm.Client, cfg IntegrationContractConfig) (bool, string, error) {
	if len(strings.TrimSpace(cfg.ArchSection)) < cfg.MinLength {
		return false, "architecture section too short", nil
	}

	client = getOrCreateJudgeClient(client)

	systemPrompt := `You are a strict integration contract reviewer.
Evaluate if the Architecture integration contract section is complete and matches SPEC.
Return ONLY a JSON object:
{
  "pass": true|false,
  "reason": "specific explanation of gaps",
  "missing": ["gap 1", "gap 2"]
}

Check for:
1. Server entrypoint wiring: how dependencies are instantiated and passed
2. Route registration: exact SPEC HTTP paths mapped to handlers
3. Exported symbols per file: what each file exports (types, constructors, methods)
4. Dependency wiring: single consistent story (instance + factories, package-level funcs, or same-package registerHandlers)
5. No invented routes/params not in SPEC HTTP table
6. All SPEC HTTP routes accounted for in architecture
7. Dependency injection / initialization order clear`

	userPrompt := fmt.Sprintf(`SPEC HTTP Table:
%s

Architecture Integration Section:
%s

Plan Integration Contract Section:
%s`, cfg.SpecHTTPTable, cfg.ArchSection, cfg.PlanSection)

	client = getOrCreateJudgeClient(nil)

	resp, err := client.Complete(ctx, systemPrompt, userPrompt)
	if err != nil {
		return false, "", fmt.Errorf("LLM call: %w", err)
	}

	var result JudgeResult
	if err := json.Unmarshal([]byte(resp), &result); err != nil {
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

func getOrCreateJudgeClient(client *llm.Client) *llm.Client {
	if client == nil {
		return llm.NewClient(
			"http://localhost:11434/v1/chat/completions",
			GetModel("judge"),
			"",
			60*time.Second,
		)
	}
	return client
}