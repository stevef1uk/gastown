package specprofile

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/orchestrator"
)

const maxSpecChars = 60000

// LLMExtractProfile calls an OpenAI-compatible chat API to turn SPEC.md into WorkflowValidation fields.
// endpoint and model should come from ResolveLLMForSpecIndex(townRoot); empty strings use Freeride defaults.
func LLMExtractProfile(ctx context.Context, endpoint, model, specContent string) (orchestrator.WorkflowValidation, string, error) {
	endpoint = strings.TrimSpace(endpoint)
	model = strings.TrimSpace(model)
	if endpoint == "" {
		endpoint = config.DefaultFreerideProxyEndpoint
	}
	if model == "" {
		model = "ollama/llama3.3"
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

	client := &http.Client{Timeout: HTTPTimeoutForSpecIndex()}
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
  "python_venv_dir": string,
  "dev_server_port": number,
  "confidence": string
}

Rules:
- layout_root: top-level project directory name if the spec says code lives under a named folder relative to repo root; use "." if the primary code is at repo root. All QA verify commands (both root-level and per-phase) must cd to this same directory — never invent a different directory name. When layout_root is ".", verify commands MUST NOT cd to the rig name or any top-level project name from the spec — only cd to actual subdirectories like "frontend/" or "backend/".
- bead_title_contains: short prefix for implementation task beads (e.g. "Implement <layout_root>/"); if layout_root is "." use "Implement " (no ./ prefix); must be stable for grep on bd list.
- test_runner: one of "unittest", "pytest", "custom".
- unittest_module: dotted module for stdlib unittest ONLY if test_runner is unittest (e.g. backend.test_app); else "".
- qa_verify_command: default rig-wide verify when no per-phase command applies — must run the **full unit test suite** (e.g. go test ./..., or pytest -v tests/), not compile-only. Must be consistent with layout_root — never cd to a directory that doesn't match layout_root. When layout_root is "." do not add any cd prefix; when layout_root is "myapp", use "cd myapp && go test ./...".
- required_files: ALL file paths under mayor/rig (under layout_root) that appear in the SPEC directory tree, architecture.md backtick paths, or are otherwise required by the project. Include scripts, config, deployment files, docs, and startup files — not just source code and tests. Include **unit test files** alongside implementation: Go *_test.go in the same package as the code under test; Python tests/test_<module>.py (or equivalent) per package/API layer. Order in phases: module code before its tests before cmd/server/main.go. For large specs (15+ files), still list the full union here.
- delivery_phases: For large or multi-stack specs, split into 4–10 phases with at most 10 required_files each (backend layers, frontend, e2e). **Keep each source file's corresponding test file (test_*.py, *_test.go) in the same phase** so QA can verify each phase independently. Each phase needs id (kebab-case), title, required_files subset, and qa_verify_command that validates only that slice. Order phases by dependency (application source before packaging). Put Dockerfile, docker-compose.yml, docker-compose.test.yml, and .dockerignore in the **final** phase only — not setup-infrastructure or the first phase. **Frontend-only phases must typecheck, not run E2E tests**: use "cd frontend && npm install && npx tsc --noEmit" (or yarn/pnpm). Playwright/E2E tests that need a running server belong in the final e2e-and-deployment phase. Omit delivery_phases for tiny tutorials (≤12 files total).
- spec_summary: 400–2500 characters summarizing goals, stack, directory layout, functional requirements to cover in unit tests, and how to run the test suite—so downstream agents need not re-read the full spec.
- min_architecture_bytes: target 2500–5000 for most rigs; use 6000–8000 only when required_files has 15+ paths or the spec is very large. For ≤10 required_files use 2000–3500. Use 200–8192 only; NEVER copy SPEC byte length.
- min_plan_bytes: ignored at runtime — plan.md must be ≥ half of architecture.md bytes (set min_architecture_bytes only).
- python_venv_dir: Relative path for the Python virtual environment directory under mayor/rig. Use ".venv" for most projects. Set to "off" to skip venv creation.
- dev_server_port: The port the dev server listens on. Set 0 if the project is NOT a web server (CLI tool, library, background worker, etc.). If the project IS a web server (handles HTTP requests, serves a web UI/API), set the port number explicitly if the spec mentions one (e.g. 8080, 3000, 5000, 8000), otherwise default to 8080 for Go servers and 8000 for Python servers.
- confidence: "high", "medium", or "low".

CRITICAL: The agent's working directory when running QA commands is $GT_ROOT/<rig>/mayor/rig/. All qa_verify_command values (root and per-phase) must be relative to that directory. Examples:
- layout_root "." → commands run from mayor/rig/: "cd frontend && npm test" (NOT "cd finally/frontend")
- layout_root "myapp" → commands run from mayor/rig/: "cd myapp/frontend && npm test" (correct)

CRITICAL delivery_phases rules (the LLM must obey these):
- Every phase MUST have a qa_verify_command (non-empty string). Phases without verification will fail.
- depends_on MUST reference only phase IDs that exist in the same delivery_phases array. Use the exact "id" string from another phase object.
- Phase IDs must be unique, lowercase, kebab-case (e.g. "backend-core-db", "frontend-ui-1").
- Order phases by dependency: earlier phases listed first, later phases depend on earlier ones.
- Dockerfile, docker-compose.yml, docker-compose.test.yml, .dockerignore go in the FINAL phase only (typically "e2e-and-deployment" or similar).
- Frontend-only phases MUST use "cd frontend && npm install && npx tsc --noEmit" (typecheck only). When layout_root is not ".", use "cd <layout_root>/frontend && npm install && npx tsc --noEmit". Do NOT put Playwright/E2E tests in frontend phases.
- Playwright/E2E tests that need a running server belong in the FINAL e2e-and-deployment phase.
- For E2E/deployment phases that include docker-compose.test.yml or Playwright test files, use "docker compose -f test/docker-compose.test.yml up --build --abort-on-container-exit" as qa_verify_command (no cd prefix when layout_root is "."; when layout_root is not ".", prefix with "cd <layout_root> && "). Do NOT output "echo 'no verify command inferred'".
- Keep source files and their corresponding test files (*_test.go, test_*.py) in the SAME phase so QA can verify each phase independently.
- If the spec is small (≤12 total required_files), you MAY omit delivery_phases entirely (single-phase workflow).

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
	PythonVenvDir        string                      `json:"python_venv_dir"`
	DevServerPort        int                         `json:"dev_server_port"`
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
		PythonVenvDir:        strings.TrimSpace(payload.PythonVenvDir),
		DevServerPort:        payload.DevServerPort,
	}
	if v.LayoutRoot == "." {
		v.LayoutRoot = ""
	}

	// Validate and fix delivery_phases for internal consistency
	if len(v.DeliveryPhases) > 0 {
		v.DeliveryPhases = ValidateAndFixDeliveryPhases(v.DeliveryPhases, v.LayoutRoot)
	}

	v = orchestrator.ClampProfileValidation(orchestrator.NormalizeLayoutProfile(v))
	conf := strings.TrimSpace(payload.Confidence)
	if conf == "" {
		conf = "medium"
	}
	return v, conf, nil
}

