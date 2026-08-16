package specprofile

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/orchestrator"
)



// judgeSystemPrompt returns the system prompt for the QA verify command judge.
func judgeSystemPrompt() string {
	return `You are a QA verify command reviewer. Your job is to ensure each delivery phase in a build profile has a proper qa_verify_command that actually tests the files in that phase.

For each phase you receive, evaluate the current qa_verify_command. If it is a placeholder (echo, trivial command) or doesn't properly test the phase's required files, replace it with a real command.

Each phase's "spec_excerpt" field contains a relevant excerpt from the project's SPEC.md/REQUIREMENTS.md. **GENERATE VERIFY COMMANDS THAT TEST THE ACTUAL BEHAVIOR DESCRIBED IN THE spec_excerpt — not just file presence.** For example, if the spec_excerpt says "users can place limit orders", the verify command should start the server and test that the order endpoint actually processes limit orders. If spec_excerpt is empty for a phase, fall back to the generic file-presence or compile-check patterns below.

Return a FLAT JSON object. Each key is a phase ID (string), each value is a qa_verify_command (string). **Only include phases where the current command is wrong.** Do NOT include phases where the current command is wrong. Do NOT repeat the phase metadata (title, required_files, etc.). If a phase already has a valid, non-placeholder command that properly tests its required files, leave it out of your response entirely. Do NOT replace valid commands with equivalent alternatives (e.g. don't replace "uv run pytest" with "python -m pytest" — both are fine). Phases not in the response keep their current command.

For **early/mid phases** (backend, frontend, database), prefer behavioral checks over file-presence when spec sections describe specific functionality:
- If the spec mentions API endpoints, start the server and curl those endpoints with real payloads
- If the spec mentions database tables/schemas, verify the schema exists with a query
- If no spec sections are available, fall back to file presence or compile checks:
  - Shell scripts (.sh): "test -f scripts/start_mac.sh && test -f scripts/stop_mac.sh"
  - Docker/compose files: "test -f Dockerfile && test -f docker-compose.yml && echo 'docker ok'"
  - **Playwright E2E (integration-test phase with playwright.config.ts + e2e specs + docker-compose.yml): "cd <layout_root> && docker-compose down && docker-compose build --no-cache && docker-compose up --exit-code-from playwright && docker image prune -f"**
  - Playwright config: "cd test && npm install --ignore-scripts && npx playwright test --list"
  - **Python/pytest: ONLY if phase required_files includes test files (*_test.py, test_*.py, conftest.py, tests/): "cd backend && python -m pytest -v tests/"; otherwise import-check: "cd backend && python -c 'import main; print(\"ok\")'"**
  - **Go: ONLY if phase required_files includes *_test.go: "cd . && go test ./..."; otherwise compile-check: "cd . && go build ./..."**
  - **TypeScript/React: "cd frontend && npm install --ignore-scripts && npx tsc --noEmit"**
  - **Frontend tests: ONLY if phase required_files includes *.spec.ts(x) or test/ dir: "cd frontend && npm test -- --watchAll=false"**
  - Database files: "test -f db/finally.db && echo 'db ok'"
  - Backend source (no tests yet): "cd backend && python -c 'import sys; sys.path.insert(0, \"src\"); from main import app; print(\"ok\")'"

For **final/integration phases** (e.g. smoke-test, deployment-and-e2e, doc-seed), generate a comprehensive **start -> functional API & UI verify -> stop** smoke test. Infer the project's run pattern from required_files, spec_focus, and spec_excerpt.

**The verify command MUST validate application BEHAVIOR against specific endpoints and features described in spec_excerpt — not just basic health or status 200.** Use spec_excerpt to determine:
1. Specific API endpoints (e.g. /api/v1/auth/login, /api/workspaces, /api/pages) and payloads to test via curl/python.
2. Specific UI title or text elements expected on the main page.
3. Database seeding or data queries to assert.

**Required final smoke test structure:**
1. Start container/server (e.g. "docker compose up -d" or "go run . &").
2. Wait/retry for readiness.
3. Perform **at least 2-3 specific functional endpoint tests with payload/JSON assertions** (e.g. test health endpoint, POST to auth/create resource endpoint, GET list endpoint).
4. Assert root UI HTML content contains expected spec strings.
5. Gracefully tear down / kill server process.

**CRITICAL: The working directory is the rig root ($GT_ROOT/<rig>/mayor/rig/). layout_root is a SUBDIRECTORY of the rig root. All paths in qa_verify_command must be relative to the rig root.**

EXAMPLES (layout_root = "myapp"):
- "cd myapp && python -m pytest"
- "cd myapp/frontend && npm install --ignore-scripts && npx tsc --noEmit"  (NOT "cd myapp && cd myapp/frontend")
- If layout_root = ".": "python -m pytest" (no cd)

NEVER use the rig name as a path prefix. NEVER double-cd into the same directory.

**CRITICAL: When combining multiple verification steps in a SINGLE shell command (chained with &&), each cd is relative to the PREVIOUS directory, not the rig root.**

CORRECT patterns for multi-step verification:
- Subshells (each independent from rig root): "(cd myapp && python -m pytest) && (cd myapp/frontend && npm test)"
- Relative chaining: "cd myapp && python -m pytest && cd frontend && npm test"  (second cd is relative to myapp/)
- Separate commands (preferred): "cd myapp && python -m pytest ; cd myapp/frontend && npm test"

WRONG: "cd myapp && python -m pytest && cd myapp/frontend && npm test"  (second cd tries myapp/myapp/frontend)

Output JSON only — no prose, no markdown fences.`
}

