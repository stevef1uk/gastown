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

// extractPhaseSpecs makes a single fast LLM call to extract relevant SPEC.md/REQUIREMENTS.md
// sections for each phase based on its spec_focus. Returns a map of phase_id→excerpt.
// On failure, returns nil (JUDGE will proceed without spec context).
func extractPhaseSpecs(ctx context.Context, endpoint, model string, phases []judgePhasePayload, specText, reqText string) map[string]string {
	if (specText == "" && reqText == "") || len(phases) == 0 {
		return nil
	}
	totalSpec := len(specText) + len(reqText)
	log.Printf("[judge] extracting spec sections for %d phases from %d total chars", len(phases), totalSpec)

	var b strings.Builder
	b.WriteString("For each phase below, extract the relevant sections from the project's SPEC.md and REQUIREMENTS.md that describe the behavior this phase should test.\n\n")
	b.WriteString("Phases:\n")
	for _, p := range phases {
		b.WriteString(fmt.Sprintf("- %s (spec_focus: %s)\n", p.ID, p.SpecFocus))
	}
	if reqText != "" {
		r := reqText
		if len(r) > 8000 {
			r = r[:8000] + "\n... (truncated)"
		}
		b.WriteString("\n### REQUIREMENTS.md\n")
		b.WriteString(r)
	}
	if specText != "" {
		s := specText
		if len(s) > 12000 {
			s = s[:12000] + "\n... (truncated)"
		}
		b.WriteString("\n### SPEC.md\n")
		b.WriteString(s)
	}
	b.WriteString("\n\nReturn a JSON object mapping each phase ID to a short excerpt (max 400 chars) from the spec/req that describes what that phase should verify. If no relevant section is found for a phase, omit it. Output JSON only.\n")

	body := map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": "You extract relevant sections from project specifications for specific delivery phases."},
			{"role": "user", "content": b.String()},
		},
		"stream": false,
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		log.Printf("[judge] extract: marshal: %v", err)
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		log.Printf("[judge] extract: create req: %v", err)
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GasTown-Role", "judge-extract-specs")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[judge] extract: LLM failed: %v", err)
		return nil
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("[judge] extract: HTTP %d: %v", resp.StatusCode, err)
		return nil
	}

	var wrap struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil || len(wrap.Choices) == 0 {
		log.Printf("[judge] extract: decode: %v", err)
		return nil
	}

	content := strings.TrimSpace(wrap.Choices[0].Message.Content)
	var result map[string]string
	if err := ExtractJSONObject(content, &result); err != nil {
		log.Printf("[judge] extract: parse JSON: %v", err)
		return nil
	}
	log.Printf("[judge] extract: got sections for %d phases", len(result))
	return result
}

