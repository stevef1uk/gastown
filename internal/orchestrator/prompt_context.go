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
	"fmt"
	"os"
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
	}
	if !hasJSDOMTest && !hasGoTest && !hasPythonTest {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Test file conventions\n\n")
	if hasJSDOMTest {
		b.WriteString("- **Next.js test files (`*.test.tsx`, `*.test.ts`):** Add `// @jest-environment jsdom` as the first line of every test file. `next/jest`'s `createJestConfig` may override `testEnvironment` to `node`, causing `document is not defined` errors. The docblock forces jsdom per-file.\n")
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
	case "prune_stale_layout_go":
		return PruneStaleLayoutGoFilesLog(townRoot, rig, v)
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