// judgePhaseVerifyCommandPayload is sent to the LLM for each profile.
type judgePhasePayload struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	RequiredFiles   []string `json:"required_files"`
	SpecFocus       string   `json:"spec_focus"`
	CurrentCommand  string   `json:"current_qa_verify_command"`
	SpecExcerpt     string   `json:"spec_excerpt,omitempty"`
}

// JudgePhaseVerifyCommands reviews all delivery phase verify commands in a two-stage
// LLM pipeline: the generator LLM (endpoint/model) suggests improved commands for
// placeholder/mismatched phases, then the validator LLM (validatorEndpoint/validatorModel)
// reviews each suggestion and only approved changes are applied.
//
// specText and reqText contain the project's SPEC.md and REQUIREMENTS.md content.
// They are matched to each phase via spec_focus and injected into the prompt so the
// LLM can generate behavioral verify commands that validate actual project requirements.
//
// If validatorEndpoint is empty or equals the generator, the generator model is reused
// for both stages (but still makes two separate calls).
func JudgePhaseVerifyCommands(ctx context.Context, endpoint, model, validatorEndpoint, validatorModel string, v orchestrator.WorkflowValidation, specText, reqText string) orchestrator.WorkflowValidation {
	if !v.HasPhasedDelivery() {
		log.Printf("[judge] skipping — no delivery phases")
		return v
	}
	endpoint = strings.TrimSpace(endpoint)
	model = strings.TrimSpace(model)
	if endpoint == "" {
		endpoint = "http://localhost:11434/v1/chat/completions"
	}
	if model == "" {
		model = "ollama/llama3.3"
	}

	log.Printf("[judge] reviewing %d phase verify commands via %s %s", len(v.DeliveryPhases), endpoint, model)

	// Build phase payloads (without spec excerpts yet — those come from extraction below).
	phases := make([]judgePhasePayload, 0, len(v.DeliveryPhases))
	for _, p := range v.DeliveryPhases {
		phases = append(phases, judgePhasePayload{
			ID:             p.ID,
			Title:          p.Title,
			RequiredFiles:  p.RequiredFiles,
			SpecFocus:      p.SpecFocus,
			CurrentCommand: p.QAVerifyCommand,
		})
		log.Printf("[judge] phase %q current command: %s", p.ID, p.QAVerifyCommand)
	}

	// Pre-extraction: use local RAG (keyword-scored chunk matching) to extract relevant
	// spec/req sections for each phase, rather than dumping the full documents into
	// every JUDGE call or relying on LLM extraction.
	phaseSpecMap := ExtractSpecExcerpts(phases, specText, reqText)

	// Embed excerpts inline in the phase payload JSON so the LLM sees the connection.
	for i, p := range phases {
		if phaseSpecMap != nil {
			phases[i].SpecExcerpt = phaseSpecMap[p.ID]
		}
	}

	payloadJSON, err := json.MarshalIndent(phases, "", "  ")
	if err != nil {
		log.Printf("[judge] marshal phases: %v", err)
		return v
	}

	userPrompt := "Review these delivery phases and return updated qa_verify_command values. Each phase includes a \"spec_excerpt\" field with relevant project specification text — use it to generate behavioral tests:\n\n" + string(payloadJSON)

	body := map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": judgeSystemPrompt()},
			{"role": "user", "content": userPrompt},
		},
		"max_tokens": 4096,
		"stream":     false,
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		log.Printf("[judge] marshal request: %v", err)
		return v
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		log.Printf("[judge] create request: %v", err)
		return v
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GasTown-Role", "judge-verify-commands")

	log.Printf("[judge] sending LLM request...")
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[judge] LLM request failed: %v", err)
		return v
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[judge] read response: %v", err)
		return v
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("[judge] LLM HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
		return v
	}

	var wrap struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		log.Printf("[judge] decode response: %v", err)
		return v
	}
	if len(wrap.Choices) == 0 {
		log.Printf("[judge] no choices in LLM response")
		return v
	}
	if wrap.Choices[0].FinishReason == "length" {
		log.Printf("[judge] LLM response truncated (finish_reason=length), skipping update")
		return v
	}

	content := strings.TrimSpace(wrap.Choices[0].Message.Content)
	log.Printf("[judge] LLM response (%d chars): %.200s", len(content), content)

	updates := parseJudgeResponse(content)
	if updates == nil {
		return v
	}

	log.Printf("[judge] LLM returned %d phase updates", len(updates))
	for id, cmd := range updates {
		log.Printf("[judge]   %q → %q", id, cmd)
	}

	// Stage 2: validate proposed changes with a second LLM call.
	if len(updates) > 0 {
		updates = validatePhaseUpdates(ctx, validatorEndpoint, validatorModel, phases, updates)
	}

	// Apply validated updates — the validator already approved these, so
	// no heuristic guard is needed.
	applied := 0
	for i := range v.DeliveryPhases {
		p := &v.DeliveryPhases[i]
		newCmd, ok := updates[p.ID]
		if !ok {
			continue
		}
		newCmd = strings.TrimSpace(newCmd)
		if newCmd == "" || newCmd == p.QAVerifyCommand {
			log.Printf("[judge]   %q: skipping (unchanged)", p.ID)
			continue
		}
		log.Printf("[judge]   %q: %q → %q", p.ID, p.QAVerifyCommand, newCmd)
		p.QAVerifyCommand = newCmd
		applied++
	}

	log.Printf("[judge] applied %d updates", applied)
	return v
}

