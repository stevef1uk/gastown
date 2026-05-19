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
		if step == "repair_requirements" {
			maybeRepairWorkflowRequirements(r.townRoot, r.rig, r.v)
			continue
		}
		logLine, err := orchestrator.RunPreRunHook(step, r.townRoot, r.rig, r.v)
		if err != nil {
			orchestratedFprintfStderr("[gt-agent] pre_run %s: %v\n", step, err)
			continue
		}
		if logLine != "" {
			orchestratedPrintf("[gt-agent] %s: %s\n", step, logLine)
		}
	}
}

func (r *stateRunner) promptContextBlocks() []string {
	return orchestrator.PromptContextBlocks(r.hooks.PromptContext, r.townRoot, r.rig, r.v)
}

func (r *stateRunner) failurePromptContextBlocks() []string {
	keys := r.hooks.FailurePromptContext
	if len(keys) == 0 {
		return r.promptContextBlocks()
	}
	return orchestrator.PromptContextBlocks(keys, r.townRoot, r.rig, r.v)
}

func (r *stateRunner) artifactFailureFeedback(err error) string {
	msg := "Validation failed: " + err.Error()
	if h := r.failureHint(); h != "" {
		msg += ". " + h
	}
	for _, block := range r.failurePromptContextBlocks() {
		msg += "\n\n" + block
	}
	return msg
}

func (r *stateRunner) emptyResponseNudge() string {
	msg := "Empty reply."
	for _, block := range r.failurePromptContextBlocks() {
		msg += " " + block
	}
	if suffix := orchestrator.SubstituteVars(strings.TrimSpace(r.hooks.EmptyResponseSuffix), r.promptVars); suffix != "" {
		msg += " " + suffix
	} else if h := r.failureHint(); h != "" {
		msg += " " + h
	} else {
		msg += " Send CMD: lines only (no blank turns)."
	}
	return msg
}

func (r *stateRunner) runPerTurn() {
	for _, step := range r.hooks.PerTurn {
		switch step {
		case "repair_requirements":
			maybeRepairWorkflowRequirements(r.townRoot, r.rig, r.v)
		}
	}
}

func validateImplementationBeadOrder(townRoot, rig, cmd string, v orchestrator.WorkflowValidation) error {
	if len(v.RequiredFiles) == 0 {
		return nil
	}
	if !isBeadUpdateInProgressCommand(cmd) && !isBeadCloseCommand(cmd) {
		return nil
	}
	next, err := orchestrator.NextOpenImplementBead(townRoot, rig, v)
	if err != nil || next == nil || next.ID == "" {
		return nil
	}
	var id string
	if isBeadUpdateInProgressCommand(cmd) {
		id = extractBeadIDFromBdUpdate(cmd)
	} else {
		id = extractBeadIDFromBdClose(cmd)
	}
	if id == "" || id == next.ID {
		return nil
	}
	return fmt.Errorf("implement beads in profile order — next open bead is %s (%s); finish or bd close it before starting %s", next.ID, next.Title, id)
}

func (r *stateRunner) rejectScope() string {
	if s, ok := cmdGuardRejectScope[r.hooks.CmdGuard]; ok {
		return s
	}
	return "command scope"
}

func (r *stateRunner) validateCommand(cmd string) error {
	if fn, ok := cmdGuardHandlers[r.hooks.CmdGuard]; ok {
		return fn(r, cmd)
	}
	return nil
}

