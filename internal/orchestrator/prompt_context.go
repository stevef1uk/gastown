// Hook registries for rig-flow (prompt_context and pre_run).
//
// # MAINTAINER / AGENTS — config first, not Go
//
// Do NOT add behavior with `if task.State == "implementation"` (or any state name) in cmd/gt-agent.
// Do NOT shorten prompts, change bead queues, or tweak failure text in Go when YAML can do it.
//
// Configure per state in templates/rig-flow.yaml hooks:
//
//   - prompt_file + instructions     — system/task text (prompts/rig-flow/*.md)
//   - prompt_context                 — injected user blocks (e.g. implementation_queue)
//   - omit_orchestrator_context        — skip the generic "## Orchestrator context" appendix
//   - system_prompt_footer             — short per-state CMD/JSON reminder
//   - user_prompt_wrapper: none      — drop "Complete this step only:" prefix
//   - failure_prompt_context           — which prompt_context keys to repeat on validation/empty reply (not the full list)
//   - empty_response_suffix            — one line after empty LLM reply
//   - append_go_compile_context        — inject .go file snippets after failed go build/tidy (polecat repair)
//   - failure_hint, retry_hint_key, pre_run, on_timeout, state_timeout_seconds, post_artifact_success, cmd_guard, artifacts, …
//
// Add a new case to PromptContextBlock or RunPreRunHook only for a reusable named hook; reference it from YAML.
// Profile vars: {rig}/mayor/rig/.gastown/workflow-profile.json. See town/README.md § "FSM behavior belongs in YAML".
package orchestrator

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	rigpkg "github.com/steveyegge/gastown/internal/rig"
)

// PromptContextBlock returns injected user-prompt text for a rig-flow prompt_context hook name.
func PromptContextBlock(key, townRoot, rig string, v WorkflowValidation) string {
	switch key {
	case "planning_bead_bootstrap":
		return FormatPlanningBeadBootstrapBlock(townRoot, rig, v.ForActivePhase())
	case "implementation_queue":
		return FormatImplementationQueueBlock(townRoot, rig, v.ForActivePhase())
	case "implement_bead_context":
		return FormatImplementBeadContextBlock(townRoot, rig, v)
	case "project_setup_stack":
		return FormatProjectSetupStackBlock(v)
	case "design_draft_context":
		return FormatDesignDraftContextBlock(townRoot, rig)
	case "phase_test_guards":
		return FormatPhaseTestGuards(townRoot, rig, v)
	default:
		return ""
	}
}

const maxDesignDraftInjectBytes = 4096

// FormatDesignDraftContextBlock injects the current architecture.md content
// into the prompt so the architect can build on prior work across turns.
func FormatDesignDraftContextBlock(townRoot, rig string) string {
	if rig == "" {
		return ""
	}
	archPath := filepath.Join(townRoot, rig, "mayor", "rig", "architecture.md")
	data, err := os.ReadFile(archPath)
	if err != nil || len(data) == 0 {
		return ""
	}
	content := string(data)
	if len(content) > maxDesignDraftInjectBytes {
		content = content[:maxDesignDraftInjectBytes] + "\n... (truncated)"
	}
	var b strings.Builder
	b.WriteString("## Current architecture.md draft (from prior turns)\n\n")
	b.WriteString("The following is the current content of `architecture.md`. **Do not send JSON success yet** — you MUST write the full revised content via a heredoc CMD (`cat > .../architecture.md <<'EOF'`) in this session. The validator rejects success without a heredoc write in the current run.\n\n")
	b.WriteString("Use the content below as your starting point. Fix any issues, then rewrite the complete file via heredoc. Do NOT wrap CMD in angle brackets — use plain `CMD: cat > ... <<'EOF'`.\n\n")
	b.WriteString("```markdown\n")
	b.WriteString(content)
	b.WriteString("\n```\n")
	return b.String()
}

