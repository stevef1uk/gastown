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

// SpecStackKind represents the primary technology stack.
type SpecStackKind string

const (
	StackGo      SpecStackKind = "go"
	StackPython  SpecStackKind = "python"
	StackNodeJS  SpecStackKind = "nodejs"
	StackDocker  SpecStackKind = "docker"
	StackGeneric SpecStackKind = "generic"
)

// detectStackFromSpec infers the primary stack from SPEC.md content.
func detectStackFromSpec(spec string) SpecStackKind {
	lower := strings.ToLower(spec)
	hasGo := strings.Contains(lower, "go.mod") || strings.Contains(lower, "go test") ||
		strings.Contains(lower, "go build") || strings.Contains(lower, "go run") ||
		strings.Contains(lower, "golang") || strings.Contains(lower, "main.go") ||
		strings.Contains(lower, "_test.go") || strings.Contains(lower, "gin") ||
		strings.Contains(lower, "chi") || strings.Contains(lower, "echo")
	hasPython := strings.Contains(lower, "requirements.txt") || strings.Contains(lower, "pyproject.toml") ||
		strings.Contains(lower, "uv.lock") || strings.Contains(lower, "pytest") ||
		strings.Contains(lower, "uvicorn") || strings.Contains(lower, "fastapi") ||
		strings.Contains(lower, "django") || strings.Contains(lower, "flask") ||
		strings.Contains(lower, "main.py") || strings.Contains(lower, "test_*.py")
	hasNode := strings.Contains(lower, "package.json") || strings.Contains(lower, "pnpm-lock.yaml") ||
		strings.Contains(lower, "yarn.lock") || strings.Contains(lower, "npm install") ||
		strings.Contains(lower, "npm ci") || strings.Contains(lower, "node.js") ||
		strings.Contains(lower, "typescript") || strings.Contains(lower, "react") ||
		strings.Contains(lower, "next.js") || strings.Contains(lower, "vite")
	hasDocker := strings.Contains(lower, "dockerfile") || strings.Contains(lower, "docker-compose") ||
		strings.Contains(lower, "container")

	if hasGo && !hasPython && !hasNode {
		return StackGo
	}
	if hasPython && !hasGo && !hasNode {
		return StackPython
	}
	if hasNode && !hasGo && !hasPython {
		return StackNodeJS
	}
	if hasDocker {
		return StackDocker
	}
	if hasGo {
		return StackGo
	}
	if hasPython {
		return StackPython
	}
	if hasNode {
		return StackNodeJS
	}
	return StackGeneric
}

