package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/agentenv"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/orchestrator"
	rigpkg "github.com/steveyegge/gastown/internal/rig"
)

func taskHooks(task *orchestrator.Task) orchestrator.StateHooks {
	if task == nil {
		return orchestrator.StateHooks{}
	}
	return task.Hooks
}

// cmdTracker accumulates per-attempt command outcomes for artifact validation.
type cmdTracker struct {
	designArchWritten bool
	hadCmdFailure     bool
	verifyOK          bool
	beadCreateOK      bool
	beadDeleteOK      bool
	beadCloseOK       bool
	listOpenOK        bool
	didDelete         bool
	bdListClosedOK    bool
	unittestOK        bool
	activeBead        string
}

type stateRunner struct {
	task        *orchestrator.Task
	townRoot    string
	rig         string
	hooks       orchestrator.StateHooks
	v           orchestrator.WorkflowValidation
	promptVars  map[string]string
	track       *cmdTracker
}

func newStateRunner(task *orchestrator.Task, townRoot, rig string) *stateRunner {
	v := taskValidation(task)
	vars := map[string]string{"rig": rig}
	for k, val := range v.PromptVars() {
		vars[k] = val
	}
	return &stateRunner{
		task:       task,
		townRoot:   townRoot,
		rig:        rig,
		hooks:      taskHooks(task),
		v:          v,
		promptVars: vars,
		track:      &cmdTracker{},
	}
}

func (r *stateRunner) maxTurns() int {
	return r.hooks.EffectiveMaxCmdTurns()
}

func (r *stateRunner) runPreRun() {
	for _, step := range r.hooks.PreRun {
		switch step {
		case "repair_requirements":
			maybeRepairWorkflowRequirements(r.townRoot, r.rig, r.v)
		case "reopen_implement_beads":
			if reopened, err := orchestrator.EnsureImplementBeadsAvailable(r.townRoot, r.rig, r.v); err != nil {
				orchestratedFprintfStderr("[gt-agent] reopen implement beads: %v\n", err)
			} else if len(reopened) > 0 {
				orchestratedPrintf("[gt-agent] auto-reopened implement beads: %s\n", strings.Join(reopened, ", "))
			}
		}
	}
}

func (r *stateRunner) runPerTurn() {
	for _, step := range r.hooks.PerTurn {
		switch step {
		case "repair_requirements":
			maybeRepairWorkflowRequirements(r.townRoot, r.rig, r.v)
		}
	}
}

func (r *stateRunner) rejectScope() string {
	switch r.hooks.CmdGuard {
	case "design":
		return "architect scope"
	case "planning":
		return "planner scope"
	case "project_setup":
		return "project setup scope"
	case "implementation":
		return "polecat scope"
	case "plan_review":
		return "plan review scope"
	case "qa":
		return "QA scope"
	default:
		return "command scope"
	}
}

func (r *stateRunner) validateCommand(cmd string) error {
	switch r.hooks.CmdGuard {
	case "design":
		return validateDesignCommand(cmd, r.rig)
	case "planning":
		return validatePlanningCommand(cmd, r.rig)
	case "project_setup":
		return validateProjectSetupCommand(cmd, r.rig, r.v)
	case "implementation":
		return validateImplementationCommandWithState(cmd, r.rig, r.track.activeBead, r.v, r.track.verifyOK)
	case "plan_review":
		return validatePlanReviewCommand(cmd, r.rig)
	case "qa":
		return validateQACommand(cmd, r.rig, r.v)
	default:
		return nil
	}
}

func (r *stateRunner) rewriteCommand(cmd string) string {
	for _, rw := range r.hooks.CmdRewrites {
		switch rw {
		case "rig_placeholders":
			if fixed, ok := rewriteOrchestratedRigPlaceholders(cmd, r.townRoot, r.rig); ok {
				orchestratedPrintf("[gt-agent] rewrote RIG placeholder paths → %s: %s\n", r.rig, fixed)
				cmd = fixed
			}
		case "spec_md_case":
			if fixed, ok := rewriteSpecMDPathCaseInsensitive(cmd); ok {
				cmd = fixed
			}
		case "plan_md_after_cd":
			if fixed, ok := rewritePlanMDPathAfterCD(cmd, r.rig); ok {
				orchestratedPrintf("[gt-agent] rewrote plan.md path after cd: %s\n", fixed)
				cmd = fixed
			}
		case "backend_path_after_cd":
			if fixed, ok := rewriteBackendPathAfterCD(cmd, r.rig, r.v.LayoutRoot); ok {
				orchestratedPrintf("[gt-agent] rewrote layout path after cd: %s\n", fixed)
				cmd = fixed
			}
		case "bd_list_implement_scope":
			if title := r.v.BeadTitleContains; title != "" {
				if fixed, ok := rewriteBdListImplementScope(cmd, title); ok {
					orchestratedPrintf("[gt-agent] scoped bd list to implement beads: %s\n", fixed)
					cmd = fixed
				}
			}
		case "unittest_workdir":
			if fixed, ok := rewriteUnittestToWorkdir(cmd, r.rig, r.v); ok {
				orchestratedPrintf("[gt-agent] rewrote verify/toolchain cmd for workdir: %s\n", fixed)
				cmd = fixed
			}
		case "bd_list_limit":
			if fixed, ok := rewriteBdListLimit(cmd); ok {
				cmd = fixed
			}
		case "unwrap_bash_lc":
			if unwrapped := agentenv.UnwrapBashLcSingleLine(cmd); unwrapped != cmd {
				orchestratedPrintf("[gt-agent] unwrapped bash -lc → %s\n", unwrapped)
				cmd = unwrapped
			}
		}
	}
	return cmd
}