// FormatPhaseTestGuards injects test-environment rules based on required_files patterns.
// For example, Next.js test files (*.test.tsx) need a @jest-environment jsdom docblock
// because next/jest's createJestConfig may override testEnvironment.
func FormatPhaseTestGuards(townRoot, rig string, v WorkflowValidation) string {
	scoped := v.ForActivePhase()
	files := scoped.RequiredFiles
	if len(files) == 0 {
		files = v.UnionRequiredFiles()
	}
	var hasJSDOMTest bool
	var hasGoTest bool
	var hasPythonTest bool
	var hasFrontendSource bool
	var hasSSE bool
	var hasTypeScriptTest bool
	for _, f := range files {
		lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(f)))
		if strings.HasSuffix(lower, ".test.tsx") || strings.HasSuffix(lower, ".test.ts") {
			if !strings.HasPrefix(lower, "e2e/") && !strings.HasPrefix(lower, "playwright/") && !strings.HasPrefix(lower, "cypress/") &&
				!strings.Contains(lower, "/e2e/") && !strings.Contains(lower, "/playwright/") && !strings.Contains(lower, "/cypress/") {
				hasJSDOMTest = true
			}
		}
		if strings.HasSuffix(lower, "_test.go") {
			hasGoTest = true
		}
		if strings.HasSuffix(lower, "_test.py") || strings.HasSuffix(lower, "test_.py") {
			hasPythonTest = true
		}
		if strings.HasSuffix(lower, ".tsx") || strings.HasSuffix(lower, ".jsx") ||
			strings.HasSuffix(lower, "/package.json") || lower == "package.json" {
			if !strings.HasPrefix(lower, "e2e/") && !strings.HasPrefix(lower, "playwright/") && !strings.HasPrefix(lower, "cypress/") &&
				!strings.Contains(lower, "/e2e/") && !strings.Contains(lower, "/playwright/") && !strings.Contains(lower, "/cypress/") {
				hasFrontendSource = true
			}
		}
		// Detect SSE/EventSource usage — frontend files that import or use EventSource
		if strings.HasSuffix(lower, ".tsx") || strings.HasSuffix(lower, ".ts") || strings.HasSuffix(lower, ".jsx") || strings.HasSuffix(lower, ".js") {
			if !strings.HasPrefix(lower, "e2e/") && !strings.HasPrefix(lower, "playwright/") && !strings.HasPrefix(lower, "cypress/") &&
				!strings.Contains(lower, "/e2e/") && !strings.Contains(lower, "/playwright/") && !strings.Contains(lower, "/cypress/") {
				// Could do a deeper scan for "EventSource" but presence of frontend source is sufficient signal
				hasSSE = true
			}
		}
		// Detect TypeScript test files for comprehensive stack guidance
		if strings.HasSuffix(lower, ".test.tsx") || strings.HasSuffix(lower, ".test.ts") ||
			strings.HasSuffix(lower, ".spec.tsx") || strings.HasSuffix(lower, ".spec.ts") {
			if !strings.HasPrefix(lower, "e2e/") && !strings.HasPrefix(lower, "playwright/") && !strings.HasPrefix(lower, "cypress/") &&
				!strings.Contains(lower, "/e2e/") && !strings.Contains(lower, "/playwright/") && !strings.Contains(lower, "/cypress/") {
				hasTypeScriptTest = true
			}
		}
	}
	// Trigger jsdom guard when frontend source files exist (test files are implementation artifacts, not listed in required_files).
	if hasFrontendSource {
		hasJSDOMTest = true
	}
	if !hasJSDOMTest && !hasGoTest && !hasPythonTest && !hasTypeScriptTest {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Test file conventions\n\n")
	if hasJSDOMTest {
		b.WriteString("- **Next.js test files (`*.test.tsx`, `*.test.ts`):** Add `// @jest-environment jsdom` as the first line of every test file. `next/jest`'s `createJestConfig` may override `testEnvironment` to `node`, causing `document is not defined` errors. The docblock forces jsdom per-file.\n")
	}
	if hasSSE {
		b.WriteString("- **SSE/EventSource components:** Add a Jest polyfill for `EventSource` in `jest.setup.ts` (or per test file) — jsdom does not provide it. Example:\n  ```ts\n  global.EventSource = class EventSource { constructor(url: string) { this.url = url; } close() {} onmessage: ((ev: MessageEvent) => void) | null = null; url: string; };\n  ```\n  Without this, tests rendering SSE components fail with `ReferenceError: EventSource is not defined`.\n")
	}
	if hasGoTest {
		b.WriteString("- **Go test files (`*_test.go`):** Use `package pkg_test` (external test package) for black-box tests. Import `testing` and use `t *testing.T` parameter. Run `go test -count=1 ./...` from the layout root.\n")
	}
	if hasPythonTest {
		b.WriteString("- **Python test files (`test_*.py`, `*_test.py`):** Use `unittest.TestCase` or `pytest` functions. Run `python3 -m pytest path/to/test_file.py -v` from the layout root.\n")
	}
	if hasTypeScriptTest {
		b.WriteString("- **TypeScript/Next.js test stack requirements:**\n")
		b.WriteString("  - **tsconfig.json:** Add `\"isolatedModules\": true` to compilerOptions. Without this, `ts-jest` emits `TS151002` warning and module resolution can fail.\n")
		b.WriteString("  - **Install dev deps:** `@types/jest`, `ts-jest` (or use `babel-jest` with `@babel/preset-typescript`), `identity-obj-proxy` for CSS modules.\n")
		b.WriteString("  - **jest.config.js:** Use `babel-jest` (not `ts-jest`) for faster transforms. Set `transformIgnorePatterns: ['/node_modules/(?!(recharts|@recharts)/)']` for ESM packages. Add `haste: { throwOnModuleCollision: false }` to silence naming collisions between `package.json` and `src/package.json`.\n")
		b.WriteString("  - **babel.config.js:** Use `@babel/preset-env` with `targets: { node: 'current' }`, `@babel/preset-react` with `runtime: 'automatic'`, `@babel/preset-typescript`. Add `@babel/preset-env` for ESM transpilation.\n")
		b.WriteString("  - **Install dev deps:** `@types/jest`, `@types/react`, `@types/react-dom`, `@babel/preset-env`, `@babel/preset-react`, `@babel/preset-typescript`, `babel-jest`, `identity-obj-proxy`, `jest-environment-jsdom`, `ts-jest` (optional, if not using babel-jest).\n")
		b.WriteString("  - **Test file imports:** Import components from `../components/Component` (relative to test file), NOT `../src/components/Component`. Test files live in `src/__tests__/`.\n")
		b.WriteString("  - **Component props:** Always pass required props in tests (e.g., `<PriceFlash priceChange={0} />`). TypeScript enforces this.\n")
		b.WriteString("  - **Jest setup:** Create `jest.setup.ts` with `@testing-library/jest-dom` import and any polyfills (EventSource, etc.). Reference it in `jest.config.js` via `setupFilesAfterLoad`.\n")
		b.WriteString("  - **Run command:** From the rig root (`{{rig}}/mayor/rig/`), run `npm install --ignore-scripts`, then `cd {{layout_root}} && npx jest --no-cache`.\n")
	}
	if hasGoTest {
		b.WriteString("- **Go test files (`*_test.go`):** Use `package pkg_test` (external test package) for black-box tests. Import `testing` and use `t *testing.T` parameter. Run `go test -count=1 ./...` from the layout root.\n")
	}
	if hasPythonTest {
		b.WriteString("- **Python test files (`test_*.py`, `*_test.py`):** Use `unittest.TestCase` or `pytest` functions. Run `python3 -m pytest path/to/test_file.py -v` from the layout root.\n")
	}
	return b.String()
}
func PromptContextBlocks(keys []string, townRoot, rig string, v WorkflowValidation) []string {
	var out []string
	for _, key := range keys {
		if b := PromptContextBlock(key, townRoot, rig, v); b != "" {
			out = append(out, b)
		}
	}
	return out
}