// stackDeliveryPhaseGuidance returns stack-specific delivery phase guidance for the spec-index prompt.
func stackDeliveryPhaseGuidance(stack SpecStackKind) string {
	switch stack {
	case StackGo:
		return `
  * Go phases: "go test ./..." or "go build ./..." from layout_root. Go mod phase: "go mod tidy && go build ./...".`
	case StackPython:
		return `
  * Python phases: "cd backend && python -m pytest -v tests/" (from layout_root/backend).`
	case StackNodeJS:
		return `
  * Frontend-only phases (TypeScript/React): typecheck, not run E2E tests: "cd frontend && npm install --ignore-scripts && npx tsc --noEmit" (or yarn/pnpm; prefer "npm ci --ignore-scripts" when lockfile present). Always add --ignore-scripts to every npm/pnpm/yarn install command — lifecycle hooks are a known supply-chain attack vector (Shai-Hulud), so the orchestrator and exec layer will reject/harden unhardened installs.`
	case StackDocker:
		return `
  * Docker phases: Use "docker build" and "docker-compose" commands as appropriate for the stack.`
	default:
		return `
  * Frontend-only phases (TypeScript/React): typecheck, not run E2E tests: "cd frontend && npm install --ignore-scripts && npx tsc --noEmit" (or yarn/pnpm; prefer "npm ci --ignore-scripts" when lockfile present). Always add --ignore-scripts to every npm/pnpm/yarn install command — lifecycle hooks are a known supply-chain attack vector (Shai-Hulud), so the orchestrator and exec layer will reject/harden unhardened installs.
  * Go phases: "go test ./..." or "go build ./..." from layout_root. Go mod phase: "go mod tidy && go build ./...".
  * Python phases: "cd backend && python -m pytest -v tests/" (from layout_root/backend).`
	}
}

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

	stack := detectStackFromSpec(specContent)
	system := specIndexSystemPrompt()
	// Inject stack-specific delivery phase guidance after the base delivery_phases rule
	system = strings.Replace(system,
		"- delivery_phases: For large or multi-stack specs, split into 4-10 phases with at most 10 required_files each (backend layers, frontend, e2e). Keep each source file's corresponding test file (*_test.go, test_*.py) in the same phase so QA can verify each phase independently. Each phase needs id (kebab-case), title, required_files subset, and qa_verify_command that validates only that slice. Order phases by dependency (application source before packaging). Put Dockerfile, docker-compose.yml, docker-compose.test.yml, and .dockerignore in the **final** phase only — not setup-infrastructure or the first phase. Frontend-only phases must typecheck, not run E2E tests: use \"cd frontend && npm install --ignore-scripts && npx tsc --noEmit\" (or yarn/pnpm, and prefer \"npm ci --ignore-scripts\" when a lockfile is present). Always add --ignore-scripts to every npm/pnpm/yarn install command — lifecycle hooks are a known supply-chain attack vector (Shai-Hulud), so the orchestrator and exec layer will reject/harden unhardened installs. Playwright/E2E tests that need a running server belong in the final e2e-and-deployment phase. Docker compose E2E verify commands MUST rebuild the image from scratch before starting containers: \"docker-compose build --no-cache\" before \"docker-compose up --exit-code-from playwright\", and end with \"docker image prune -f\" to clean dangling layers. A plain \"compose up\" reuses whatever image is already tagged (often a stale build from an earlier phase), so QA would test old code.",
		"- delivery_phases: For large or multi-stack specs, split into 4-10 phases with at most 10 required_files each (backend layers, frontend, e2e). Keep each source file's corresponding test file (*_test.go, test_*.py) in the same phase so QA can verify each phase independently. Each phase needs id (kebab-case), title, required_files subset, and qa_verify_command that validates only that slice. Order phases by dependency (application source before packaging). Put Dockerfile, docker-compose.yml, docker-compose.test.yml, and .dockerignore in the **final** phase only — not setup-infrastructure or the first phase."+stackDeliveryPhaseGuidance(stack)+`
  * Playwright/E2E tests that need a running server belong in the final e2e-and-deployment phase. Docker compose E2E verify commands MUST rebuild the image from scratch before starting containers: "docker-compose build --no-cache" before "docker-compose up --exit-code-from playwright", then "docker rmi test-app:latest 2>/dev/null || true && docker image prune -f" to clean dangling layers. A plain "compose up" reuses whatever image is already tagged (often a stale build from an earlier phase), so QA would test old code.`, -1)

	user := "SPECIFICATION:\n\n" + specContent

	body := map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"max_tokens": 8192,
		"stream":     false,
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
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		return orchestrator.WorkflowValidation{}, "", fmt.Errorf("decode completions: %w", err)
	}
	if len(wrap.Choices) == 0 {
		return orchestrator.WorkflowValidation{}, "", fmt.Errorf("no choices in llm response")
	}

	// Reject truncated responses — an incomplete JSON profile is worse than no profile.
	if wrap.Choices[0].FinishReason == "length" {
		return orchestrator.WorkflowValidation{}, "", fmt.Errorf("llm response truncated (finish_reason=length): %s", strings.TrimSpace(string(raw)))
	}

	content := strings.TrimSpace(wrap.Choices[0].Message.Content)
	v, conf, err := parseSpecIndexPayload(content)
	if err != nil {
		return orchestrator.WorkflowValidation{}, "", err
	}
	return v, conf, nil
}