// ValidateAndFixDeliveryPhases sanitizes LLM output for internal consistency.
// Ensures: unique kebab-case IDs, valid depends_on refs, every phase has qa_verify_command.
func ValidateAndFixDeliveryPhases(phases []orchestrator.DeliveryPhase, layoutRoot string) []orchestrator.DeliveryPhase {
	if len(phases) == 0 {
		return phases
	}

	// Build ID set and map
	idSet := make(map[string]bool, len(phases))
	idMap := make(map[string]*orchestrator.DeliveryPhase, len(phases))
	for i := range phases {
		id := strings.TrimSpace(phases[i].ID)
		if id == "" {
			// Generate from title
			id = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(phases[i].Title), " ", "-"))
			id = strings.Trim(id, "-")
		}
		// Normalize to kebab-case
		id = normalizePhaseID(id)
		// Ensure uniqueness
		base := id
		suffix := 2
		for idSet[id] {
			id = fmt.Sprintf("%s-%d", base, suffix)
			suffix++
		}
		idSet[id] = true
		phases[i].ID = id
		idMap[id] = &phases[i]
	}

	// Validate depends_on
	for i := range phases {
		p := &phases[i]
		var validDeps []string
		for _, dep := range p.DependsOn {
			dep = strings.TrimSpace(dep)
			if dep == "" {
				continue
			}
			if idSet[dep] {
				validDeps = append(validDeps, dep)
			}
		}
		p.DependsOn = validDeps

		// Ensure qa_verify_command exists and has a real command
		cmd := strings.TrimSpace(p.QAVerifyCommand)
		if cmd == "" || strings.Contains(cmd, "no verify command inferred") {
			p.QAVerifyCommand = defaultQAVerifyForPhase(p, layoutRoot)
		}
	}

	return phases
}