// RunPreRunHook runs a named pre_run step from rig-flow.yaml hooks.pre_run.
// Returns a log line for stdout when the hook did work (may be empty).
func RunPreRunHook(step, townRoot, rig string, v WorkflowValidation) (string, error) {
	switch step {
	case "bootstrap_implement_beads", "bootstrap_planning_beads": // legacy alias
		created, err := EnsurePlanningImplementBeads(townRoot, rig, v)
		if err != nil {
			return "", err
		}
		if len(created) > 0 {
			return fmt.Sprintf("auto-created implement beads: %s", joinStrings(created, ", ")), nil
		}
	case "enforce_implement_bead_queue":
		var logParts []string
		dupes, err := PruneDuplicateImplementBeads(townRoot, rig, v)
		if err != nil {
			return "", err
		}
		if len(dupes) > 0 {
			logParts = append(logParts, "deduped open: "+joinStrings(dupes, ", "))
		}
		closedDupes, err := PruneDuplicateClosedImplementBeads(townRoot, rig, v)
		if err != nil {
			return "", err
		}
		if len(closedDupes) > 0 {
			logParts = append(logParts, "deduped closed: "+joinStrings(closedDupes, ", "))
		}
		deleted, err := PruneExtraImplementBeads(townRoot, rig, v)
		if err != nil {
			return "", err
		}
		if len(deleted) > 0 {
			logParts = append(logParts, "pruned extras: "+joinStrings(deleted, ", "))
		}
		promoted, reopened, err := PromoteImplementQueueHead(townRoot, rig, v)
		if err != nil {
			return "", err
		}
		if len(reopened) > 0 {
			logParts = append(logParts, "single in_progress: reopened "+joinStrings(reopened, ", "))
		}
		if promoted != "" {
			logParts = append(logParts, "promoted queue head: "+promoted)
		}
		if len(logParts) > 0 {
			return "implement bead queue: " + joinStrings(logParts, "; "), nil
		}
	case "repair_planning_beads":
		syncV := ValidationForPlanningSync(townRoot, rig, v)
		logLine, err := RepairPlanningBeadSet(townRoot, rig, syncV)
		if err != nil {
			return "", err
		}
		if logLine != "" {
			return "planning bead repair: " + logLine, nil
		}
	case "sync_planning_artifacts":
		// Nested layouts need plan.md rewritten from required_files; setup/planner must not leave flat paths.
		syncV := ValidationForPlanningSync(townRoot, rig, v)
		forcePlan := RequiresExactImplementPaths(syncV)
		logLine, err := SyncPlanningArtifacts(townRoot, rig, syncV, forcePlan)
		if err != nil {
			return "", err
		}
		if logLine != "" {
			return "planning sync: " + logLine, nil
		}
	case "refresh_plan_md_if_stale":
		syncV := ValidationForPlanningSync(townRoot, rig, v)
		if !PlanningPlanMDNeedsRefresh(townRoot, rig, syncV) {
			return "", nil
		}
		forcePlan := RequiresExactImplementPaths(syncV)
		logLine, err := SyncPlanningArtifacts(townRoot, rig, syncV, forcePlan)
		if err != nil {
			return "", err
		}
		if logLine != "" {
			return "plan.md refresh: " + logLine, nil
		}
	case "refresh_codeindex":
		mayorRig := filepath.Join(townRoot, rig, "mayor", "rig")
		log, err := RefreshCodeindexIndex(mayorRig, v)
		if err != nil {
			return "", err
		}
		return log, nil
	case "ensure_http_implementation_config":
		return EnsureHTTPImplementationRigConfigLog(townRoot, rig, v)
	case "ensure_implement_smoke_ready":
		return EnsureImplementSmokeReadyLog(townRoot, rig, v)
	case "ensure_handler_phase_prerequisites":
		return EnsureHandlerPhasePrerequisitesLog(townRoot, rig, v)
	case "reopen_implement_beads", "reconcile_implement_beads":
		log, err := ReconcileImplementBeads(townRoot, rig, v)
		if err != nil {
			return "", err
		}
		if log != "" && log != "implement beads and required_files are consistent" {
			return "reconcile implement beads: " + log, nil
		}
	case "ensure_test_stack_ready":
		return EnsureTestStackReadyLog(townRoot, rig, v)
	case "ensure_playwright_config_ready":
		return EnsurePlaywrightConfigReady(townRoot, rig, v)
	case "ensure_go_mod_from_spec":
		logLine, err := EnsureGoModFromSpec(townRoot, rig, v)
		if err != nil {
			return "", err
		}
		return logLine, nil
	case "ensure_dolt_schema_health":
		if err := ensureDoltSchemaHealth(townRoot, rig); err != nil {
			return "", err
		}
	case "ensure_dolt_auto_commit":
		if err := ensureDoltAutoCommit(townRoot, rig); err != nil {
			return "", err
		}
	case "prune_stale_layout_files":
		return PruneStaleLayoutFilesLog(townRoot, rig, v)
	case "prune_rig_root_junk":
		mayorRig := filepath.Join(townRoot, rig, "mayor", "rig")
		return rigpkg.RemoveMayorRigAgentJunkLog(mayorRig)
	case "reset_layout_pre_implementation":
		mayorRig := filepath.Join(townRoot, rig, "mayor", "rig")
		layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
		if layout == "" {
			layout = rigpkg.InferLayoutRootFromMayorRig(mayorRig)
		}
		if layout == "" {
			return "", nil
		}
		removed, err := rigpkg.ResetLayoutPreImplementation(mayorRig, layout)
		if err != nil {
			return "", err
		}
		if len(removed) == 0 {
			return "", nil
		}
		if len(removed) > 8 {
			return fmt.Sprintf("reset layout pre-implementation: removed %d stale files under %s/", len(removed), layout), nil
		}
		return fmt.Sprintf("reset layout pre-implementation: removed %s", joinStrings(removed, ", ")), nil
	case "close_project_setup_beads":
		closed, err := CloseProjectSetupBeads(townRoot, rig, v)
		if err != nil {
			return "", err
		}
		if len(closed) > 0 {
			return "closed project_setup beads: " + joinStrings(closed, ", "), nil
		}
	}
	return "", nil
}

