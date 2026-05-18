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

	system := specIndexSystemPrompt()

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
	v, conf, err := parseSpecIndexPayload(content)
	if err != nil {
		return orchestrator.WorkflowValidation{}, "", err
	}
	return v, conf, nil
}

func specIndexSystemPrompt() string {
	return `You are a build-system assistant. Given a project SPECIFICATION (markdown), emit a single JSON object only—no prose, no markdown fences.

The JSON must match this shape (use sensible defaults for small tutorials; for large specs, capture the primary deliverable layout):
{
  "layout_root": string,
  "bead_title_contains": string,
  "unittest_module": string,
  "qa_verify_command": string,
  "required_files": string[],
  "delivery_phases": [{ "id": string, "title": string, "required_files": string[], "qa_verify_command": string, "depends_on": string[], "spec_focus": string }],
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
- qa_verify_command: default rig-wide verify when no per-phase command applies.
- required_files: Union of ALL critical paths relative to mayor/rig (under layout_root). For large specs (15+ files), still list the full union here.
- delivery_phases: For large or multi-stack specs, split into 4–10 phases with 5–15 required_files each (infra, backend layers, frontend, e2e). Each phase needs id (kebab-case), title, required_files subset, and qa_verify_command that validates only that slice. Order phases by dependency. Omit delivery_phases for tiny tutorials (≤12 files total).
- spec_summary: 400–2500 characters summarizing goals, stack, directory layout, and how to run tests—so downstream agents need not re-read the full spec.
- min_plan_bytes: target 3000–5000 (enough for a detailed bead list + phased strategy in plan.md). Use 200–8192 only; NEVER copy SPEC byte length or spec_summary character count.
- min_architecture_bytes: target 2500–5000 for most rigs; use 6000–8000 only when required_files has 15+ paths or the spec is very large. For ≤10 required_files use 2000–3500. Use 200–8192 only; NEVER copy SPEC byte length.
- confidence: "high", "medium", or "low".

Output JSON only.`
}

type specIndexPayload struct {
	LayoutRoot           string                      `json:"layout_root"`
	BeadTitleContains    string                      `json:"bead_title_contains"`
	UnittestModule       string                      `json:"unittest_module"`
	QAVerifyCommand      string                      `json:"qa_verify_command"`
	RequiredFiles        []string                    `json:"required_files"`
	DeliveryPhases       []orchestrator.DeliveryPhase `json:"delivery_phases"`
	TestRunner           string                      `json:"test_runner"`
	SpecSummary          string                      `json:"spec_summary"`
	MinArchitectureBytes int64                       `json:"min_architecture_bytes"`
	MinPlanBytes         int64                       `json:"min_plan_bytes"`
	Confidence           string                      `json:"confidence"`
}

func parseSpecIndexPayload(content string) (orchestrator.WorkflowValidation, string, error) {
	var payload specIndexPayload
	if err := ExtractJSONObject(content, &payload); err != nil {
		return orchestrator.WorkflowValidation{}, "", fmt.Errorf("parse llm json: %w (raw: %.200s)", err, content)
	}
	v := orchestrator.WorkflowValidation{
		LayoutRoot:           strings.TrimSpace(payload.LayoutRoot),
		BeadTitleContains:    strings.TrimSpace(payload.BeadTitleContains),
		UnittestModule:       strings.TrimSpace(payload.UnittestModule),
		QAVerifyCommand:      strings.TrimSpace(payload.QAVerifyCommand),
		RequiredFiles:        payload.RequiredFiles,
		DeliveryPhases:       payload.DeliveryPhases,
		TestRunner:           strings.TrimSpace(payload.TestRunner),
		SpecSummary:          strings.TrimSpace(payload.SpecSummary),
		MinArchitectureBytes: payload.MinArchitectureBytes,
		MinPlanBytes:         payload.MinPlanBytes,
	}
	if v.LayoutRoot == "." {
		v.LayoutRoot = ""
	}
	v = orchestrator.ClampProfileValidation(orchestrator.NormalizeLayoutProfile(v))
	conf := strings.TrimSpace(payload.Confidence)
	if conf == "" {
		conf = "medium"
	}
	return v, conf, nil
}