func (r *stateRunner) repairPipBeforeRun(cmd string) {
	if !r.hooks.PipRepairBeforeRun {
		return
	}
	if reqRel := agentenv.RequirementsPathFromPipInstall(cmd); reqRel != "" {
		reqPath := filepath.Join(rigMayorRigDir(r.townRoot, r.rig), reqRel)
		if changed, err := agentenv.RepairRequirementsFile(reqPath); err != nil {
			orchestratedFprintfStderr("[gt-agent] requirements repair: %v\n", err)
		} else if changed {
			orchestratedPrintf("[gt-agent] repaired shell lines in %s\n", reqRel)
		}
	}
}

func (r *stateRunner) commandEnv(base []string) []string {
	env := agentenv.WithPython3(agentenv.EnsurePATH(base))
	if r.rig == "" || r.townRoot == "" || !r.hooks.Env.BeadsDir {
		return env
	}
	beadsDir := config.ResolveBeadsDirForRig(r.townRoot, r.rig)
	env = withEnvKey(env, "BEADS_DIR", beadsDir)
	workDir := rigMayorRigDir(r.townRoot, r.rig)
	if !r.v.UsesPythonVenv() {
		return env
	}
	venvRel := r.v.PythonVenvRelDir()
	switch strings.ToLower(strings.TrimSpace(r.hooks.Env.PythonVenv)) {
	case "create":
		var created bool
		var venvErr error
		env, _, created, venvErr = agentenv.WithRigVenv(env, workDir, venvRel)
		if venvErr != nil {
			orchestratedFprintfStderr("[gt-agent] venv %s: %v (using host python)\n", venvRel, venvErr)
		} else if created {
			orchestratedPrintf("[gt-agent] created python venv at %s/%s\n", rigMayorRigPath(r.rig), venvRel)
			if err := rigpkg.EnsureGitignorePatterns(workDir); err != nil {
				orchestratedFprintfStderr("[gt-agent] gitignore: %v\n", err)
			}
		}
	case "activate":
		if r.hooks.Env.PythonPATH {
			env = prependEnvPath(env, "PYTHONPATH", workDir)
		}
		env = agentenv.ActivateRigVenvIfExists(env, workDir, venvRel)
	}
	return env
}

func (r *stateRunner) rewritePythonCmd(cmd string, cmdEnv []string) string {
	if !r.hooks.Python3Rewrite {
		return cmd
	}
	py := agentenv.ResolvePython3(cmdEnv)
	if fixed := agentenv.RewritePython3InCommand(cmd, py); fixed != cmd {
		orchestratedPrintf("[gt-agent] using python %s\n", py)
		return fixed
	}
	return cmd
}

func (r *stateRunner) workDir() string {
	return orchestratedCommandWorkDir(r.townRoot, r.rig, r.task.State)
}

func (r *stateRunner) afterCommand(cmd string, cmdErr error, workDir, sessionName string, cmdEnv []string, combined *strings.Builder) {
	if r.hooks.EmptyBdListOK && isScopedImplementBdListEmpty(cmd, cmdErr) {
		cmdErr = nil
		combined.WriteString("(no matching open/in_progress implement beads)\n")
	}
	if cmdErr == nil && writesRequirementsFile(cmd) {
		maybeRepairWorkflowRequirements(r.townRoot, r.rig, r.v)
	}
	r.trackCommand(cmd, cmdErr)
	if cmdErr == nil {
		r.runAutoVerify(cmd, workDir, sessionName, cmdEnv, combined)
	}
}

