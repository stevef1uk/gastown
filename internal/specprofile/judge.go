package specprofile

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/orchestrator"
)



// judgeSystemPrompt returns the system prompt for the QA phase reviewer.
func judgeSystemPrompt() string {
	return `You are a QA phase reviewer. Your job is to ensure each delivery phase in a build profile has:
1. A proper qa_verify_command that actually tests the files in that phase
2. Correct RequiredFiles -- each file assigned to exactly ONE phase that matches its purpose

For each phase you receive, evaluate BOTH the current qa_verify_command AND the RequiredFiles assignment.

If qa_verify_command is a placeholder (echo, trivial command) or doesn't properly test the phase's required files, replace it with a real command.

If RequiredFiles has issues, fix them:
- **No duplicates**: Each file must appear in exactly ONE phase's RequiredFiles
- **Right phase**: Test files belong in the phase whose Goal/Deliverables match (e.g., test_api.py in the phase that implements API routes, not in "project-foundation")
- **No build artifacts**: Generated files (frontend/out/*, dist/*, build/*, *.db, __pycache__) must NOT be in RequiredFiles
- **No null/empty**: Every phase must have concrete RequiredFiles to anchor verification
- **Phase alignment**: Files must match the phase's spec_focus and deliverables

Return a FLAT JSON object with two keys: "verify_commands" and "required_files". Each maps phase ID -> corrected value.
Only include phases where the current value is wrong. If a phase is correct, leave it out.
Do NOT repeat unchanged phase metadata (title, spec_focus).

For **early/mid phases** (backend, frontend, database), prefer behavioral checks over file-presence when spec sections describe specific functionality:
- If the spec mentions API endpoints, start the server and curl those endpoints with real payloads
- If the spec mentions database tables/schemas, verify the schema exists with a query
- If no spec sections are available, fall back to the generic file-presence or compile-check patterns below:
  - Shell scripts (.sh): "test -f scripts/start_mac.sh && test -f scripts/stop_mac.sh"
  - Docker/compose files: "test -f Dockerfile && test -f docker-compose.yml && echo 'docker ok'"
  - **Playwright E2E (integration-test phase with playwright.config.ts + e2e specs + docker-compose.yml): "cd <layout_root> && docker-compose down && docker-compose build --no-cache && docker-compose up --exit-code-from playwright && docker rmi test-app:latest 2>/dev/null || true && docker image prune -f"**
  - **Playwright config must disable Chromium HTTPS auto-upgrade** (causes ERR_SSL_PROTOCOL_ERROR on http://app:8000): add launch args --disable-features=HttpsUpgrades,HttpsFirstModeV2,AutomaticHttpsDefault,AutomaticHttpsDefaultEnabled,HttpsOnlyMode and use baseURL: "http://app:8000" without trailing slash. Prefer IP http://172.26.0.2:8000 if network is fixed.**
  - Playwright config: "cd test && npm install --ignore-scripts && npx playwright test"
  - **Python/pytest: ONLY if phase required_files includes test files (*_test.py, test_*.py, conftest.py, tests/): "cd backend && python -m pytest -v tests/"; otherwise import-check: "cd backend && python -c 'import main; print("ok")'"**
  - **Go: ONLY if phase required_files includes *_test.go: "cd . && go test ./..."; otherwise compile-check: "cd . && go build ./..."**
  - **TypeScript/React: "cd frontend && npm install --ignore-scripts && npx tsc --noEmit"**
  - **Frontend tests: ONLY if phase required_files includes *.spec.ts(x) or test/ dir: "cd frontend && npm test -- --watchAll=false"**
  - Database files: "test -f db/finally.db && echo 'db ok'"
  - Backend source (no tests yet): "cd backend && python -c 'import sys; sys.path.insert(0, "src"); from main import app; print("ok")'"

For **final/integration phases** (e.g. smoke-test, deployment-and-e2e, doc-seed), generate a comprehensive **start -> functional API & UI verify -> stop** smoke test. Infer the project's run pattern from required_files, spec_focus, and spec_excerpt.

**The verify command MUST validate application BEHAVIOR against specific endpoints and features described in spec_excerpt -- not just basic health or status 200.** Use spec_excerpt to determine:
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

Output JSON only -- no prose, no markdown fences.`
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
	for id, u := range updates {
		if u.VerifyCommand != "" {
			log.Printf("[judge]   %q verify_cmd → %q", id, u.VerifyCommand)
		}
		if len(u.RequiredFiles) > 0 {
			log.Printf("[judge]   %q required_files → %v", id, u.RequiredFiles)
		}
	}

	// Stage 2: validate proposed changes with a second LLM call.
	// Build updates maps for validator
	verifyUpdates := make(map[string]string)
	requiredFilesUpdates := make(map[string][]string)
	for id, u := range updates {
		if u.VerifyCommand != "" {
			verifyUpdates[id] = u.VerifyCommand
		}
		if len(u.RequiredFiles) > 0 {
			requiredFilesUpdates[id] = u.RequiredFiles
		}
	}
	approvedVerify, approvedFiles := validatePhaseUpdates(ctx, validatorEndpoint, validatorModel, phases, verifyUpdates, requiredFilesUpdates)

	// Apply validated updates — the validator already approved these, so
	// no heuristic guard is needed.
	applied := 0
	for i := range v.DeliveryPhases {
		p := &v.DeliveryPhases[i]
		u, ok := updates[p.ID]
		if !ok {
			continue
		}
		// Apply verify command if present and approved
		if u.VerifyCommand != "" {
			// Check if validator approved this verify command
			approvedCmd, approved := approvedVerify[p.ID]
			if !approved && u.VerifyCommand != p.QAVerifyCommand {
				log.Printf("[judge]   %q verify_cmd: REJECTED by validator", p.ID)
			} else {
				newCmd := strings.TrimSpace(approvedCmd)
				if newCmd == "" {
					newCmd = strings.TrimSpace(u.VerifyCommand)
				}
				if newCmd != "" && newCmd != p.QAVerifyCommand {
					log.Printf("[judge]   %q: %q → %q", p.ID, p.QAVerifyCommand, newCmd)
					p.QAVerifyCommand = newCmd
					applied++
				}
			}
		}
		// Apply required_files if present and approved
		if len(u.RequiredFiles) > 0 {
			_, filesApproved := approvedFiles[p.ID]
			if !filesApproved && !slices.Equal(p.RequiredFiles, u.RequiredFiles) {
				log.Printf("[judge]   %q required_files: REJECTED by validator", p.ID)
			} else {
				// Deduplicate and normalize
				normalized := normalizePathList(u.RequiredFiles)
				deduped := deduplicateRequiredFiles(normalized)
				// Only apply if different
				currentNorm := normalizePathList(p.RequiredFiles)
				currentDedup := deduplicateRequiredFiles(currentNorm)
				if !slices.Equal(currentDedup, deduped) {
					log.Printf("[judge]   %q required_files: %v → %v", p.ID, p.RequiredFiles, deduped)
					p.RequiredFiles = deduped
					applied++
				}
			}
		}
	}

	log.Printf("[judge] applied %d updates", applied)
	return v
}

