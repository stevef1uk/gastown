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

// extractJSONFromResponse extracts JSON from a response that may contain markdown code fences.
func stripThinkTags(s string) string {
	// Some models (deepseek, qwen) emit <think>...</think> blocks before JSON.
	// Strip the outermost pair and everything between them.
	for {
		start := strings.Index(s, "<think>")
		if start < 0 {
			return s
		}
		end := strings.Index(s[start:], "</think>")
		if end < 0 {
			// Unclosed think tag — strip from </think> to end
			return strings.TrimSpace(s[:start])
		}
		s = strings.TrimSpace(s[:start] + s[start+end+len("</think>"):])
	}
}

func extractJSONFromResponse(resp string) string {
	// Remove markdown code fences
	resp = strings.TrimSpace(resp)

	// Strip <think>...</think> reasoning blocks that some models emit before JSON
	resp = stripThinkTags(resp)

	// Handle ```json ... ``` fences
	if strings.HasPrefix(resp, "```") {
		lines := strings.Split(resp, "\n")
		// Find first line with ```
		startIdx := -1
		for i, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				startIdx = i
				break
			}
		}
		// Find closing ```
		endIdx := -1
		for i := startIdx + 1; i < len(lines); i++ {
			if strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
				endIdx = i
				break
			}
		}
		if startIdx >= 0 && endIdx > startIdx {
			extracted := strings.Join(lines[startIdx+1:endIdx], "\n")
			return strings.TrimSpace(extracted)
		}
	}
	
	// Fallback: find first { and last }
	start := strings.Index(resp, "{")
	end := strings.LastIndex(resp, "}")
	if start >= 0 && end > start {
		extracted := resp[start:end+1]
		// Strip trailing backticks and whitespace that may remain from code fences
		extracted = strings.TrimSpace(extracted)
		extracted = strings.TrimRight(extracted, "` \t\n\r")
		return extracted
	}
	
	return ""
}

func ValidateDocumentWithJudge(ctx context.Context, client *llm.Client, cfg JudgeConfig) (bool, string, error) {

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
		// Try to extract JSON from response (handle markdown code fences)
		jsonStr := extractJSONFromResponse(resp)
		if jsonStr == "" {
			return false, "", fmt.Errorf("parse judge response: could not extract JSON\nraw: %s", resp)
		}
		if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
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

// TestAdequacyConfig for whole-phase test adequacy review.
type TestAdequacyConfig struct {
	Requirements string   // REQUIREMENTS.md content (may be empty)
	SPEC         string   // SPEC.md content
	Architecture string   // architecture.md content
	TestPlan     string   // TEST_PLAN.md content
	TestFiles    []string // list of test file paths present on disk
	MinLength    int      // minimum TEST_PLAN.md length
}

// ValidateTestAdequacyWithJudge evaluates whether TEST_PLAN.md covers every
// active-phase requirement at an adequate level (unit/integration/ui) and whether
// the planned test files actually exist. It is a soft judge: connection failures
// skip (return pass), like the other judge validators.
func ValidateTestAdequacyWithJudge(ctx context.Context, client *llm.Client, cfg TestAdequacyConfig) (bool, string, error) {
	if len(strings.TrimSpace(cfg.TestPlan)) < cfg.MinLength {
		return false, fmt.Sprintf("TEST_PLAN.md too short (%d chars, need %d)", len(cfg.TestPlan), cfg.MinLength), nil
	}

	client = getOrCreateJudgeClient(client)

	systemPrompt := `You are a strict test adequacy reviewer for a software delivery phase.
The TEST_PLAN.md must map every functional requirement to a unit, integration, or UI test.
Return ONLY a JSON object:
{
  "pass": true|false,
  "reason": "specific explanation of adequacy issues",
  "missing": ["requirement id", "issue 2"]
}

Check for:
1. Every requirement ID in REQUIREMENTS.md/SPEC.md appears as a "### <req-id>" block in TEST_PLAN.md
2. Each block declares Level (unit|integration|ui), Test file, Bead ID, Scenarios, and Assertions
3. Levels are appropriate: unit for pure logic, integration for HTTP/store wiring, ui only for user-visible flows when the phase ships UI
4. The planned Test file path for every row exists in the provided Test Files list
5. No requirement is waved off with "ensure quality" or "covered by review" instead of a concrete test
6. Test files named are consistent with the active phase's file layout (no tests for files outside the phase)`

	fileList := "none provided"
	if len(cfg.TestFiles) > 0 {
		fileList = strings.Join(cfg.TestFiles, "\n")
	}

	userPrompt := fmt.Sprintf(`REQUIREMENTS:
%s

SPEC:
%s

ARCHITECTURE:
%s

TEST PLAN:
%s

TEST FILES ON DISK:
%s`, cfg.Requirements, cfg.SPEC, cfg.Architecture, cfg.TestPlan, fileList)

	resp, err := client.Complete(ctx, systemPrompt, userPrompt)
	if err != nil {
		if strings.Contains(err.Error(), "connection refused") || strings.Contains(err.Error(), "no such host") {
			return true, "LLM judge unavailable (connection refused), skipping", nil
		}
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