func (r *stateRunner) trackCommand(cmd string, cmdErr error) {
	switch r.hooks.Track {
	case "design":
		if cmdErr == nil && isArchitectureMDWriteCommand(cmd) {
			r.track.designArchWritten = true
		}
	case "planning":
		if cmdErr != nil {
			r.track.hadCmdFailure = true
		}
		if isBeadCreateCommand(cmd) && cmdErr == nil {
			r.track.beadCreateOK = true
		}
		if cmdErr == nil && isBeadDeleteCommand(cmd) {
			r.track.beadDeleteOK = true
		}
		if cmdErr == nil && isPlanMDWriteCommand(cmd) && planMDMeetsMinSize(r.townRoot, r.rig) {
			r.track.hadCmdFailure = false
		}
	case "implementation":
		if cmdErr != nil {
			r.track.hadCmdFailure = true
		}
		if isBeadCloseCommand(cmd) && cmdErr == nil {
			r.track.beadCloseOK = true
			if id := extractBeadIDFromBdClose(cmd); id != "" && id == r.track.activeBead {
				r.track.activeBead = ""
			}
		}
		if isBeadUpdateInProgressCommand(cmd) && cmdErr == nil {
			if id := extractBeadIDFromBdUpdate(cmd); id != "" {
				if r.track.activeBead != id {
					r.track.verifyOK = false
				}
				r.track.activeBead = id
			}
		}
		if cmdErr == nil && isQATestCommandOK(cmd, r.v) {
			r.track.verifyOK = true
			r.track.hadCmdFailure = false
		}
		if cmdErr == nil && isGitCommitLayoutCommand(cmd, r.v.LayoutRoot) {
			r.track.hadCmdFailure = false
		}
	case "project_setup":
		if cmdErr != nil {
			r.track.hadCmdFailure = true
		}
		if cmdErr == nil && isProjectSetupVerifyCommandOK(cmd, r.v) {
			r.track.verifyOK = true
			r.track.hadCmdFailure = false
		}
	case "plan_review":
		if cmdErr != nil {
			r.track.hadCmdFailure = true
		}
		if cmdErr == nil && isBdListOpenCommand(cmd) {
			r.track.listOpenOK = true
			r.track.hadCmdFailure = false
		}
		if cmdErr == nil && isQAReadOnlyCommand(cmd) {
			r.track.hadCmdFailure = false
		}
		if cmdErr == nil && isBeadDeleteCommand(cmd) {
			r.track.hadCmdFailure = false
			r.track.didDelete = true
		}
	case "qa":
		if cmdErr != nil {
			r.track.hadCmdFailure = true
		}
		if cmdErr == nil && isBdListClosedCommand(cmd) {
			r.track.bdListClosedOK = true
		}
		if cmdErr == nil && isQATestCommandOK(cmd, r.v) {
			r.track.unittestOK = true
			r.track.hadCmdFailure = false
		}
		if cmdErr == nil && isQAReadOnlyCommand(cmd) {
			r.track.hadCmdFailure = false
		}
	}
}

func (r *stateRunner) runAutoVerify(cmd, workDir, sessionName string, cmdEnv []string, combined *strings.Builder) {
	for _, hook := range r.hooks.AutoVerify {
		if !r.autoVerifyMatches(cmd, hook.When) {
			continue
		}
		verifyCmd := r.verifyCommand(hook.Verify)
		if verifyCmd == "" {
			continue
		}
		verifyOut, verifyErr := runOrchestratedCommand(verifyCmd, workDir, sessionName, cmdEnv)
		if verifyErr != nil {
			r.track.hadCmdFailure = true
			r.track.verifyOK = false
			orchestratedFprintfStderr("[gt-agent] auto-verify failed: %v\n%s\n", verifyErr, string(verifyOut))
			combined.WriteString(fmt.Sprintf("Auto-verify: %s\nError: %v\nOutput: %s\n\n", verifyCmd, verifyErr, string(verifyOut)))
		} else {
			r.track.verifyOK = true
			if r.hooks.Track == "project_setup" {
				r.track.hadCmdFailure = false
			}
			orchestratedPrintf("[gt-agent] auto-verify ok\n")
			combined.WriteString(string(verifyOut))
		}
	}
}

func (r *stateRunner) autoVerifyMatches(cmd, when string) bool {
	switch when {
	case "go_mod_tidy":
		return isGoModTidyCommand(cmd)
	case "go_mod_init":
		return isGoModInitCommand(cmd)
	case "pip_install":
		return isPipInstallRequirementsCommand(cmd)
	case "python_venv":
		return strings.Contains(strings.ToLower(cmd), "python3 -m venv")
	case "qa_test_ok":
		return isQATestCommandOK(cmd, r.v)
	case "go_write_layout":
		return orchestratedWritesGoUnderLayout(cmd, r.v)
	default:
		return false
	}
}

func (r *stateRunner) verifyCommand(kind string) string {
	switch kind {
	case "go_setup":
		if orchestrator.WorkflowUsesGo(r.v) {
			return orchestrator.GoProjectSetupVerifyCommand(r.v)
		}
	case "go_with_tidy":
		if orchestrator.WorkflowUsesGo(r.v) {
			return orchestrator.GoVerifyCommandWithTidy(r.v)
		}
	case "python":
		if orchestrator.WorkflowUsesPython(r.v) {
			return orchestrator.PythonVerifyCommand(r.v)
		}
	case "profile":
		return strings.TrimSpace(r.v.QAVerifyCommand)
	}
	return ""
}