// judgeValidatorSystemPrompt returns the system prompt for the second-stage validator LLM.
func judgeValidatorSystemPrompt() string {
	return `You are a QA phase validator. Your job is to review proposed changes to delivery phase verify commands AND required_files assignments and decide whether to approve or reject each one.

For each proposed change, evaluate:

VERIFY COMMAND:
1. Is the proposed command a genuine improvement over the current command?
2. Does it properly test the phase's required files?
3. Does it match the phase's spec_focus?
4. Is it realistic — will it actually work given the files in the phase?
5. For final integration / smoke test phases (e.g. deployment-smoke, smoke-test, doc-seed), reject weak file-presence checks (like "test -f file") and insist on functional endpoint/UI checks or container/server start-verify-stop sequences when proposed.

REQUIRED_FILES:
1. Does the proposed list remove duplicates (same file in multiple phases)?
2. Does each file belong to the phase whose Goal/Deliverables match its purpose?
3. Are there NO build artifacts (generated files, *.db, dist/*, build/*, __pycache__)?
4. Are there NO null/empty required_files?
5. Does the list match the phase's spec_focus?

Return a JSON object with two keys: "verify_commands" and "required_files". Each maps phase IDs to "approve" or "reject". Only include phases you want to approve or reject. Phases not included in your response will be treated as rejected.

Output JSON only — no prose, no markdown fences.`
}

// validatorResponse holds the validator's verdicts for both verify_commands and required_files
type validatorResponse struct {
	VerifyCommands map[string]string `json:"verify_commands"`
	RequiredFiles  map[string]string `json:"required_files"`
}