func EnsureTestStackReadyLog(townRoot, rig string, v WorkflowValidation) (string, error) {
	scoped := v.ForActivePhase()
	files := scoped.RequiredFiles
	if len(files) == 0 {
		files = v.UnionRequiredFiles()
	}
	hasFrontend := false
	hasTypeScriptTest := false
	for _, f := range files {
		lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(f)))
		if strings.HasSuffix(lower, ".tsx") || strings.HasSuffix(lower, ".jsx") ||
			strings.HasSuffix(lower, "/package.json") || lower == "package.json" {
			if !strings.HasPrefix(lower, "e2e/") && !strings.HasPrefix(lower, "playwright/") && !strings.HasPrefix(lower, "cypress/") &&
				!strings.Contains(lower, "/e2e/") && !strings.Contains(lower, "/playwright/") && !strings.Contains(lower, "/cypress/") {
				hasFrontend = true
			}
		}
		if strings.HasSuffix(lower, ".test.tsx") || strings.HasSuffix(lower, ".test.ts") ||
			strings.HasSuffix(lower, ".spec.tsx") || strings.HasSuffix(lower, ".spec.ts") {
			if !strings.HasPrefix(lower, "e2e/") && !strings.HasPrefix(lower, "playwright/") && !strings.HasPrefix(lower, "cypress/") &&
				!strings.Contains(lower, "/e2e/") && !strings.Contains(lower, "/playwright/") && !strings.Contains(lower, "/cypress/") {
				hasTypeScriptTest = true
			}
		}
	}
	if !hasFrontend && !hasTypeScriptTest {
		return "", nil
	}

	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	if layout == "" {
		return "", nil
	}
	layoutDir := filepath.Join(rigDir, layout)

	var actions []string

	// Check tsconfig.json for isolatedModules
	tsconfigPath := filepath.Join(layoutDir, "tsconfig.json")
	if data, err := os.ReadFile(tsconfigPath); err == nil {
		var cfg map[string]interface{}
		if json.Unmarshal(data, &cfg) == nil {
			if co, ok := cfg["compilerOptions"].(map[string]interface{}); ok {
				if _, ok := co["isolatedModules"]; !ok {
					co["isolatedModules"] = true
					cfg["compilerOptions"] = co
					newData, _ := json.MarshalIndent(cfg, "", "  ")
					os.WriteFile(tsconfigPath, newData, 0644)
					actions = append(actions, "tsconfig.json: added isolatedModules=true")
				}
			}
		}
	}

	// Check jest.config.js for haste collision fix
	jestConfigPath := filepath.Join(layoutDir, "jest.config.js")
	if _, err := os.Stat(jestConfigPath); err == nil {
		data, _ := os.ReadFile(jestConfigPath)
		content := string(data)
		if !strings.Contains(content, "throwOnModuleCollision: false") && !strings.Contains(content, "throwOnModuleCollision") {
			content = strings.Replace(content, "};", `  haste: {
    forceNodeFilesystemAPI: true,
    throwOnModuleCollision: false,
  },
};`, 1)
			os.WriteFile(jestConfigPath, []byte(content), 0644)
			actions = append(actions, "jest.config.js: added haste collision fix")
		}
	}

	// Check babel.config.js for proper presets
	babelPath := filepath.Join(layoutDir, "babel.config.js")
	if _, err := os.Stat(babelPath); err == nil {
		data, _ := os.ReadFile(babelPath)
		content := string(data)
		updated := false
		if !strings.Contains(content, "@babel/preset-env") {
			content = strings.Replace(content, "presets: [", `presets: [
    ["@babel/preset-env", { targets: { node: "current" } }],`, 1)
			updated = true
		}
		if !strings.Contains(content, "@babel/preset-typescript") {
			content = strings.Replace(content, `"@babel/preset-react"`, `"@babel/preset-typescript", "@babel/preset-react"`, 1)
			updated = true
		}
		if updated {
			os.WriteFile(babelPath, []byte(content), 0644)
			actions = append(actions, "babel.config.js: added @babel/preset-env and @babel/preset-typescript")
		}
	}

	// Check jest.setup.ts for EventSource polyfill
	setupPath := filepath.Join(layoutDir, "jest.setup.ts")
	if _, err := os.Stat(setupPath); err == nil {
		data, _ := os.ReadFile(setupPath)
		content := string(data)
		if !strings.Contains(content, "EventSource") {
			content += "\n// Polyfill for EventSource in jsdom\nif (typeof global.EventSource === 'undefined') {\n  global.EventSource = class EventSource {\n    constructor(url) { this.url = url; }\n    close() {}\n    onmessage: ((ev: MessageEvent) => void) | null = null;\n    url: string;\n  };\n}"
			os.WriteFile(setupPath, []byte(content), 0644)
			actions = append(actions, "jest.setup.ts: added EventSource polyfill")
		}
	} else {
		content := "// Polyfill for EventSource in jsdom\nif (typeof global.EventSource === 'undefined') {\n  global.EventSource = class EventSource {\n    constructor(url) { this.url = url; }\n    close() {}\n    onmessage: ((ev: MessageEvent) => void) | null = null;\n    url: string;\n  };\n}\nimport \"@testing-library/jest-dom\";\n"
		os.WriteFile(setupPath, []byte(content), 0644)
		actions = append(actions, "jest.setup.ts: created with EventSource polyfill and @testing-library/jest-dom")
	}

	// Ensure jest.config.js references jest.setup.ts
	jestConfigPath = filepath.Join(layoutDir, "jest.config.js")
	if _, err := os.Stat(jestConfigPath); err == nil {
		data, _ := os.ReadFile(jestConfigPath)
		content := string(data)
		if !strings.Contains(content, "setupFilesAfterLoad") && !strings.Contains(content, "setupFilesAfterLoad") {
			content = strings.Replace(content, "};", `  setupFilesAfterLoad: ["<rootDir>/jest.setup.ts"],
};`, 1)
			os.WriteFile(jestConfigPath, []byte(content), 0644)
			actions = append(actions, "jest.config.js: added setupFilesAfterLoad")
		}
	}

	if len(actions) > 0 {
		return "test stack auto-fix: " + strings.Join(actions, "; "), nil
	}
	return "", nil
}