// judgeSystemPrompt returns the system prompt for the QA verify command judge.
func judgeSystemPrompt() string {
	return `You are a QA verify command reviewer. Your job is to ensure each delivery phase in a build profile has a proper qa_verify_command that actually tests the files in that phase.

For each phase you receive, evaluate the current qa_verify_command. If it is a placeholder (echo, trivial command) or doesn't properly test the phase's required files, replace it with a real command.

Each phase above includes a relevant excerpt from the project's SPEC.md/REQUIREMENTS.md. **GENERATE VERIFY COMMANDS THAT TEST THE ACTUAL BEHAVIOR DESCRIBED IN THE EXCERPT** — not just file presence. For example, if the spec excerpt says "users can place limit orders", the verify command should start the server and test that the order endpoint actually processes limit orders.

Return a FLAT JSON object. Each key is a phase ID (string), each value is a qa_verify_command (string). **Only include phases where the current command is wrong.** Do NOT repeat the phase metadata (title, required_files, etc.). If a phase already has a valid, non-placeholder command that properly tests its required files, leave it out of your response entirely. Do NOT replace valid commands with equivalent alternatives (e.g. don't replace "uv run pytest" with "python -m pytest" — both are fine). Phases not in the response keep their current command.

For **early/mid phases** (backend, frontend, database), prefer behavioral checks over file-presence when spec sections describe specific functionality:
- If the spec mentions API endpoints, start the server and curl those endpoints with real payloads
- If the spec mentions database tables/schemas, verify the schema exists with a query
- If no spec sections are available, fall back to file presence or compile checks:
  - Shell scripts (.sh): "test -f scripts/start_mac.sh && test -f scripts/stop_mac.sh"
  - Docker/compose files: "test -f Dockerfile && test -f docker-compose.yml && echo 'docker ok'"
  - Playwright config: "cd test && npm install && npx playwright test --list"
  - Python/pytest: "cd backend && python -m pytest -v tests/"
  - Go: "cd . && go test ./..."
  - TypeScript/React: "cd frontend && npm install && npx tsc --noEmit"
  - Frontend tests: "cd frontend && npm test -- --watchAll=false"
  - Database files: "test -f db/finally.db && echo 'db ok'"
  - Backend source (no tests yet): "cd backend && python -c 'import sys; sys.path.insert(0, \"src\"); from main import app; print(\"ok\")'"

For **final/integration phases** (e.g. smoke-test, deployment-and-e2e), generate a **start -> verify -> stop** smoke test. Infer the project's run pattern from required_files, spec_focus, and spec_sections.

**The verify command must validate application BEHAVIOR against the spec — not just health.** Use spec_sections to determine:
1. What API endpoints exist and what payloads they accept
2. What UI features the frontend should display
3. What database tables and relationships exist
4. Expected error conditions and edge cases

Include content checks against multiple endpoints with real request payloads derived from spec sections:

- **API functional test**: start the server, POST to endpoints with spec-described payloads, assert response fields match spec
- **UI content**: check the root page HTML for spec-described UI elements: "curl -s http://localhost:8000/ | grep -qi 'expected-ui-text' && echo 'UI content found'" 
- **Data endpoints**: check they return meaningful content matching spec data models: "curl -s http://localhost:8000/api/endpoint | python3 -c 'import json,sys; d=json.load(sys.stdin); assert \"expectedField\" in d'"
- **Database schema**: verify tables/columns described in spec exist: "cd backend && python3 -c 'import sqlite3; conn=sqlite3.connect(\"finally.db\"); assert \"expected_table\" in [r[0] for r in conn.execute(\"SELECT name FROM sqlite_master WHERE type='table'\")]'"

**Tech-specific smoke patterns:**
- Docker (with timeout for first-run image pulls): "timeout 300 docker compose build && timeout 300 docker compose up -d && sleep 5 && curl -s http://localhost:8000/ | grep -qi 'expected-text' && curl -s http://localhost:8000/api/health | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d.get(\"status\") == \"ok\"' && curl -s http://localhost:8000/api/portfolio | python3 -c 'import json,sys; d=json.load(sys.stdin); assert len(d.get(\"positions\",[]))>0' && docker compose down"
- Docker (single-container, non-daemon — shows build errors inline): "timeout 300 docker compose up --build --abort-on-container-exit & sleep 5 && curl --retry 3 --retry-delay 2 -s http://localhost:8000/ | grep -qi 'expected-text' && curl -s http://localhost:8000/api/health | python3 -c '...' && kill %1 2>/dev/null; docker compose down"
- Script-based: "cd test && npm install && ../scripts/start_mac.sh & sleep 3 && curl --retry 3 --retry-delay 2 -s http://localhost:8000/ | grep -qi 'expected-text' && ../scripts/stop_mac.sh"
- Go server: "go run ./cmd/server/ & sleep 3 && curl --retry 3 --retry-delay 2 -s http://localhost:8000/ | grep -qi 'expected-text' && kill %1"
- **First-run setups**: if the project needs npm install, pip install, or docker pull, wrap the setup step with "timeout 300" to allow first-run downloads. Example: "timeout 300 docker compose build && docker compose up -d && ..."
- **Dependency consistency**: for npm projects, run "npm install" (not "npm ci") before any test/build to ensure lockfile matches package.json when dependencies have been added. Example: "cd frontend && npm install && npm run build"
- Always include at least 2-3 content-validating curl checks (root page, health, data endpoint) in addition to any Playwright tests. **Parse the JSON response, don't just check for HTTP 200.**
- Content validation pattern (parses API JSON, verifies structure): "curl -s http://localhost:8000/api/watchlist | python3 -c 'import json,sys; d=json.load(sys.stdin); items=d.get(\"items\",[]); assert len(items)>0; print(f\"{len(items)} items ok\")'"

Always include a health check AND content validation (grep HTML for expected strings, parse API JSON) AND functional tests (Playwright) before tearing down. If the phase has no obvious run mechanism, fall back to file-presence checks from the early/mid phase rules.

CRITICAL: All paths are relative to the rig root (mayor/rig/). Do NOT prefix with the rig name. Use actual file paths from required_files.

Example correct output:
{
  "infrastructure": "test -f scripts/start_mac.sh && test -f scripts/stop_mac.sh && test -f scripts/start_windows.ps1 && echo 'scripts ok'",
  "smoke-test": "docker compose build && docker compose up -d && sleep 3 && curl --retry 5 --retry-delay 2 http://localhost:8000/health && npx playwright test && docker compose down"
}

Output JSON only — no prose, no markdown fences.`
}

// judgePhaseVerifyCommandPayload is sent to the LLM for each profile.
type judgePhasePayload struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	RequiredFiles   []string `json:"required_files"`
	SpecFocus       string   `json:"spec_focus"`
	CurrentCommand  string   `json:"current_qa_verify_command"`
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

	// Build phase payloads.
	var phases []judgePhasePayload
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

	// Pre-extraction: use a single fast LLM call to extract relevant spec/req sections
	// for each phase, rather than dumping the full documents into every JUDGE call.
	phaseSpecMap := extractPhaseSpecs(ctx, endpoint, model, phases, specText, reqText)

	payloadJSON, err := json.MarshalIndent(phases, "", "  ")
	if err != nil {
		log.Printf("[judge] marshal phases: %v", err)
		return v
	}

	// Build user prompt: phase payloads plus extracted spec excerpts.
	var userPromptBuilder strings.Builder
	userPromptBuilder.WriteString("Review these delivery phases and return updated qa_verify_command values:\n\n")
	userPromptBuilder.WriteString(string(payloadJSON))
	if len(phaseSpecMap) > 0 {
		userPromptBuilder.WriteString("\n\n---\nRelevant SPEC.md / REQUIREMENTS.md excerpts per phase:\n")
		for _, p := range phases {
			if excerpt, ok := phaseSpecMap[p.ID]; ok && excerpt != "" {
				userPromptBuilder.WriteString(fmt.Sprintf("\n### %s (%s)\n%s\n", p.ID, p.SpecFocus, excerpt))
			}
		}
		userPromptBuilder.WriteString("---\n")
	}
	userPrompt := userPromptBuilder.String()

	body := map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": judgeSystemPrompt()},
			{"role": "user", "content": userPrompt},
		},
		"stream": false,
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
		"stream": false,
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
