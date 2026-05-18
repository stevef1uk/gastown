package orchestrator

import "strings"

// StateHooks configures gt-agent behavior for one FSM state (declarative; no state-name switches in gt-agent).
type StateHooks struct {
	MaxCmdTurns int `yaml:"max_cmd_turns,omitempty" json:"max_cmd_turns,omitempty"`

	PreRun   []string `yaml:"pre_run,omitempty" json:"pre_run,omitempty"`
	PerTurn  []string `yaml:"per_turn,omitempty" json:"per_turn,omitempty"`
	CmdGuard string   `yaml:"cmd_guard,omitempty" json:"cmd_guard,omitempty"`

	CmdRewrites []string `yaml:"cmd_rewrites,omitempty" json:"cmd_rewrites,omitempty"`

	Env StateEnvHooks `yaml:"env,omitempty" json:"env,omitempty"`

	PipRepairBeforeRun bool `yaml:"pip_repair_before_run,omitempty" json:"pip_repair_before_run,omitempty"`
	Python3Rewrite     bool `yaml:"python3_rewrite,omitempty" json:"python3_rewrite,omitempty"`

	Track string `yaml:"track,omitempty" json:"track,omitempty"`

	AutoVerify []AutoVerifyHook `yaml:"auto_verify,omitempty" json:"auto_verify,omitempty"`

	Artifacts string `yaml:"artifacts,omitempty" json:"artifacts,omitempty"`

	RetryHint    string `yaml:"retry_hint,omitempty" json:"retry_hint,omitempty"`
	RetryHintKey string `yaml:"retry_hint_key,omitempty" json:"retry_hint_key,omitempty"`
	FailureHint  string `yaml:"failure_hint,omitempty" json:"failure_hint,omitempty"`

	BeadIDsInSummary bool `yaml:"bead_ids_in_summary,omitempty" json:"bead_ids_in_summary,omitempty"`
	EmptyBdListOK    bool `yaml:"empty_bd_list_ok,omitempty" json:"empty_bd_list_ok,omitempty"`
}

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

func resolveRetryHintKey(key string, v WorkflowValidation, vars map[string]string) string {
	switch key {
	case "project_setup":
		if WorkflowUsesGo(v) {
			layout := v.LayoutRootDir()
			if layout == "" {
				layout = "."
			}
			return "Go rig: run go mod init/get/tidy under " + layout +
				" (never heredoc go.mod/go.sum, no go build/run/curl in setup). Split oversized beads with bd create/delete so each bead is one file. Green verify: " +
				GoProjectSetupVerifyCommand(v)
		}
		if WorkflowUsesPython(v) {
			req := v.RequirementsFilePath()
			if req == "" {
				req = "requirements.txt"
			}
			return "Python rig: create " + v.PythonVenvRelDir() + " with python3 -m venv, pip install -r " + req +
				" once, split beads one file each. Green verify: " + PythonVerifyCommand(v)
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