// chatCompletionJSON performs a single OpenAI-compatible chat call that must
// return a JSON object. It is the shared low-level plumbing used by the
// deterministic-index phase assignment step. Returns the raw content string.
func chatCompletionJSON(ctx context.Context, endpoint, model, system, user string) (string, error) {
	endpoint = strings.TrimSpace(endpoint)
	model = strings.TrimSpace(model)
	if endpoint == "" {
		endpoint = config.DefaultFreerideProxyEndpoint
	}
	if model == "" {
		model = "ollama/llama3.3"
	}
	body := map[string]interface{}{
		"model": model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"max_tokens": 8192,
		"stream":     false,
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(jsonBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GasTown-Role", "spec-index")

	client := &http.Client{Timeout: HTTPTimeoutForSpecIndex()}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm request: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("llm http %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
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
		return "", fmt.Errorf("decode completions: %w", err)
	}
	if len(wrap.Choices) == 0 {
		return "", fmt.Errorf("no choices in llm response")
	}
	if wrap.Choices[0].FinishReason == "length" {
		return "", fmt.Errorf("llm response truncated (finish_reason=length)")
	}
	return strings.TrimSpace(wrap.Choices[0].Message.Content), nil
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

- bead_title_contains: short prefix for implementation task beads (e.g. "Implement <layout_root>/"); if layout_root is "." use "Implement " (no ./ prefix); must be stable for grep on bd list. Examples:
  * layout_root "helloapi" → "Implement helloapi/"
  * layout_root "." → "Implement "
  * Never include quotes, never end with slash

- test_runner: one of "unittest", "pytest", "custom".
- unittest_module: dotted module for stdlib unittest ONLY if test_runner is unittest (e.g. backend.test_app); else "".
- qa_verify_command: default rig-wide verify when no per-phase command applies — must run the **full unit test suite** (e.g. go test ./..., or pytest -v tests/), not compile-only. Must be consistent with layout_root — never cd to a directory that doesn't match layout_root. When layout_root is "." do not add any cd prefix; when layout_root is "myapp", use "cd myapp && go test ./...".

- required_files: ALL file paths under mayor/rig (under layout_root) that appear in the SPEC directory tree, architecture.md backtick paths, or are otherwise required by the project. **EXCLUDE standard library imports** for any language (Go: encoding/json, net/http, os/signal, testing, os, io, log, context, time, strings, strconv, fmt, errors, sync, net, net/http/httptest; Python: os, sys, json, json.encoder, json.decoder, http, signal, unittest, pytest, typing, dataclasses, datetime, collections, itertools, functools, pathlib; Node: fs, path, http, crypto, util, events, stream, url, querystring, assert, fs/promises) — these are NOT files to create. EXCLUDE function calls, method calls, or code snippets (e.g. encoding/json.NewEncoder, json.NewDecoder, http.ListenAndServe, json.Marshal, json.dumps, json.loads, http.listen, express(), app.get) — these are code, NOT files. A robust heuristic: EXCLUDE any line that has a pattern like "func name(", "type name", or looks like a Go type declaration, Go function call with parenthesis, Python function call. Also exclude any items that:
  * Start and end with backticks like "like this"
  * Contain patterns like encoding/json.NewEncoder(...) (parenthesis inside)
  * Contain patterns like encoding/json.NewEncoder\. (escaped dot)
  * Are Go type placeholders like encoding/json.Decoder, decoder
  * Contain backtick-wrapped patterns with parentheses or dots (function calls)

  NOT excluded: actual file paths like "helloapi/handler.go", "helloapi/main.go", "helloapi/.gitignore"
  Include unit test files alongside implementation: Go *_test.go files in the same package as the code under test; Python tests/test_<module>.py per package/API layer.
  Order: module code before its tests before cmd/server/main.go.

- delivery_phases: For large or multi-stack specs, split into 4-10 phases with at most 10 required_files each (backend layers, frontend, e2e). Keep each source file's corresponding test file (*_test.go, test_*.py) in the same phase so QA can verify each phase independently. Each phase needs id (kebab-case), title, required_files subset, and qa_verify_command that validates only that slice. Order phases by dependency (application source before packaging). Put Dockerfile, docker-compose.yml, docker-compose.test.yml, and .dockerignore in the **final** phase only — not setup-infrastructure or the first phase. Frontend-only phases must typecheck, not run E2E tests: use "cd frontend && npm install --ignore-scripts && npx tsc --noEmit" (or yarn/pnpm, and prefer "npm ci --ignore-scripts" when a lockfile is present). Always add --ignore-scripts to every npm/pnpm/yarn install command — lifecycle hooks are a known supply-chain attack vector (Shai-Hulud), so the orchestrator and exec layer will reject/harden unhardened installs. Playwright/E2E tests that need a running server belong in the final e2e-and-deployment phase. Docker compose E2E verify commands MUST rebuild the image from scratch before starting containers: "docker-compose build --no-cache" before "docker-compose up --exit-code-from playwright", then "docker rmi test-app:latest 2>/dev/null || true && docker image prune -f" to clean dangling layers. A plain "compose up" reuses whatever image is already tagged (often a stale build from an earlier phase), so QA would test old code.

- spec_summary: 400–2500 characters summarizing goals, stack, directory layout, functional requirements to cover in unit tests, and how to run the test suite.
- min_architecture_bytes: target 2500–5000 for most rigs; use 6000–8000 only when required_files has 15+ paths. For ≤10 required_files use 2000–3500. Use 200–8192 only; NEVER copy SPEC byte length.
- min_plan_bytes: ignored at runtime.
- python_venv_dir: Relative path for the Python virtual environment directory under mayor/rig. Use ".venv\" for most projects. Set to \"off\" to skip venv creation.
- dev_server_port: The port the dev server listens on. Set 0 if the project is NOT a web server. If the project IS a web server, set the port number explicitly if the spec mentions one, otherwise default to 8080 for Go servers and 8000 for Python servers.
- confidence: \"high\", \"medium\", or \"low\".

CRITICAL: The agent's working directory when running QA commands is $GT_ROOT/<rig>/mayor/rig/ (the "rig root"). The layout_root is a SUBDIRECTORY of the rig root. All qa_verify_command values (root and per-phase) must be paths relative to the rig root.

EXAMPLES (assuming layout_root = "myapp"):
- From rig root, cd into project: "cd myapp && python -m pytest"
- Frontend subdirectory: "cd myapp/frontend && npm install && npx tsc --noEmit"  (NOT "cd myapp && cd myapp/frontend")
- Backend subdirectory: "cd myapp/backend && python -m pytest tests/"
- If layout_root = "." (project at rig root): "python -m pytest"  (no cd prefix)

NEVER use the rig name as a path prefix. NEVER do double cd into the same directory.

**CRITICAL: When combining multiple verification steps in a SINGLE shell command (chained with &&), each cd is relative to the PREVIOUS directory, not the rig root.**

CORRECT patterns for multi-step verification:
- Subshells (each independent from rig root): "(cd myapp && python -m pytest) && (cd myapp/frontend && npm test)"
- Relative chaining: "cd myapp && python -m pytest && cd frontend && npm test"  (second cd is relative to myapp/)
- Separate commands (preferred): "cd myapp && python -m pytest ; cd myapp/frontend && npm test"

WRONG: "cd myapp && python -m pytest && cd myapp/frontend && npm test"  (second cd tries myapp/myapp/frontend)

CRITICAL delivery_phases rules (the LLM must obey these):
- Every phase MUST have a qa_verify_command (non-empty string).
- depends_on MUST reference only phase IDs that exist in the same delivery_phases array.
- Phase IDs must be unique, lowercase, kebab-case (e.g. \"backend-core-db\", \"frontend-ui-1\").
- Order phases by dependency.
- Dockerfile, docker-compose.yml, docker-compose.test.yml, .dockerignore go in the FINAL phase only.
- Keep source files and their corresponding test files in the SAME phase.
- If the SPEC declares its own delivery phases (e.g. a "## Delivery Phases" section), mirror them exactly — id, title, order, and per-phase file groupings — even for small specs. NEVER collapse an explicitly declared phase list to a single phase; a small project with declared phases keeps all of them.
- Only omit delivery_phases entirely (single-phase workflow) when the SPEC is small (≤12 total required_files) AND declares NO phases of its own. In that case, **UNLESS the SPEC describes manual integration verification** (e.g., "run server then curl endpoint", "start server and test with curl", "verify with curl"), in which case add only a final smoke test phase as described below.
- **Add a final smoke test phase** when the SPEC describes manual integration verification (e.g., "run server then curl endpoint" or "start server with go run/python/uvicorn then curl"). This phase should:
  - Have id like "smoke-test" or "integration-test"
  - Depend on the server-wiring/backend phase
  - Have required_files including the main entry point (e.g., "helloapi/main.go", "backend/main.py", "server.js")
	- Have qa_verify_command that: starts the server in background, sleeps briefly, curls the endpoint, kills by port. Language-specific examples:
	    - Go: "cd helloapi && go run . & sleep 2 && curl -sf http://localhost:8080/hello | grep 'Hello, World!' && kill \$(lsof -ti:8080)"
	    - Python: "cd backend && python -m uvicorn main:app --port 8000 & sleep 2 && curl -sf http://localhost:8000/hello && kill \$(lsof -ti:8000)"
	    - Node: "cd server && node index.js & sleep 2 && curl -sf http://localhost:3000/hello && kill \$(lsof -ti:3000)"

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
		return orchestrator.HardenNodeInstallCommand(fmt.Sprintf("cd %s/frontend && npm install && npx tsc --noEmit", lr))
	}
	if hasJS && hasTSConfig {
		return orchestrator.HardenNodeInstallCommand(fmt.Sprintf("cd %s && npm install && npx tsc --noEmit", lr))
	}
	if hasJS {
		return orchestrator.HardenNodeInstallCommand(fmt.Sprintf("cd %s && npm install && npm test", lr))
	}
	// Fallback: use a harmless echo rather than another "no verify command inferred"
	// placeholder, which would trigger the replacement check in ValidateAndFixDeliveryPhases
	// and cause a recursive loop back to this function.
	return fmt.Sprintf("cd %s && echo 'verify ok (no automated tests for this phase)'", lr)
}