// EnsurePlaywrightConfigReady creates playwright.config.ts if e2e tests exist but config is missing.
// Scans the union of required files across all delivery phases so the config is created even when
// the e2e specs belong to a future phase (the common case while earlier phases are still active).
//
// The config is written to the layout root (e.g. <layout>/playwright.config.ts), because the rig's
// qa_verify_command typically runs `npm run e2e` from the layout root — Playwright resolves the
// config from the CWD upward, never into a child test/ directory. testDir points at the e2e dir.
func EnsurePlaywrightConfigReady(townRoot, rig string, v WorkflowValidation) (string, error) {
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	if layout == "" {
		return "", nil
	}
	layoutDir := filepath.Join(rigDir, layout)

	// Detect the e2e spec dir relative to the layout root (e.g. "tests" for
	// "<layout>/tests/e2e/foo.spec.ts", "test" for "<layout>/test/e2e/...").
	testDirRel := ""
	for _, f := range v.UnionRequiredFiles() {
		lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(f)))
		if !isPlaywrightSpecPath(lower) {
			continue
		}
		rel := lower
		if strings.HasPrefix(lower, layout+"/") {
			rel = strings.TrimPrefix(lower, layout+"/")
		}
		if idx := strings.Index(rel, "/e2e/"); idx >= 0 {
			testDirRel = rel[:idx]
		} else {
			testDirRel = "test"
		}
		break
	}

	// Docker/Playwright harness detection must happen before the existence
	// check: a model-authored config for a host-run rig carries a webServer
	// block that launches the app server INSIDE the Playwright container
	// (where e.g. Go isn't installed) — that config has to be repaired, not
	// respected.
	hasDockerPlaywright := false
	for _, p := range v.DeliveryPhases {
		if phaseShipsDockerPlaywright(&p) {
			hasDockerPlaywright = true
			break
		}
	}

	if testDirRel == "" {
		return "", nil
	}

	testDir := layoutDir
	if testDirRel != "." {
		testDir = filepath.Join(layoutDir, testDirRel)
	}

	pwConfigPath := filepath.Join(layoutDir, "playwright.config.ts")
	existingPath := ""
	existingContent := ""
	for _, p := range []string{
		pwConfigPath,
		filepath.Join(layoutDir, "playwright.config.js"),
		filepath.Join(testDir, "playwright.config.ts"),
		filepath.Join(testDir, "playwright.config.js"),
	} {
		if b, err := os.ReadFile(p); err == nil {
			existingPath = p
			existingContent = string(b)
			break
		}
	}

	if existingContent != "" {
		if hasDockerPlaywright && strings.Contains(existingContent, "webServer") {
			log.Printf("[playwright-config] repairing %s: dropping webServer block — host-run rig uses the Docker harness", existingPath)
			// Overwrite the offending config in place (it may live in the
			// test dir rather than the layout root).
			pwConfigPath = existingPath
		} else if !hasDockerPlaywright && strings.Contains(existingContent, "host.docker.internal") {
			return "", nil // reverse drift after a stack change; leave as-is
		} else {
			return "", nil // healthy existing config
		}
	}

	// Determine baseURL from workflow profile or default to 8000
	port := v.DevServerPort
	if port <= 0 {
		port = 8000
	}

	e2eRel := testDirRel + "/e2e"
	if testDirRel == "." {
		e2eRel = "e2e"
	}

	// Choose the dev-server command generically for Go, Python, and Node rigs.
	serverCmd := DevServerCommand(layoutDir, v)
	// Resolve '@playwright/test' from wherever it is installed: prefer the layout
	// root (matches "run from layout root" QA), else the test dir, so the config
	// loads even when node_modules lives in a subdir (e.g. finally's test/).
	pwImport := playwrightImport(layoutDir, testDir, testDirRel)

	pwConfig := fmt.Sprintf(`import { defineConfig, devices } from %s;

export default defineConfig({
  testDir: './%s',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 1 : undefined,
  reporter: 'html',
  use: {
    baseURL: 'http://localhost:%d',
    trace: 'on-first-retry',
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
%s});
`, pwImport, e2eRel, port, func() string {
		if hasDockerPlaywright {
			return ""
		}
		return webServerBlock(serverCmd, port)
	}())

	if err := os.WriteFile(pwConfigPath, []byte(pwConfig), 0644); err != nil {
		return "", err
	}

	// Install test dependencies if package.json exists
	packageJSONPath := filepath.Join(testDir, "package.json")
	if _, err := os.Stat(packageJSONPath); err == nil {
		cmd := exec.Command("npm", "install")
		cmd.Dir = testDir
		cmd.Env = append(os.Environ(), "CI=true")
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("npm install in test dir failed: %v: %s", err, string(out))
		}
	}

	if hasDockerPlaywright {
		ensureHarnessComposeFile(layoutDir)
	}

	return fmt.Sprintf("auto-created %s with baseURL http://localhost:%d", pwConfigPath, port), nil
}