func (r *stateRunner) validateArtifacts(outcome string) error {
	if outcome != "success" && outcome != "task_passed" && outcome != "all_passed" {
		return nil
	}
	t := r.track
	switch r.hooks.Artifacts {
	case "design":
		return validateDesignArtifacts(r.townRoot, r.rig, t.designArchWritten, r.v)
	case "planning":
		return validatePlanningArtifacts(r.townRoot, r.rig, t.hadCmdFailure, t.beadCreateOK, t.beadDeleteOK, r.v)
	case "plan_review":
		return validatePlanReviewArtifacts(r.townRoot, r.rig, t.hadCmdFailure, t.listOpenOK, t.didDelete, r.v)
	case "project_setup":
		return validateProjectSetupArtifacts(r.townRoot, r.rig, t.hadCmdFailure, t.verifyOK, r.v)
	case "implementation":
		return validateImplementationArtifacts(r.townRoot, r.rig, t.hadCmdFailure, t.beadCloseOK, t.verifyOK, r.v)
	case "qa":
		return validateQAArtifacts(r.townRoot, r.rig, outcome, t.hadCmdFailure, t.bdListClosedOK, t.unittestOK, r.v)
	default:
		return nil
	}
}

func (r *stateRunner) tryAutoOutcome() (outcome, summary string, ok bool) {
	switch r.hooks.Artifacts {
	case "design":
		if err := validateDesignArtifacts(r.townRoot, r.rig, r.track.designArchWritten, r.v); err != nil {
			return "", "", false
		}
	case "planning":
		if err := r.validateArtifacts("success"); err != nil {
			return "", "", false
		}
	default:
		return "", "", false
	}
	o := normalizeOrchestratedOutcome("success", r.task.AllowedOutcomes)
	if o == "" {
		return "", "", false
	}
	orchestratedPrintf("[gt-agent] auto-completing %s: artifacts satisfied\n", r.task.State)
	return o, "artifacts validated", true
}

func (r *stateRunner) retryHint() string {
	return r.hooks.RetryHintText(r.v, r.promptVars)
}

func (r *stateRunner) failureHint() string {
	if h := r.hooks.FailureHintText(r.v, r.promptVars); h != "" {
		return h
	}
	switch r.hooks.Artifacts {
	case "design":
		return "Write architecture.md with a heredoc CMD in this session (stale files from prior runs do not count). Read SPEC with head -n 60."
	case "planning":
		work := rigMayorRigPath(r.rig)
		return fmt.Sprintf("Repair beads: `bd delete %s --force` for duplicates, `bd create` only for missing paths, rewrite plan.md if needed — then JSON success. Work from %s with BEADS_DIR=$GT_ROOT/%s/.beads. No python/git/backend code.",
			beadIDExample(r.townRoot, r.rig), work, r.rig)
	case "plan_review":
		return fmt.Sprintf("Run `bd list --status=open` from %s with BEADS_DIR set; compare titles to architecture required_files. Use outcome failure to send the Planner back to fix duplicates or missing paths.", rigMayorRigPath(r.rig))
	case "implementation":
		return fmt.Sprintf("Use bare `bd` from %s with BEADS_DIR=$GT_ROOT/%s/.beads: fix files QA named, run %s, `bd close` a bead, then success. No shell text in .py files.",
			rigMayorRigPath(r.rig), r.rig, r.v.UnittestCommandHint())
	case "qa":
		return "Run real CMD: lines (not markdown fences): bd list --status=closed, head SPEC.md, " + r.v.UnittestCommandHint() + " from " + rigMayorRigPath(r.rig) + ". No /workspace paths. Then JSON only."
	default:
		return "Use CMD: with a heredoc to write files, then send JSON outcome."
	}
}

func validateOutcomeForTask(task *orchestrator.Task, townRoot, rig, outcome, summary string) error {
	if !task.Hooks.BeadIDsInSummary {
		return nil
	}
	return validateOutcomeSummaryBeadIDs(townRoot, rig, outcome, summary)
}

// validateOutcomeSummaryBeadIDs when hooks require it (plan_review, qa).
func validateOutcomeSummaryBeadIDs(townRoot, rig, outcome, summary string) error {
	if !isOrchestratedFailureOutcome(outcome) {
		return nil
	}
	known, prefix, err := orchestrator.ListRigBeadIDSet(townRoot, rig)
	if err != nil {
		return nil
	}
	return orchestrator.ValidateSummaryBeadIDs(summary, known, prefix)
}