// judgeValidatorSystemPrompt returns the system prompt for the second-stage validator LLM.
func judgeValidatorSystemPrompt() string {
	return `You are a QA verify command validator. Your job is to review proposed changes to delivery phase verify commands and decide whether to approve or reject each one.

For each proposed change, evaluate:
1. Is the proposed command a genuine improvement over the current command?
2. Does it properly test the phase's required files?
3. Does it match the phase's spec_focus?
4. Is it realistic — will it actually work given the files in the phase?
5. For final integration / smoke test phases (e.g. deployment-smoke, smoke-test, doc-seed), reject weak file-presence checks (like "test -f file") and insist on functional endpoint/UI checks or container/server start-verify-stop sequences when proposed.

Return a JSON object mapping phase IDs to "approve" or "reject". Only include phases you want to approve or reject. Phases not included in your response will be treated as rejected.

Output JSON only — no prose, no markdown fences.`
}

// validatePhaseUpdates sends proposed verify command changes to a validator LLM
// and returns only the approved updates. If the validator call fails, the original
// updates are returned as-is (fail-open).
func validatePhaseUpdates(ctx context.Context, endpoint, model string, phases []judgePhasePayload, updates map[string]string) map[string]string {
	endpoint = strings.TrimSpace(endpoint)
	model = strings.TrimSpace(model)
	if endpoint == "" || model == "" {
		log.Printf("[judge] validator: skipping — no endpoint/model")
		return updates
	}

	// Build validation prompt listing each proposed change.
	var buf strings.Builder
	buf.WriteString("Review these proposed verify command changes and decide whether to approve or reject each one:\n\n")
	for _, p := range phases {
		newCmd, ok := updates[p.ID]
		if !ok {
			continue
		}
		buf.WriteString(fmt.Sprintf("Phase: %s (%s)\n", p.ID, p.Title))
		buf.WriteString(fmt.Sprintf("Required files: [%s]\n", strings.Join(p.RequiredFiles, ", ")))
		buf.WriteString(fmt.Sprintf("Spec focus: %s\n", p.SpecFocus))
		buf.WriteString(fmt.Sprintf("Current command: %s\n", p.CurrentCommand))
		buf.WriteString(fmt.Sprintf("Proposed command: %s\n\n", newCmd))
	}
	userPrompt := buf.String()

	body := map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": judgeValidatorSystemPrompt()},
			{"role": "user", "content": userPrompt},
		},
		"max_tokens": 4096,
		"stream":     false,
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		log.Printf("[judge] validator: marshal request: %v", err)
		return updates
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		log.Printf("[judge] validator: create request: %v", err)
		return updates
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GasTown-Role", "judge-validate-commands")

	log.Printf("[judge] validator: sending %d proposed changes to %s %s", len(updates), endpoint, model)
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[judge] validator: LLM request failed: %v", err)
		return updates
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[judge] validator: read response: %v", err)
		return updates
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("[judge] validator: LLM HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
		return updates
	}

	var wrap struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		log.Printf("[judge] validator: decode response: %v", err)
		return updates
	}
	if len(wrap.Choices) == 0 {
		log.Printf("[judge] validator: no choices in LLM response")
		return updates
	}
	if wrap.Choices[0].FinishReason == "length" {
		log.Printf("[judge] validator: LLM response truncated (finish_reason=length), keeping original updates")
		return updates
	}

	content := strings.TrimSpace(wrap.Choices[0].Message.Content)
	log.Printf("[judge] validator: response (%d chars): %.200s", len(content), content)

	var verdicts map[string]string
	if err := ExtractJSONObject(content, &verdicts); err != nil {
		// Fallback: try parsing as nested objects, extract "approve"/"reject" from each.
		var nested map[string]interface{}
		if err2 := ExtractJSONObject(content, &nested); err2 != nil {
			log.Printf("[judge] validator: parse JSON: %v — using all updates", err)
			return updates
		}
		verdicts = make(map[string]string)
		for id, val := range nested {
			switch v := val.(type) {
			case string:
				verdicts[id] = v
			case map[string]interface{}:
				if vv, ok := v["verdict"]; ok {
					if s, ok := vv.(string); ok {
						verdicts[id] = s
					}
				}
			}
		}
		if len(verdicts) == 0 {
			log.Printf("[judge] validator: no verdicts found in nested response — using all updates")
			return updates
		}
		log.Printf("[judge] validator: extracted %d verdicts from nested response", len(verdicts))
	}

	// Filter updates: only keep those approved.
	approved := make(map[string]string)
	for id, cmd := range updates {
		v, ok := verdicts[id]
		if !ok || strings.ToLower(strings.TrimSpace(v)) != "approve" {
			log.Printf("[judge] validator:   %q: REJECTED (verdict: %q)", id, v)
			continue
		}
		log.Printf("[judge] validator:   %q: APPROVED", id)
		approved[id] = cmd
	}
	log.Printf("[judge] validator: %d / %d approved", len(approved), len(updates))
	return approved
}