// ensureHarnessComposeFile writes (or repairs) a test-harness-only
// docker-compose.yml at the layout root for Docker/Playwright rigs whose SPEC
// mandates the application runs on the HOST. The Playwright container uses
// network_mode:host so the generated config's http://localhost baseURL reaches
// the host server directly. An existing compose that BUILDS an application
// image contradicts such specs and is replaced; a compose already referencing
// the shared runner image is left untouched.
func ensureHarnessComposeFile(layoutDir string) {
	path := filepath.Join(layoutDir, "docker-compose.yml")
	existingBytes, err := os.ReadFile(path)
	if err == nil && strings.Contains(string(existingBytes), "playwright-go-test") {
		return // already canonical
	}
	compose := `# Test-harness ONLY — the application server runs on the HOST per SPEC.
# network_mode:host lets this container reach it at http://localhost:<port>.
services:
  playwright:
    image: playwright-go-test:latest
    user: "${DOCKER_UID:-1000}:${DOCKER_GID:-1000}"
    working_dir: /src
    network_mode: host
    volumes:
      - .:/src
    command: >
      /bin/sh -c "npm install --ignore-scripts && npx playwright test"
`
	if err := os.WriteFile(path, []byte(compose), 0644); err != nil {
		log.Printf("[harness-compose] write %s failed: %v", path, err)
		return
	}
	if len(existingBytes) > 0 {
		log.Printf("[harness-compose] replaced non-canonical %s (host-run rig must not build an app image)", path)
	} else {
		log.Printf("[harness-compose] created %s", path)
	}
}