// validatePhaseUpdates sends proposed changes (verify commands and required_files)
// to a validator LLM and returns the approved verdicts for both.
// If the validator call fails or returns an unparseable response, no updates are
// applied (fail-closed) to prevent weak or hallucinated generator output.
func validatePhaseUpdates(ctx context.Context, endpoint, model string, phases []judgePhasePayload, verifyUpdates map[string]string, requiredFilesUpdates map[string][]string) (map[string]string, map[string]string) {
	endpoint = strings.TrimSpace(endpoint)
	model = strings.TrimSpace(model)
	if endpoint == "" || model == "" {
		log.Printf("[judge] validator: skipping — no endpoint/model — rejecting all updates")
		return nil, nil
	}

	// Build validation prompt listing each proposed change.
	var buf strings.Builder
	buf.WriteString("Review these proposed changes to delivery phases and decide whether to approve or reject each one:\n\n")
	for _, p := range phases {
		newCmd, hasCmd := verifyUpdates[p.ID]
		newFiles, hasFiles := requiredFilesUpdates[p.ID]
		if !hasCmd && !hasFiles {
			continue
		}
		buf.WriteString(fmt.Sprintf("Phase: %s (%s)\n", p.ID, p.Title))
		buf.WriteString(fmt.Sprintf("Current required files: [%s]\n", strings.Join(p.RequiredFiles, ", ")))
		buf.WriteString(fmt.Sprintf("Spec focus: %s\n", p.SpecFocus))
		if hasCmd {
			buf.WriteString(fmt.Sprintf("Current command: %s\n", p.CurrentCommand))
			buf.WriteString(fmt.Sprintf("Proposed command: %s\n", newCmd))
		}
		if hasFiles {
			buf.WriteString(fmt.Sprintf("Proposed required_files: [%s]\n", strings.Join(newFiles, ", ")))
		}
		buf.WriteString("\n")
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
		log.Printf("[judge] validator: marshal request: %v — rejecting all updates", err)
		return nil, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		log.Printf("[judge] validator: create request: %v — rejecting all updates", err)
		return nil, nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GasTown-Role", "judge-validate-commands")

	log.Printf("[judge] validator: sending %d proposed changes to %s %s", len(verifyUpdates)+len(requiredFilesUpdates), endpoint, model)
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[judge] validator: LLM request failed: %v — rejecting all updates", err)
		return nil, nil
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[judge] validator: read response: %v — rejecting all updates", err)
		return nil, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("[judge] validator: LLM HTTP %d: %s — rejecting all updates", resp.StatusCode, strings.TrimSpace(string(raw)))
		return nil, nil
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
		log.Printf("[judge] validator: decode response: %v — rejecting all updates", err)
		return nil, nil
	}
	if len(wrap.Choices) == 0 {
		log.Printf("[judge] validator: no choices in LLM response — rejecting all updates")
		return nil, nil
	}
	if wrap.Choices[0].FinishReason == "length" {
		log.Printf("[judge] validator: LLM response truncated (finish_reason=length) — rejecting all updates")
		return nil, nil
	}

	content := strings.TrimSpace(wrap.Choices[0].Message.Content)
	log.Printf("[judge] validator: response (%d chars): %.200s", len(content), content)

	var verdicts validatorResponse
	if err := ExtractJSONObject(content, &verdicts); err != nil {
		// Fallback: try parsing as nested objects with verify_commands/required_files keys
		var nested map[string]interface{}
		if err2 := ExtractJSONObject(content, &nested); err2 != nil {
			log.Printf("[judge] validator: parse JSON: %v — rejecting all updates", err)
			return nil, nil
		}
		verdicts = validatorResponse{
			VerifyCommands: make(map[string]string),
			RequiredFiles:  make(map[string]string),
		}
		for key, val := range nested {
			if key == "verify_commands" || key == "required_files" {
				if m, ok := val.(map[string]interface{}); ok {
					for id, v := range m {
						if s, ok := v.(string); ok {
							if key == "verify_commands" {
								verdicts.VerifyCommands[id] = s
							} else {
								verdicts.RequiredFiles[id] = s
							}
						}
					}
				}
			}
		}
		if len(verdicts.VerifyCommands) == 0 && len(verdicts.RequiredFiles) == 0 {
			log.Printf("[judge] validator: no verdicts found in nested response — rejecting all updates")
			return nil, nil
		}
		log.Printf("[judge] validator: extracted %d verify + %d files verdicts from nested response", len(verdicts.VerifyCommands), len(verdicts.RequiredFiles))
	}

	// Filter updates: only keep those approved.
	approvedVerify := make(map[string]string)
	approvedFiles := make(map[string]string)
	for id, cmd := range verifyUpdates {
		v, ok := verdicts.VerifyCommands[id]
		if !ok || strings.ToLower(strings.TrimSpace(v)) != "approve" {
			log.Printf("[judge] validator:   %q verify_cmd: REJECTED (verdict: %q)", id, v)
			continue
		}
		log.Printf("[judge] validator:   %q verify_cmd: APPROVED", id)
		approvedVerify[id] = cmd
	}
	for id, files := range requiredFilesUpdates {
		v, ok := verdicts.RequiredFiles[id]
		if !ok || strings.ToLower(strings.TrimSpace(v)) != "approve" {
			log.Printf("[judge] validator:   %q required_files: REJECTED (verdict: %q)", id, v)
			continue
		}
		log.Printf("[judge] validator:   %q required_files: APPROVED", id)
		approvedFiles[id] = strings.Join(files, ",")
	}
	log.Printf("[judge] validator: %d / %d verify_cmds approved, %d / %d required_files approved",
		len(approvedVerify), len(verifyUpdates), len(approvedFiles), len(requiredFilesUpdates))
	return approvedVerify, approvedFiles
}

