package specprofile

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

const maxSpecChars = 60000

// LLMExtractProfile calls an OpenAI-compatible chat API to turn SPEC.md into WorkflowValidation fields.
func LLMExtractProfile(ctx context.Context, specContent string) (orchestrator.WorkflowValidation, string, error) {
	endpoint := strings.TrimSpace(os.Getenv("LLM_ENDPOINT"))
	if endpoint == "" {
		endpoint = "http://localhost:11434/v1/chat/completions"
	}
	model := strings.TrimSpace(os.Getenv("LLM_MODEL"))
	if model == "" {
		model = "llama3"
	}

	if len(specContent) > maxSpecChars {
		specContent = specContent[:maxSpecChars] + "\n\n[truncated for indexing]\n"
	}

	system := `You are a build-system assistant. Given a project SPECIFICATION (markdown), emit a single JSON object only—no prose, no markdown fences.

The JSON must match this shape (use sensible defaults for small tutorials; for large specs, capture the primary deliverable layout):
{
  "layout_root": string,
  "bead_title_contains": string,
  "unittest_module": string,
  "qa_verify_command": string,
  "required_files": string[],
  "test_runner": string,
  "spec_summary": string,
  "min_architecture_bytes": number,
  "min_plan_bytes": number,
  "confidence": string
}

Rules:
- layout_root: top-level project directory name if the spec says code lives under a named folder relative to repo root; use "." if the primary code is at repo root.
- bead_title_contains: short prefix for implementation task beads (e.g. "Implement <layout_root>/"); must be stable for grep on bd list.
- test_runner: one of "unittest", "pytest", "custom".
- unittest_module: dotted module for stdlib unittest ONLY if test_runner is unittest (e.g. backend.test_app); else "".
- qa_verify_command: ONE shell command or chain run from the rig worktree (mayor/rig cwd is the repo root). It must verify the app. Prefer "python3 -m pytest -q" or "python3 -m unittest …" (not bare "pytest"). Use paths consistent with layout_root and required_files.
- required_files: 3–8 critical paths relative to mayor/rig (under layout_root when set). Include entrypoints and requirements.txt/pyproject.toml when the spec names them.
- spec_summary: 400–2500 characters summarizing goals, stack, directory layout, and how to run tests—so downstream agents need not re-read the full spec.
- min_plan_bytes: target 1500–2500 (enough for bead list + phased strategy in plan.md). Use 200–4096 only; NEVER copy SPEC byte length or spec_summary character count.
- min_architecture_bytes: target 4000–8192 for a substantive architecture.md. Use 200–8192 only; NEVER copy SPEC byte length.
- confidence: "high", "medium", or "low".

Output JSON only.`

	user := "SPECIFICATION:\n\n" + specContent

	body := map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"stream": false,
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return orchestrator.WorkflowValidation{}, "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return orchestrator.WorkflowValidation{}, "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GasTown-Role", "spec-index")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return orchestrator.WorkflowValidation{}, "", fmt.Errorf("llm request: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return orchestrator.WorkflowValidation{}, "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return orchestrator.WorkflowValidation{}, "", fmt.Errorf("llm http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var wrap struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return orchestrator.WorkflowValidation{}, "", fmt.Errorf("decode completions: %w", err)
	}
	if len(wrap.Choices) == 0 {
		return orchestrator.WorkflowValidation{}, "", fmt.Errorf("no choices in llm response")
	}

	content := strings.TrimSpace(wrap.Choices[0].Message.Content)
	var payload struct {
		LayoutRoot           string   `json:"layout_root"`
		BeadTitleContains    string   `json:"bead_title_contains"`
		UnittestModule       string   `json:"unittest_module"`
		QAVerifyCommand      string   `json:"qa_verify_command"`
		RequiredFiles        []string `json:"required_files"`
		TestRunner           string   `json:"test_runner"`
		SpecSummary          string   `json:"spec_summary"`
		MinArchitectureBytes int64    `json:"min_architecture_bytes"`
		MinPlanBytes         int64    `json:"min_plan_bytes"`
		Confidence           string   `json:"confidence"`
	}
	if err := ExtractJSONObject(content, &payload); err != nil {
		return orchestrator.WorkflowValidation{}, "", fmt.Errorf("parse llm json: %w (raw: %.200s)", err, content)
	}

	v := orchestrator.WorkflowValidation{
		LayoutRoot:           strings.TrimSpace(payload.LayoutRoot),
		BeadTitleContains:    strings.TrimSpace(payload.BeadTitleContains),
		UnittestModule:       strings.TrimSpace(payload.UnittestModule),
		QAVerifyCommand:      strings.TrimSpace(payload.QAVerifyCommand),
		RequiredFiles:        payload.RequiredFiles,
		TestRunner:           strings.TrimSpace(payload.TestRunner),
		SpecSummary:          strings.TrimSpace(payload.SpecSummary),
		MinArchitectureBytes: payload.MinArchitectureBytes,
		MinPlanBytes:         payload.MinPlanBytes,
	}
	if v.LayoutRoot == "." {
		v.LayoutRoot = ""
	}
	v = orchestrator.ClampProfileValidation(v)
	conf := strings.TrimSpace(payload.Confidence)
	if conf == "" {
		conf = "medium"
	}
	return v, conf, nil
}