func (r *stateRunner) rewriteCommand(cmd string) string {
	if fixed, ok := normalizeGoCommandTypos(cmd); ok {
		orchestratedPrintf("[gt-agent] rewrote go command typo → %s\n", fixed)
		cmd = fixed
	}
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
		case "unwrap_bash_lc":
			if unwrapped := agentenv.UnwrapBashLcSingleLine(cmd); unwrapped != cmd {
				orchestratedPrintf("[gt-agent] unwrapped bash -lc → %s\n", unwrapped)
				cmd = unwrapped
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
	if !r.hooks.Python3Rewrite || orchestrator.IsPythonImportCheckCommand(cmd) {
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
	return orchestratedCommandWorkDir(r.townRoot, r.rig, "")
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
	if fn, ok := trackHandlers[r.hooks.Track]; ok {
		fn(r, cmd, cmdErr)
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
		if hook.Verify != "python_setup" {
			if fixed, ok := rewriteUnittestToWorkdir(verifyCmd, r.rig, r.v); ok {
				verifyCmd = fixed
			}
		} else if fixed, ok := rewriteOrchestratedRigPlaceholders(verifyCmd, r.townRoot, r.rig); ok {
			verifyCmd = fixed
		}
		verifyOut, verifyErr := runOrchestratedCommand(verifyCmd, workDir, sessionName, cmdEnv)
		if verifyErr != nil {
			r.track.hadCmdFailure = true
			r.track.verifyOK = false
			orchestratedFprintfStderr("[gt-agent] auto-verify failed: %v\n%s\n", verifyErr, string(verifyOut))
			combined.WriteString(fmt.Sprintf("Auto-verify: %s\nError: %v\nOutput: %s\n\n", verifyCmd, verifyErr, string(verifyOut)))
			if r.hooks.AppendGoCompileContext && orchestrator.WorkflowUsesGo(r.v) {
				appendGoCompileSourceContext(combined, rigMayorRigDir(r.townRoot, r.rig), r.v.LayoutRoot, verifyCmd, string(verifyOut))
			}
		} else {
			r.track.verifyOK = true
			if r.hooks.AutoVerifyOKClearsCmdFailure {
				r.track.hadCmdFailure = false
			}
			orchestratedPrintf("[gt-agent] auto-verify ok: %s\n", verifyCmd)
			combined.WriteString(fmt.Sprintf("Auto-verify: %s\n%s", verifyCmd, formatSuccessCommandOutput(verifyOut)))
		}
	}
}

func (r *stateRunner) autoVerifyMatches(cmd, when string) bool {
	if fn, ok := autoVerifyWhenHandlers[when]; ok {
		return fn(r, cmd)
	}
	return false
}

func (r *stateRunner) verifyCommand(kind string) string {
	if fn, ok := verifyKindHandlers[kind]; ok {
		return fn(r)
	}
	return ""
}

func (r *stateRunner) validateArtifacts(outcome string) error {
	if outcome != "success" && outcome != "task_passed" && outcome != "all_passed" {
		return nil
	}
	fn, ok := artifactValidators[r.hooks.Artifacts]
	if !ok {
		return nil
	}
	if err := fn(r, outcome); err != nil {
		return err
	}
	return r.runPostArtifactSuccess()
}

func (r *stateRunner) runPostArtifactSuccess() error {
	for _, step := range r.hooks.PostArtifactSuccess {
		logLine, err := orchestrator.RunPreRunHook(step, r.townRoot, r.rig, r.v)
		if err != nil {
			orchestratedFprintfStderr("[gt-agent] post_artifact_success %s: %v\n", step, err)
			return err
		}
		if logLine != "" {
			orchestratedPrintf("[gt-agent] %s: %s\n", step, logLine)
		}
	}
	return nil
}

func (r *stateRunner) tryAutoOutcome() (outcome, summary string, ok bool) {
	fn, ok := artifactAutoCompleters[r.hooks.Artifacts]
	if !ok {
		return "", "", false
	}
	if err := fn(r); err != nil {
		return "", "", false
	}
	o := normalizeOrchestratedOutcome("success", r.task.AllowedOutcomes)
	if o == "" {
		return "", "", false
	}
	orchestratedPrintf("[gt-agent] auto-completing %s: artifacts satisfied\n", r.hooks.Artifacts)
	return o, "artifacts validated", true
}

func (r *stateRunner) retryHint() string {
	return r.hooks.RetryHintText(r.v, r.promptVars)
}

func (r *stateRunner) failureHint() string {
	if h := r.hooks.FailureHintText(r.v, r.promptVars); h != "" {
		return h
	}
	if fn, ok := artifactFailureHints[r.hooks.Artifacts]; ok {
		return fn(r)
	}
	return "Use CMD: with a heredoc to write files, then send JSON outcome."
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