// parseJudgeResponse parses the generator LLM response into a flat phase_id→command map.
// It first tries the standard flat format, then falls back to extracting qa_verify_command
// from nested object responses (a common LLM mistake).
func parseJudgeResponse(content string) map[string]string {
	var flat map[string]string
	if err := ExtractJSONObject(content, &flat); err == nil {
		return flat
	}

	// Fallback: parse as nested objects and extract qa_verify_command from each.
	var nested map[string]interface{}
	if err := ExtractJSONObject(content, &nested); err != nil {
		log.Printf("[judge] parse JSON from response: %v", err)
		return nil
	}
	out := make(map[string]string)
	for id, val := range nested {
		obj, ok := val.(map[string]interface{})
		if !ok {
			continue
		}
		cmd, ok := obj["qa_verify_command"]
		if !ok {
			cmd, ok = obj["cmd"]
		}
		if !ok {
			continue
		}
		cmdStr, ok := cmd.(string)
		if !ok || strings.TrimSpace(cmdStr) == "" {
			continue
		}
		out[id] = strings.TrimSpace(cmdStr)
	}
	if len(out) > 0 {
		log.Printf("[judge] extracted %d commands from nested response", len(out))
		return out
	}
	log.Printf("[judge] no valid commands found in response")
	return nil
}