// judgePhaseUpdate contains the proposed changes for a phase.
type judgePhaseUpdate struct {
	VerifyCommand  string
	RequiredFiles  []string
}

// parseJudgeResponse parses the generator LLM response into a flat phase_id→updates map.
// Expected format: {"verify_commands": {"phase_id": "cmd"}, "required_files": {"phase_id": ["file1", "file2"]}}
// Falls back to the old flat format for backward compatibility.
func parseJudgeResponse(content string) map[string]judgePhaseUpdate {
	// Try new format with verify_commands and required_files
	var structured struct {
		VerifyCommands map[string]string         `json:"verify_commands"`
		RequiredFiles  map[string][]string       `json:"required_files"`
	}
	if err := ExtractJSONObject(content, &structured); err == nil {
		out := make(map[string]judgePhaseUpdate)
		for id, cmd := range structured.VerifyCommands {
			if strings.TrimSpace(cmd) != "" {
				out[id] = judgePhaseUpdate{
					VerifyCommand: strings.TrimSpace(cmd),
				}
			}
		}
		for id, files := range structured.RequiredFiles {
			if u, ok := out[id]; ok {
				u.RequiredFiles = files
				out[id] = u
			} else {
				out[id] = judgePhaseUpdate{
					RequiredFiles: files,
				}
			}
		}
		if len(out) > 0 {
			log.Printf("[judge] extracted %d phase updates (structured)", len(out))
			return out
		}
	}

	// Fallback: old flat format (phase_id -> command string)
	var flat map[string]string
	if err := ExtractJSONObject(content, &flat); err == nil {
		out := make(map[string]judgePhaseUpdate)
		for id, cmd := range flat {
			if strings.TrimSpace(cmd) != "" {
				out[id] = judgePhaseUpdate{
					VerifyCommand: strings.TrimSpace(cmd),
				}
			}
		}
		if len(out) > 0 {
			log.Printf("[judge] extracted %d commands from flat response", len(out))
			return out
		}
	}

	// Fallback: parse as nested objects and extract qa_verify_command from each.
	var nested map[string]interface{}
	if err := ExtractJSONObject(content, &nested); err != nil {
		log.Printf("[judge] parse JSON from response: %v", err)
		return nil
	}
	out := make(map[string]judgePhaseUpdate)
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
		out[id] = judgePhaseUpdate{
			VerifyCommand: strings.TrimSpace(cmdStr),
		}
	}
	if len(out) > 0 {
		log.Printf("[judge] extracted %d commands from nested response", len(out))
		return out
	}
	log.Printf("[judge] no valid updates found in response")
	return nil
}

// normalizePathList normalizes and filters a list of file paths.
func normalizePathList(files []string) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// deduplicateRequiredFiles removes obviously incorrect nested paths when the
// correct parent path is already present. E.g., if both "X/package.json" and
// "X/src/package.json" are in the list, the src/ one is wrong.
func deduplicateRequiredFiles(files []string) []string {
	fileSet := make(map[string]bool, len(files))
	for _, f := range files {
		fileSet[f] = true
	}
	out := make([]string, 0, len(files))
	for _, f := range files {
		dir := filepath.Dir(f)
		base := filepath.Base(f)
		parts := strings.Split(dir, "/")
		skip := false
		if len(parts) >= 2 {
			parentDir := strings.Join(parts[:len(parts)-1], "/")
			parentPath := parentDir + "/" + base
			if fileSet[parentPath] && parentPath != f {
				skip = true
			}
		}
		if !skip {
			out = append(out, f)
		}
	}
	return out
}
