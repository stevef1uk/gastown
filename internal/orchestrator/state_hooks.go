package orchestrator

import "strings"

// StateHooks configures gt-agent behavior for one FSM state (declarative; no state-name switches in gt-agent).
// Wire new behavior via templates/rig-flow.yaml — see prompt_context.go and town/README.md (§ FSM vs Go).
type StateHooks struct {
	MaxCmdTurns int `yaml:"max_cmd_turns,omitempty" json:"max_cmd_turns,omitempty"`

	PreRun   []string `yaml:"pre_run,omitempty" json:"pre_run,omitempty"`
	// OnTimeout runs when a state exceeds state_timeout_seconds or max_cmd_turns is exhausted (e.g. reset_planning_phase, recover_implementation_stall).
	OnTimeout []string `yaml:"on_timeout,omitempty" json:"on_timeout,omitempty"`
	// StateTimeoutSeconds is the wall-clock limit for the current FSM state (0 = disabled).
	StateTimeoutSeconds int `yaml:"state_timeout_seconds,omitempty" json:"state_timeout_seconds,omitempty"`
	// CmdTimeoutSeconds caps each shell CMD in gt-agent for this state (0 = default per command type).
	CmdTimeoutSeconds int `yaml:"cmd_timeout_seconds,omitempty" json:"cmd_timeout_seconds,omitempty"`
	PerTurn  []string `yaml:"per_turn,omitempty" json:"per_turn,omitempty"`
	// PromptContext lists prompt_context hook names (see orchestrator.PromptContextBlock).
	// Example keys: planning_bead_bootstrap, implementation_queue.
	PromptContext []string `yaml:"prompt_context,omitempty" json:"prompt_context,omitempty"`
	CmdGuard string   `yaml:"cmd_guard,omitempty" json:"cmd_guard,omitempty"`

	CmdRewrites []string `yaml:"cmd_rewrites,omitempty" json:"cmd_rewrites,omitempty"`

	Env StateEnvHooks `yaml:"env,omitempty" json:"env,omitempty"`

	PipRepairBeforeRun bool `yaml:"pip_repair_before_run,omitempty" json:"pip_repair_before_run,omitempty"`
	Python3Rewrite     bool `yaml:"python3_rewrite,omitempty" json:"python3_rewrite,omitempty"`

	Track string `yaml:"track,omitempty" json:"track,omitempty"`

	AutoVerify []AutoVerifyHook `yaml:"auto_verify,omitempty" json:"auto_verify,omitempty"`

	Artifacts string `yaml:"artifacts,omitempty" json:"artifacts,omitempty"`
	// PostArtifactSuccess runs named hooks after artifact validation passes (e.g. close_project_setup_beads).
	PostArtifactSuccess []string `yaml:"post_artifact_success,omitempty" json:"post_artifact_success,omitempty"`
	// AutoVerifyOKClearsCmdFailure clears hadCmdFailure after a successful auto_verify (project_setup recovery).
	AutoVerifyOKClearsCmdFailure bool `yaml:"auto_verify_ok_clears_cmd_failure,omitempty" json:"auto_verify_ok_clears_cmd_failure,omitempty"`

	RetryHint    string `yaml:"retry_hint,omitempty" json:"retry_hint,omitempty"`
	RetryHintKey string `yaml:"retry_hint_key,omitempty" json:"retry_hint_key,omitempty"`
	FailureHint  string `yaml:"failure_hint,omitempty" json:"failure_hint,omitempty"`

	BeadIDsInSummary bool `yaml:"bead_ids_in_summary,omitempty" json:"bead_ids_in_summary,omitempty"`
	EmptyBdListOK    bool `yaml:"empty_bd_list_ok,omitempty" json:"empty_bd_list_ok,omitempty"`

	// Prompt framing (prefer these over if task.State in gt-agent).
	OmitOrchestratorContext bool   `yaml:"omit_orchestrator_context,omitempty" json:"omit_orchestrator_context,omitempty"`
	SystemPromptFooter      string `yaml:"system_prompt_footer,omitempty" json:"system_prompt_footer,omitempty"`
	UserPromptWrapper       string `yaml:"user_prompt_wrapper,omitempty" json:"user_prompt_wrapper,omitempty"` // "none" = no "Complete this step only" prefix
	FailurePromptContext    []string `yaml:"failure_prompt_context,omitempty" json:"failure_prompt_context,omitempty"` // prompt_context keys on validation/empty-reply nudges only
	EmptyResponseSuffix     string `yaml:"empty_response_suffix,omitempty" json:"empty_response_suffix,omitempty"`
	// AppendGoCompileContext injects head of .go files mentioned in failed go build/tidy output (stateless LLM repair).
	AppendGoCompileContext bool `yaml:"append_go_compile_context,omitempty" json:"append_go_compile_context,omitempty"`
	// NativeEditTools enables READ:/EDIT:/WRITE: file tools in gt-agent (preferred over sed/heredoc for implementation).
	NativeEditTools bool `yaml:"native_edit_tools,omitempty" json:"native_edit_tools,omitempty"`
}

