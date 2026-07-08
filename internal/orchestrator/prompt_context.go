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
	default:
		return ""
	}
}

// PromptContextBlocks resolves all keys from a state's hooks.prompt_context list.
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
