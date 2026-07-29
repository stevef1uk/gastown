package specprofile

import (
	"bytes"
	"context"
	"encoding/json"
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

Return a JSON object mapping phase IDs to updated qa_verify_command strings. **Only include phases where the current command is wrong.** If a phase already has a valid, non-placeholder command that properly tests its required files, leave it out of your response entirely. Do NOT replace valid commands with equivalent alternatives (e.g. don't replace "uv run pytest" with "python -m pytest" — both are fine). Phases not in the response keep their current command.

Rules for writing verify commands:
- Shell scripts (.sh): "test -f scripts/start_mac.sh && test -f scripts/stop_mac.sh" — verify each script exists
- Docker/compose files: "test -f Dockerfile && test -f docker-compose.yml && echo 'docker ok'" — verify files exist
- Playwright E2E: "cd test && npm install && npx playwright test --list" — list tests without running them
- Python/pytest: "cd backend && python -m pytest -v tests/" — run actual tests
- Go: "cd . && go test ./..." — run actual tests
- TypeScript/React (frontend): "cd frontend && npm install && npx tsc --noEmit" — typecheck
- Frontend tests: "cd frontend && npm test -- --watchAll=false" — run frontend unit tests
- Database files: "test -f db/finally.db && echo 'db ok'" — verify DB file exists
- Backend source (no tests yet): "cd backend && python -c 'import sys; sys.path.insert(0, \"src\"); from main import app; print(\"ok\")'" — verify module can import
- Mixed content (e.g., scripts + code): combine with && from most specific to least

CRITICAL: All paths are relative to the rig root (mayor/rig/). Do NOT prefix with the rig name. Use actual file paths from required_files.

Example correct output:
{
  "infrastructure": "test -f scripts/start_mac.sh && test -f scripts/stop_mac.sh && test -f scripts/start_windows.ps1 && echo 'scripts ok'",
  "smoke-test": "test -f Dockerfile && test -f docker-compose.yml && echo 'docker ok'"
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

// JudgePhaseVerifyCommands reviews all delivery phase verify commands via LLM
// and replaces placeholders or mismatched commands. Returns the profile with
// updates applied, or the original if the LLM call fails.
func JudgePhaseVerifyCommands(ctx context.Context, endpoint, model string, v orchestrator.WorkflowValidation) orchestrator.WorkflowValidation {
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

	// Build phase payloads
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

	payloadJSON, err := json.MarshalIndent(phases, "", "  ")
	if err != nil {
		log.Printf("[judge] marshal phases: %v", err)
		return v
	}

	userPrompt := "Review these delivery phases and return updated qa_verify_command values:\n\n" + string(payloadJSON)

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

	var updates map[string]string
	if err := ExtractJSONObject(content, &updates); err != nil {
		log.Printf("[judge] parse JSON from response: %v", err)
		return v
	}

	log.Printf("[judge] LLM returned %d phase updates", len(updates))
	for id, cmd := range updates {
		log.Printf("[judge]   %q → %q", id, cmd)
	}

	// Apply updates — only for phases where the current command is actually
	// a placeholder or doesn't match the phase's file types. Good commands
	// (e.g. "cd backend && uv run pytest") are preserved even if the LLM
	// returned a different valid alternative.
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
		// Only apply if current command is a placeholder/mismatch.
		if !orchestrator.IsPlaceholderOrMismatchedCommand(p.QAVerifyCommand, p) {
			log.Printf("[judge]   %q: skipping (current command is valid): %q", p.ID, p.QAVerifyCommand)
			continue
		}
		log.Printf("[judge]   %q: %q → %q", p.ID, p.QAVerifyCommand, newCmd)
		p.QAVerifyCommand = newCmd
		applied++
	}

	log.Printf("[judge] applied %d updates", applied)
	return v
}