// NativeEditPromptSection returns orchestrator-context instructions when native_edit_tools is enabled.
func (h StateHooks) NativeEditPromptSection() string {
	if !h.NativeEditTools {
		return ""
	}
	return strings.TrimSpace(`## Native file tools (preferred for source edits)
Use these instead of ` + "`" + `sed` + "`" + ` / ` + "`" + `patch` + "`" + ` / ` + "`" + `cat > file <<'EOF'` + "`" + ` on existing files:

` + "`" + `READ: <path-under-layout>` + "`" + ` — show file contents (dependencies + active bead allowed).

` + "`" + `EDIT: <path>` + "`" + ` — unique search/replace (must match file exactly once):
` + "`" + `` + NativeEditSearchMarker + `
exact old lines
` + NativeEditReplaceMarker + `
new lines
` + NativeEditEndMarker + `` + "`" + `

` + "`" + `WRITE: <path>` + "`" + ` — create a **new** file (or stub); body until a line containing only ` + "`" + NativeEditWriteEnd + "`" + `.

Still use ` + "`" + `CMD:` + "`" + ` for ` + "`" + `bd update` + "`" + `, ` + "`" + `bd close` + "`" + `, and **Verify** from Next bead. JSON outcome in a later message with no CMD/native tools.`)
}

// Markers exported for prompts/tests (duplicate literals in gt-agent).
const (
	NativeEditSearchMarker  = "<<<<<<< SEARCH"
	NativeEditReplaceMarker = "======="
	NativeEditEndMarker     = ">>>>>>> REPLACE"
	NativeEditWriteEnd      = "---END WRITE---"
)

// StateEnvHooks configures subprocess environment for a state.
type StateEnvHooks struct {
	BeadsDir   bool   `yaml:"beads_dir,omitempty" json:"beads_dir,omitempty"`
	PythonPATH bool   `yaml:"pythonpath,omitempty" json:"pythonpath,omitempty"`
	PythonVenv string `yaml:"python_venv,omitempty" json:"python_venv,omitempty"` // off | create | activate
}

// AutoVerifyHook runs a verify command after matching commands succeed.
type AutoVerifyHook struct {
	When   string `yaml:"when" json:"when"`
	Verify string `yaml:"verify" json:"verify"` // go_with_tidy | python | profile
}

// RetryHintText returns retry guidance for agents (YAML text or computed key).
func (h StateHooks) RetryHintText(v WorkflowValidation, vars map[string]string) string {
	if t := strings.TrimSpace(h.RetryHint); t != "" {
		return SubstituteVars(t, vars) + "\n"
	}
	if k := strings.TrimSpace(h.RetryHintKey); k != "" {
		return SubstituteVars(resolveRetryHintKey(k, v, vars), vars) + "\n"
	}
	return "One CMD: per line.\n"
}

// FailureHintText returns validation-failure guidance for the agent.
func (h StateHooks) FailureHintText(v WorkflowValidation, vars map[string]string) string {
	if t := strings.TrimSpace(h.FailureHint); t != "" {
		return SubstituteVars(t, vars)
	}
	return ""
}

// SystemPromptFooterText returns optional per-state footer appended after the prompt file ({{var}} substitution).
func (h StateHooks) SystemPromptFooterText(vars map[string]string) string {
	return SubstituteVars(strings.TrimSpace(h.SystemPromptFooter), vars)
}

// UserPromptWrapsWithCompleteStep reports whether gt-agent should prefix user prompts with "Complete this step only".
func (h StateHooks) UserPromptWrapsWithCompleteStep() bool {
	return !strings.EqualFold(strings.TrimSpace(h.UserPromptWrapper), "none")
}

func resolveRetryHintKey(key string, v WorkflowValidation, vars map[string]string) string {
	switch key {
	case "project_setup":
		if WorkflowUsesGo(v) {
			layout := v.LayoutRootDir()
			if layout == "" {
				layout = "."
			}
			return "Go rig: run go mod init/get/tidy under " + layout +
				" only — never cat/heredoc/touch source files (.go/.js/.html/.css) under " + layout +
				"/ (polecat implements them). No heredoc go.mod/go.sum, no go build/run/curl in setup. Green verify: " +
				GoProjectSetupVerifyCommand(v) + ". Then JSON success in a separate message."
		}
		if WorkflowUsesPython(v) {
			req := v.RequirementsFilePath()
			if req == "" {
				req = "requirements.txt"
			}
			return "Python rig: create " + v.PythonVenvRelDir() + " with python3 -m venv, pip install -r " + req +
				" once, split beads one file each. Green verify: " + PythonProjectSetupVerifyCommand(v)
		}
		return "Run project_setup per profile (Go or Python); do not skip with empty success."
	case "implementation":
		layout := v.LayoutRootDir()
		return "One CMD: per line. Run `bd list` first; use only bead IDs from that output. " +
			"Create files under " + layout + "/ per bead titles and profile required_files. " +
			"Heredoc: `cat > path <<'EOF'` then EOF alone on its own line."
	default:
		if rig := vars["rig"]; rig != "" {
			return "Work from " + rig + "/mayor/rig with BEADS_DIR=$GT_ROOT/" + rig + "/.beads. One CMD: per line."
		}
		return "One CMD: per line."
	}
}

// EffectiveMaxCmdTurns returns per-state turn limit (default 5; QA/plan_review often 8).
func (h StateHooks) EffectiveMaxCmdTurns() int {
	if h.MaxCmdTurns > 0 {
		return h.MaxCmdTurns
	}
	return 5
}

// EffectiveStateTimeoutSeconds returns the wall-clock limit for this state (0 = disabled).
func (h StateHooks) EffectiveStateTimeoutSeconds() int {
	if h.StateTimeoutSeconds > 0 {
		return h.StateTimeoutSeconds
	}
	return 0
}

// EffectiveCmdTimeoutSeconds returns per-shell-command wall clock for gt-agent (0 = use defaults).
func (h StateHooks) EffectiveCmdTimeoutSeconds() int {
	if h.CmdTimeoutSeconds > 0 {
		return h.CmdTimeoutSeconds
	}
	return 0
}