func normalizePhaseID(s string) string {
	s = strings.ToLower(s)
	// Replace spaces and underscores with hyphens
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")
	// Remove non-alphanumeric/hyphen
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	s = strings.Trim(b.String(), "-")
	// Collapse multiple hyphens
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	if s == "" {
		s = "phase"
	}
	return s
}

func defaultQAVerifyForPhase(p *orchestrator.DeliveryPhase, layoutRoot string) string {
	lr := layoutRoot
	if lr == "" {
		lr = "."
	}

	// Check for E2E/Docker phases first (more specific)
	hasDockerComposeTest := false
	hasPlaywright := false
	for _, f := range p.RequiredFiles {
		if strings.Contains(f, "docker-compose.test") {
			hasDockerComposeTest = true
		}
		if strings.Contains(f, "playwright") || strings.HasSuffix(f, ".spec.ts") {
			hasPlaywright = true
		}
	}

	if hasDockerComposeTest || hasPlaywright {
		return fmt.Sprintf("cd %s && docker compose -f test/docker-compose.test.yml up --build --abort-on-container-exit", lr)
	}

	hasGo := false
	hasPy := false
	hasTS := false
	hasJS := false
	hasTSConfig := false
	for _, f := range p.RequiredFiles {
		if strings.HasSuffix(f, "_test.go") || strings.HasSuffix(f, ".go") {
			hasGo = true
		}
		if strings.HasSuffix(f, ".py") || strings.HasPrefix(f, "tests/") {
			hasPy = true
		}
		if strings.HasSuffix(f, ".ts") || strings.HasSuffix(f, ".tsx") || strings.Contains(f, "frontend/") {
			hasTS = true
		}
		if strings.HasSuffix(f, ".js") || strings.HasSuffix(f, ".jsx") || strings.HasSuffix(f, ".mjs") || strings.HasSuffix(f, ".cjs") {
			hasJS = true
		}
		if strings.HasSuffix(f, "tsconfig.json") {
			hasTSConfig = true
		}
	}

	if hasGo {
		return fmt.Sprintf("cd %s && go test ./...", lr)
	}
	if hasPy {
		return fmt.Sprintf("cd %s && python -m pytest -v", lr)
	}
	if hasTS {
		return fmt.Sprintf("cd %s/frontend && npm install && npx tsc --noEmit", lr)
	}
	if hasJS && hasTSConfig {
		return fmt.Sprintf("cd %s && npm install && npx tsc --noEmit", lr)
	}
	if hasJS {
		return fmt.Sprintf("cd %s && npm install && npm test", lr)
	}
	// Fallback: use a harmless echo rather than another "no verify command inferred"
	// placeholder, which would trigger the replacement check in ValidateAndFixDeliveryPhases
	// and cause a recursive loop back to this function.
	return fmt.Sprintf("cd %s && echo 'verify ok (no automated tests for this phase)'", lr)
}