// playwrightImport returns the module specifier for '@playwright/test' that
// resolves from the layout root. If the package is not installed there but is
// present in the test dir, a relative import is used so config loading works
// even when node_modules lives in a subdir.
func playwrightImport(layoutDir, testDir, testDirRel string) string {
	if hasPlaywrightPackage(layoutDir) {
		return "'@playwright/test'"
	}
	if hasPlaywrightPackage(testDir) && testDirRel != "." {
		return "'./" + strings.TrimPrefix(filepath.ToSlash(testDirRel), "./") + "/node_modules/@playwright/test'"
	}
	return "'@playwright/test'"
}

// hasPlaywrightPackage reports whether <dir>/node_modules/@playwright/test exists.
func hasPlaywrightPackage(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, "node_modules", "@playwright", "test")); err == nil {
		return true
	}
	return false
}

// webServerBlock renders the webServer section of a generated playwright.config.ts.
// When no dev-server command can be derived the block is omitted and playwright
// relies on a server the agent started externally.
func webServerBlock(serverCmd string, port int) string {
	if serverCmd == "" {
		return ""
	}
	return fmt.Sprintf(`  webServer: {
    command: '%s',
    url: 'http://localhost:%d',
    reuseExistingServer: !process.env.CI,
    timeout: 120000,
  },
`, serverCmd, port)
}

// isPlaywrightSpecPath reports whether a layout-relative path is a Playwright
// e2e spec file (foo.spec.ts/js/tsx under a test/e2e, tests/e2e, or e2e dir).
func isPlaywrightSpecPath(path string) bool {
	if !(strings.HasSuffix(path, ".spec.ts") || strings.HasSuffix(path, ".spec.js") ||
		strings.HasSuffix(path, ".spec.tsx")) {
		return false
	}
	return strings.HasPrefix(path, "test/e2e/") || strings.HasPrefix(path, "tests/e2e/") ||
		strings.HasPrefix(path, "e2e/") || strings.Contains(path, "/e2e/")
}

func joinStrings(ss []string, sep string) string {
	if len(ss) == 0 {
		return ""
	}
	out := ss[0]
	for i := 1; i < len(ss); i++ {
		out += sep + ss[i]
	}
	return out
}
