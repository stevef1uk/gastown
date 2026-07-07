package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

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
	qaSmokeOK        bool
	activeBead        string
	activeBeadPath    string
	lastVerifyOutput  string
	bdInfraFailed     bool
	noDiskRejects     int
	openBeadRejects   int
}

type stateRunner struct {
	task        *orchestrator.Task
	townRoot    string
	rig         string
	hooks       orchestrator.StateHooks
	v           orchestrator.WorkflowValidation
	promptVars  map[string]string
	track       *cmdTracker
	servers     *devServerTracker
	qaProgress     *QAReviewProgress         // qa_review only; persisted across gt-agent restarts
	implProgress   *ImplementationProgress   // implementation: per-bead verify/close across restarts
	attemptFixWork       bool // implementation: true after successful EDIT/WRITE, verify, or bd update/close this attempt
	attemptEditSearchMiss bool // implementation: EDIT failed SEARCH-not-found this attempt (auto-READ may have run)
	turnResponse           string // current LLM turn (for fenced-code vs native-tool guards)
	turnHadSuccessfulNative bool // true after a successful WRITE/EDIT this turn
}

func (r *stateRunner) qaReworkWriteScope() *orchestrator.ImplementWriteScope {
	if r == nil || r.task == nil || r.task.PendingRework == nil {
		return nil
	}
	pr := r.task.PendingRework
	sc := orchestrator.QAReworkWriteScopeFromTransition(r.townRoot, r.rig, pr.FromState, r.task.State,
		orchestrator.CombineQAReworkText(pr.Summary, pr.Feedback))
	if !sc.QAReworkFromQAReview {
		return nil
	}
	return &sc
}

func newStateRunner(task *orchestrator.Task, townRoot, rig string) *stateRunner {
	v := taskValidation(townRoot, task)
	vars := map[string]string{"rig": rig}
	for k, val := range v.PromptVars() {
		vars[k] = val
	}
	if townRoot != "" && rig != "" {
		vars["qa_runtime_smoke_block"] = orchestrator.RigFlowQARuntimeSmokeBlock(townRoot, rig, v)
	}
	return &stateRunner{
		task:       task,
		townRoot:   townRoot,
		rig:        rig,
		hooks:      taskHooks(task),
		v:          v,
		promptVars: vars,
		track:      &cmdTracker{},
		servers:    newDevServerTracker(),
	}
}

func (r *stateRunner) maxTurns() int {
	base := r.hooks.EffectiveMaxCmdTurns()
	if base <= 0 {
		base = 40
	}
	phase := r.v.ForActivePhase()
	if len(phase.RequiredFiles) > 0 {
		openCount := 0
		for _, f := range phase.RequiredFiles {
			if strings.Contains(f, "Implement ") || strings.Contains(f, "implement ") {
				openCount++
			}
		}
		if openCount > 0 {
			dynamic := base + openCount*5
			if dynamic > 500 {
				dynamic = 500
			}
			return dynamic
		}
	}
	return base
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
	if extra := r.implementationArtifactFailureExtra(err); extra != "" {
		msg += "\n\n" + extra
	}
	if h := r.failureHint(); h != "" {
		msg += ". " + h
	}
	for _, block := range r.failurePromptContextBlocks() {
		msg += "\n\n" + block
	}
	return msg
}

func (r *stateRunner) implementationArtifactFailureExtra(err error) string {
	if r.hooks.Artifacts != "implementation" || err == nil {
		return ""
	}
	em := err.Error()
	var b strings.Builder
	if strings.Contains(em, "failed commands") || strings.Contains(em, "placeholder") {
		b.WriteString("Do not use template placeholders (`<identified-bead-id>`, `BEAD_ID`, etc.). Copy bead IDs exactly from `bd list` output.\n")
	}
	if strings.Contains(em, "open implement bead(s) remain") {
		rigDir := rigMayorRigDir(r.townRoot, r.rig)
		if issues := orchestrator.AuditRequiredImplementFiles(rigDir, r.v.ForActivePhase()); len(issues) > 0 {
			b.WriteString("Missing or stubbed on disk (write files before `bd close`):\n")
			for _, issue := range issues {
				b.WriteString("- ")
				b.WriteString(issue)
				b.WriteString("\n")
			}
		}
		if next, nerr := orchestrator.NextOpenImplementBead(r.townRoot, r.rig, r.v); nerr == nil && next != nil && next.ID != "" {
			path := orchestrator.ImplementBeadPathForID(r.townRoot, r.rig, next.ID, r.v)
			b.WriteString(fmt.Sprintf("\n**Next bead:** %s (`%s`)\n", next.ID, path))
			b.WriteString(fmt.Sprintf("`CMD: export BEADS_DIR=$GT_ROOT/%s/.beads && cd %s/mayor/rig && bd update %s --status=in_progress`\n", r.rig, r.rig, next.ID))
			if r.implProgress != nil && r.implProgress.done(implVerifyKey(next.ID)) {
				b.WriteString(fmt.Sprintf("`CMD: export BEADS_DIR=$GT_ROOT/%s/.beads && cd %s/mayor/rig && bd close %s`\n", r.rig, r.rig, next.ID))
			}
		}
	}
	if strings.Contains(em, "runtime smoke") || strings.Contains(em, "compile or runtime smoke failed") {
		reopened, _ := orchestrator.ReopenImplementationBeadsAfterSmokeFailure(r.townRoot, r.rig, r.v, err)
		if block := orchestrator.FormatImplementationSmokeFailureBlock(r.townRoot, r.rig, r.v, err, reopened); block != "" {
			b.WriteString(block)
			b.WriteString("\n")
		}
	}
	next, nerr := orchestrator.NextOpenImplementBead(r.townRoot, r.rig, r.v)
	if nerr == nil && next == nil && (strings.Contains(em, "failed commands") || strings.Contains(em, "runtime smoke") || r.hasQAPendingRework()) {
		example := beadIDExample(r.townRoot, r.rig)
		b.WriteString("All implement beads are closed but work is not done. ")
		if r.hasQAPendingRework() {
			b.WriteString("Read **Prior step failed** / QA summary above. ")
		}
		b.WriteString("Run `bd list --status=closed`, pick the bead for the broken path (e.g. cmd/server or handlers), then:\n")
		b.WriteString("`bd update " + example + " --status=open` → EDIT:/WRITE: or CMD verify → `bd close " + example + "`.\n")
		b.WriteString("Do not send JSON success until verify is green.\n")
	}
	return strings.TrimSpace(b.String())
}

func (r *stateRunner) hasQAPendingRework() bool {
	return r.task != nil && r.task.PendingRework != nil && r.task.PendingRework.FromState == "qa_review"
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
	if orchestrator.WorkflowUsesPython(r.v) {
		msg += " This is a Python project — use .venv/bin/python3, pytest, pip, uvicorn. No go/cmd/build."
	}
	if orchestrator.WorkflowUsesGo(r.v) {
		msg += " This is a Go project — use go test, go build, go mod tidy."
	}
	return msg
}

func (r *stateRunner) runPerTurn() {
	for _, step := range r.hooks.PerTurn {
		if step == "repair_requirements" {
			maybeRepairWorkflowRequirements(r.townRoot, r.rig, r.v)
			continue
		}
		logLine, err := orchestrator.RunPreRunHook(step, r.townRoot, r.rig, r.v)
		if err != nil {
			orchestratedFprintfStderr("[gt-agent] per_turn %s: %v\n", step, err)
			continue
		}
		if logLine != "" {
			orchestratedPrintf("[gt-agent] %s: %s\n", step, logLine)
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
	if stripped := stripOrchestratedShellBackticks(cmd); stripped != cmd {
		orchestratedPrintf("[gt-agent] stripped markdown backticks from command → %s\n", stripped)
		cmd = stripped
	}
	if fixed, ok := sanitizeOrchestratedShellCommand(cmd); ok {
		orchestratedPrintf("[gt-agent] trimmed glued prose/JSON from command → %s\n", fixed)
		cmd = fixed
	}
	if fixed, ok := normalizeGoCommandTypos(cmd); ok {
		orchestratedPrintf("[gt-agent] rewrote go command typo → %s\n", fixed)
		cmd = fixed
	}
	// Runtime smoke rewrite is for implementation/QA only — planner must not go run the server.
	if r.hooks.CmdGuard != "planning" && r.hooks.CmdGuard != "plan_review" && r.hooks.CmdGuard != "design" {
		if fixed, ok := normalizeGoDevServerSmokeCommand(cmd, r.townRoot, r.rig, r.v); ok {
			orchestratedPrintf("[gt-agent] rewrote dev-server smoke → %s\n", fixed)
			cmd = fixed
		}
	}
	if r.hooks.CmdGuard == "planning" {
		if fixed, ok := rewritePlanMDWCFromTownRoot(cmd, r.rig); ok {
			orchestratedPrintf("[gt-agent] rewrote plan.md wc path for rig dir: %s\n", fixed)
			cmd = fixed
		}
	}
	if r.hooks.CmdGuard == "qa" {
		if fixed, ok := rewriteQALayoutVerifyCommand(cmd, r.rig, r.v); ok {
			orchestratedPrintf("[gt-agent] rewrote QA layout verify cmd: %s\n", fixed)
			cmd = fixed
		}
		if fixed, ok := rewriteUnittestToWorkdir(cmd, r.rig, r.v); ok {
			orchestratedPrintf("[gt-agent] rewrote QA toolchain cmd for mayor/rig workdir: %s\n", fixed)
			cmd = fixed
		}
		if fixed, ok := rewriteQAMayorRigPrefix(cmd, r.rig); ok {
			orchestratedPrintf("[gt-agent] rewrote QA cmd for mayor/rig workdir: %s\n", fixed)
			cmd = fixed
		}
	}
	if orchestrator.WorkflowUsesDocker(r.v) {
		if fixed := orchestrator.NormalizeDockerCommand(cmd); fixed != cmd {
			orchestratedPrintf("[gt-agent] rewrote docker command typo → %s\n", fixed)
			cmd = fixed
		}
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
		case "plan_md_wc_dir":
			if fixed, ok := rewritePlanMDWCFromTownRoot(cmd, r.rig); ok {
				orchestratedPrintf("[gt-agent] rewrote plan.md wc path for rig dir: %s\n", fixed)
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

func (r *stateRunner) autoInstallDepsAndRetry(verifyCmd, workDir, sessionName string, cmdEnv []string, combined *strings.Builder) ([]byte, error) {
	rigDir := rigMayorRigDir(r.townRoot, r.rig)
	reqRel := r.v.RequirementsFilePath()
	reqPath := filepath.Join(rigDir, reqRel)
	if reqRel == "" {
		out, err := r.runShellCommand(verifyCmd, workDir, sessionName, cmdEnv)
		return out, err
	}
	if _, err := os.Stat(reqPath); err != nil {
		orchestratedFprintfStderr("[gt-agent] cannot auto-install deps: %s missing: %v\n", reqRel, err)
		combined.WriteString(fmt.Sprintf("Auto-install deps skipped (%s missing)\n", reqRel))
		out, err := r.runShellCommand(verifyCmd, workDir, sessionName, cmdEnv)
		return out, err
	}
	venvPip := filepath.Join(rigDir, r.v.PythonVenvRelDir(), "bin", "pip")
	pipCmd := venvPip + " install -r " + reqPath
	orchestratedPrintf("[gt-agent] auto-installing deps: %s\n", pipCmd)
	installOut, installErr := r.runShellCommand(pipCmd, rigDir, sessionName, cmdEnv)
	if installErr != nil {
		orchestratedFprintfStderr("[gt-agent] auto-install deps failed: %v\n%s\n", installErr, string(installOut))
		combined.WriteString(fmt.Sprintf("Auto-install deps failed: %v\n%s\n", installErr, string(installOut)))
	}
	out, err := r.runShellCommand(verifyCmd, workDir, sessionName, cmdEnv)
	return out, err
}

func (r *stateRunner) goModTidyAndRetry(verifyCmd, workDir, sessionName string, cmdEnv []string, combined *strings.Builder) ([]byte, error) {
	rigDir := rigMayorRigDir(r.townRoot, r.rig)
	layout := strings.Trim(strings.TrimSpace(r.v.LayoutRoot), "/")
	tidyDir := rigDir
	goModDir := rigDir
	if layout != "" && layout != "." {
		goModDir = filepath.Join(rigDir, layout)
	}
	if _, err := os.Stat(filepath.Join(goModDir, "go.mod")); err != nil {
		out, err := r.runShellCommand(verifyCmd, workDir, sessionName, cmdEnv)
		return out, err
	}
	tidyCmd := "cd " + goModDir + " && go mod tidy"
	orchestratedPrintf("[gt-agent] auto-running go mod tidy: %s\n", tidyCmd)
	tidyOut, tidyErr := r.runShellCommand(tidyCmd, tidyDir, sessionName, cmdEnv)
	if tidyErr != nil {
		orchestratedFprintfStderr("[gt-agent] go mod tidy failed: %v\n%s\n", tidyErr, string(tidyOut))
		combined.WriteString(fmt.Sprintf("go mod tidy failed: %v\n%s\n", tidyErr, string(tidyOut)))
	}
	out, err := r.runShellCommand(verifyCmd, workDir, sessionName, cmdEnv)
	return out, err
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
	if r.rig == "" || r.townRoot == "" {
		return env
	}
	workDir := rigMayorRigDir(r.townRoot, r.rig)
	if r.hooks.Env.BeadsDir {
		beadsDir := config.ResolveBeadsDirForRig(r.townRoot, r.rig)
		env = withEnvKey(env, "BEADS_DIR", beadsDir)
	}
	if r.v.UsesPythonVenv() {
		env = r.applyPythonVenvEnv(env, workDir)
	}
	return env
}

func (r *stateRunner) applyPythonVenvEnv(env []string, workDir string) []string {
	venvRel := r.v.PythonVenvRelDir()
	switch strings.ToLower(strings.TrimSpace(r.hooks.Env.PythonVenv)) {
	case "create":
		return r.ensureRigVenvEnv(env, workDir, venvRel)
	case "activate":
		if r.hooks.Env.PythonPATH {
			env = prependEnvPath(env, "PYTHONPATH", workDir)
		}
		venvPy := agentenv.VenvPython(workDir, venvRel)
		if !isExecutablePath(venvPy) {
			orchestratedFprintfStderr("[gt-agent] venv %s missing at %s — creating (project_setup may have been skipped)\n", venvRel, rigMayorRigPath(r.rig))
			return r.ensureRigVenvEnv(env, workDir, venvRel)
		}
		return agentenv.ActivateRigVenvIfExists(env, workDir, venvRel)
	default:
		return env
	}
}

func (r *stateRunner) ensureRigVenvEnv(env []string, workDir, venvRel string) []string {
	env, _, created, venvErr := agentenv.WithRigVenv(env, workDir, venvRel)
	if venvErr != nil {
		orchestratedFprintfStderr("[gt-agent] venv %s: %v (using host python)\n", venvRel, venvErr)
		return env
	}
	if created {
		orchestratedPrintf("[gt-agent] created python venv at %s/%s\n", rigMayorRigPath(r.rig), venvRel)
		if err := rigpkg.EnsureGitignorePatterns(workDir); err != nil {
			orchestratedFprintfStderr("[gt-agent] gitignore: %v\n", err)
		}
	}
	return env
}

func isExecutablePath(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
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

// effectiveCommandTimeoutSec returns per-CMD wall clock: yaml cmd_timeout_seconds, else track defaults.
func (r *stateRunner) effectiveCommandTimeoutSec(cmd string) int {
	if sec := r.hooks.EffectiveCmdTimeoutSeconds(); sec > 0 {
		return sec
	}
	if d := orchestratedCommandTimeoutForTrack(r.hooks.Track, cmd); d > 0 {
		return int(d / time.Second)
	}
	return 0
}

func (r *stateRunner) runShellCommand(cmd, workDir, sessionName string, env []string) ([]byte, error) {
	return runOrchestratedCommand(cmd, workDir, sessionName, env, r.effectiveCommandTimeoutSec(cmd))
}

func (r *stateRunner) repairGoModRequiresAfterTidy(combined *strings.Builder) {
	if r == nil {
		return
	}
	rigDir := rigMayorRigDir(r.townRoot, r.rig)
	logLine, err := orchestrator.RepairGoModRequiresFromSpec(rigDir, r.v)
	if err != nil {
		orchestratedFprintfStderr("[gt-agent] repair go.mod requires: %v\n", err)
		if combined != nil {
			combined.WriteString("go.mod repair failed: " + err.Error() + "\n")
		}
		return
	}
	if logLine != "" {
		orchestratedPrintf("[gt-agent] %s\n", logLine)
		if combined != nil {
			combined.WriteString(logLine + "\n")
		}
	}
}

func isExit143(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == 143
}

func (r *stateRunner) afterCommand(cmd string, cmdErr error, workDir, sessionName string, cmdEnv []string, combined *strings.Builder) {
	if trackNeedsDevServerCleanup(r.hooks.Track) {
		r.servers.noteCommand(cmd)
	}
	if r.hooks.EmptyBdListOK && isScopedImplementBdListEmpty(cmd, cmdErr) {
		cmdErr = nil
		combined.WriteString("(no matching open/in_progress implement beads)\n")
	}
	// Dev-server smoke with kill/wait returns 143 (SIGTERM) — not a real failure.
	if cmdErr != nil && commandStartsDevServer(cmd) && isExit143(cmdErr) {
		cmdErr = nil
	}
	if cmdErr == nil && writesRequirementsFile(cmd) {
		maybeRepairWorkflowRequirements(r.townRoot, r.rig, r.v)
	}
	r.trackCommand(cmd, cmdErr)
	if cmdErr == nil {
		if strings.EqualFold(strings.TrimSpace(r.hooks.Track), "qa") {
			r.persistQAReviewProgress(cmd)
		}
		if strings.EqualFold(strings.TrimSpace(r.hooks.Track), "implementation") {
			r.persistImplementationProgress(cmd)
		}
		r.noteImplementationFixAttempt(cmd, false)
		appendBdListImplementationHintIfNeeded(r, cmd, combined.String(), combined)
		if isGoModTidyCommand(cmd) && orchestrator.WorkflowUsesGo(r.v) {
			r.repairGoModRequiresAfterTidy(combined)
		}
		r.runAutoVerify(cmd, workDir, sessionName, cmdEnv, combined)
		return
	}
	if strings.EqualFold(strings.TrimSpace(r.hooks.Track), "qa") && qaCommandFailureNeedsCleanup(cmd) {
		shutdownStartedDevServers(r.servers)
	}
}

func (r *stateRunner) trackCommand(cmd string, cmdErr error) {
	if fn, ok := trackHandlers[r.hooks.Track]; ok {
		fn(r, cmd, cmdErr)
	}
}

func (r *stateRunner) scrubStaleDevServersAtTaskStart() {
	if !trackNeedsDevServerCleanup(r.hooks.Track) {
		return
	}
	scrubStaleDevServersAtTaskStart(r.v, rigMayorRigDir(r.townRoot, r.rig))
}

func (r *stateRunner) beforeDevServerCommand(cmd string) {
	if !trackNeedsDevServerCleanup(r.hooks.Track) {
		return
	}
	freeDevServersBeforeCommand(cmd)
}

func (r *stateRunner) shutdownStartedServers() {
	if !trackNeedsDevServerCleanup(r.hooks.Track) {
		return
	}
	shutdownStartedDevServers(r.servers)
}

// goAutoVerifyNoPackagesIsError reports whether "matched no packages" output should fail auto-verify.
// project_setup / go_setup / go mod tidy only scaffold go.mod — no .go files exist yet.
func goAutoVerifyNoPackagesIsError(verifyKind, state, verifyCmd string) bool {
	if verifyKind == "go_setup" || state == "project_setup" {
		return false
	}
	if orchestrator.GoModScaffoldOnlyCommand(verifyCmd) {
		return false
	}
	return true
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
		verifyOut, verifyErr := r.runShellCommand(verifyCmd, workDir, sessionName, cmdEnv)
		if verifyErr != nil {
			// Python pytest "no tests ran" (exit 4/5) is not a code failure —
			// the test file simply doesn't exist yet (test bead not implemented).
			if orchestrator.WorkflowUsesPython(r.v) && orchestrator.PythonVerifyNoTestsOK(string(verifyOut)) {
				r.track.verifyOK = true
				r.track.lastVerifyOutput = string(verifyOut)
			} else if orchestrator.WorkflowUsesPython(r.v) && orchestrator.PythonTestsAllPassed(string(verifyOut)) {
				r.track.verifyOK = true
				if r.hooks.AutoVerifyOKClearsCmdFailure {
					r.track.hadCmdFailure = false
				}
				r.track.lastVerifyOutput = string(verifyOut)
				orchestratedPrintf("[gt-agent] auto-verify ok (all tests passed despite exit code): %s\n", verifyCmd)
			} else if orchestrator.WorkflowUsesPython(r.v) && pythonVerifyOutputSuggestsMissingDeps(string(verifyOut)) {
				// Auto-install from requirements.txt and retry once.
				verifyOut, verifyErr = r.autoInstallDepsAndRetry(verifyCmd, workDir, sessionName, cmdEnv, combined)
				if verifyErr == nil {
					r.track.verifyOK = true
					if r.hooks.AutoVerifyOKClearsCmdFailure {
						r.track.hadCmdFailure = false
					}
					r.track.lastVerifyOutput = string(verifyOut)
					orchestratedPrintf("[gt-agent] auto-verify ok (after pip install): %s\n", verifyCmd)
				} else if orchestrator.PythonTestsAllPassed(string(verifyOut)) {
					r.track.verifyOK = true
					if r.hooks.AutoVerifyOKClearsCmdFailure {
						r.track.hadCmdFailure = false
					}
					r.track.lastVerifyOutput = string(verifyOut)
					orchestratedPrintf("[gt-agent] auto-verify ok (after pip install, all tests passed): %s\n", verifyCmd)
				} else {
					r.track.hadCmdFailure = true
					r.track.verifyOK = false
					r.track.lastVerifyOutput = string(verifyOut)
					orchestratedFprintfStderr("[gt-agent] auto-verify failed (after pip install): %v\n%s\n", verifyErr, string(verifyOut))
					combined.WriteString(fmt.Sprintf("Auto-verify (after pip install): %s\nError: %v\nOutput: %s\n\n", verifyCmd, verifyErr, string(verifyOut)))
				}
			} else if hook.Verify == "go_setup" &&
				strings.EqualFold(strings.TrimSpace(r.task.State), "project_setup") &&
				orchestrator.GoToolOutputMatchedNoPackages(string(verifyOut)) {
				// Go module scaffold (go mod download) fails on empty go.mod —
				// no packages exist yet (polecat writes them in implementation).
				r.track.verifyOK = true
				r.track.lastVerifyOutput = string(verifyOut)
			} else if orchestrator.WorkflowUsesGo(r.v) && orchestrator.GoToolOutputMissingDeps(string(verifyOut)) &&
				hook.Verify != "go_setup" && !goAutoVerifyNoPackagesIsError(hook.Verify, r.task.State, verifyCmd) {
				// Go verify failed due to missing modules — run go mod tidy and retry once.
				verifyOut, verifyErr = r.goModTidyAndRetry(verifyCmd, workDir, sessionName, cmdEnv, combined)
				if verifyErr == nil {
					r.track.verifyOK = true
					if r.hooks.AutoVerifyOKClearsCmdFailure {
						r.track.hadCmdFailure = false
					}
					r.track.lastVerifyOutput = string(verifyOut)
					orchestratedPrintf("[gt-agent] auto-verify ok (after go mod tidy): %s\n", verifyCmd)
				} else {
					r.track.hadCmdFailure = true
					r.track.verifyOK = false
					r.track.lastVerifyOutput = string(verifyOut)
					orchestratedFprintfStderr("[gt-agent] auto-verify failed (after go mod tidy): %v\n%s\n", verifyErr, string(verifyOut))
					combined.WriteString(fmt.Sprintf("Auto-verify (after go mod tidy): %s\nError: %v\nOutput: %s\n\n", verifyCmd, verifyErr, string(verifyOut)))
				}
			} else {
				r.track.hadCmdFailure = true
				r.track.verifyOK = false
				r.track.lastVerifyOutput = string(verifyOut)
				orchestratedFprintfStderr("[gt-agent] auto-verify failed: %v\n%s\n", verifyErr, string(verifyOut))
				combined.WriteString(fmt.Sprintf("Auto-verify: %s\nError: %v\nOutput: %s\n\n", verifyCmd, verifyErr, string(verifyOut)))
				if r.hooks.AppendGoCompileContext && orchestrator.WorkflowUsesGo(r.v) {
					out := string(verifyOut)
					appendGoCompileSourceContext(combined, r.townRoot, r.rig, rigMayorRigDir(r.townRoot, r.rig), r.v.LayoutRoot,
						r.activeImplementBeadPath(), r.v, verifyCmd, out)
					r.noteImplementationVerifyFailure(verifyCmd, out)
				}
				if strings.EqualFold(strings.TrimSpace(r.hooks.Track), "qa") {
					appendQAFailureReportNudge(combined, verifyCmd, verifyErr)
				}
			}
		} else if orchestrator.GoToolOutputMatchedNoPackages(string(verifyOut)) &&
			goAutoVerifyNoPackagesIsError(hook.Verify, r.task.State, verifyCmd) {
			// project_setup leaves only go.mod/go.sum (no .go yet); go mod tidy warns but exits 0.
			r.track.hadCmdFailure = true
			r.track.verifyOK = false
			r.track.lastVerifyOutput = string(verifyOut)
			errNoPkg := fmt.Errorf("go matched no packages (no .go sources in target path)")
			orchestratedFprintfStderr("[gt-agent] auto-verify failed: %v\n%s\n", errNoPkg, string(verifyOut))
			combined.WriteString(fmt.Sprintf("Auto-verify: %s\nError: %v\nOutput: %s\n\n", verifyCmd, errNoPkg, string(verifyOut)))
		} else {
			r.track.verifyOK = true
			if r.hooks.AutoVerifyOKClearsCmdFailure {
				r.track.hadCmdFailure = false
			}
			if hook.When == "go_mod_tidy" && orchestrator.WorkflowUsesGo(r.v) {
				r.repairGoModRequiresAfterTidy(combined)
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
	if r.track != nil && r.track.bdInfraFailed && isOrchestratedSuccessOutcome(outcome) {
		return fmt.Errorf("bd/Dolt unavailable for rig %s — fix beads (bd doctor, bd bootstrap, ensure Dolt serves this rig) before JSON success; do not claim tests passed or beads closed",
			r.rig)
	}
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
	if task != nil && task.State == "qa_review" && isOrchestratedFailureOutcome(outcome) {
		lower := strings.ToLower(strings.TrimSpace(summary))
		if strings.Contains(lower, "syntax error") || strings.Contains(lower, "syntaxerror") ||
			strings.Contains(lower, "command not found") || strings.Contains(lower, "no such file") &&
			strings.Contains(lower, "bd list") {
			return fmt.Errorf("QA failure rejected — summary blames a shell syntax error, not a code quality issue. Retry the command with correct shell syntax and re-evaluate")
		}
	}
	if !task.Hooks.BeadIDsInSummary {
		return nil
	}
	return validateOutcomeSummaryBeadIDs(townRoot, rig, outcome, summary)
}

// cacheValidatedContent stores a successfully-verified file in the code cache
// so subsequent retries can reuse it instead of regenerating from scratch.
func (r *stateRunner) cacheValidatedContent(relPath string) {
	if r == nil || r.task == nil || r.task.WorkflowID == "" || relPath == "" {
		orchestratedPrintf("[gt-agent] cache skip: nil runner=%v task=%v wf=%q path=%q\n", r == nil, r.task == nil, r.task.WorkflowID, relPath)
		return
	}
	rigDir := rigMayorRigDir(r.townRoot, r.rig)
	cache, err := orchestrator.OpenCodeCache(rigDir, r.task.WorkflowID)
	if err != nil {
		orchestratedPrintf("[gt-agent] cache open error: %v (rigDir=%s, wf=%s)\n", err, rigDir, r.task.WorkflowID)
		return
	}
	abs := filepath.Join(rigDir, filepath.FromSlash(relPath))
	data, err := os.ReadFile(abs)
	if err != nil {
		orchestratedPrintf("[gt-agent] cache read error: %v (abs=%s)\n", err, abs)
		return
	}
	phaseIdx := r.v.ActivePhaseIndex()
	cache.Put(phaseIdx, relPath, string(data))
	cache.MarkValidated(phaseIdx, relPath)
	orchestratedPrintf("[gt-agent] cached validated: %s (phase %d)\n", relPath, phaseIdx)
}

// formatCachedContentBlock returns a prompt hint block listing validated cached
// files for the active phase, so the LLM reuses them instead of regenerating.
func (r *stateRunner) formatCachedContentBlock() string {
	if r == nil || r.task == nil || r.task.WorkflowID == "" {
		return ""
	}
	phaseIdx := r.v.ActivePhaseIndex()
	rigDir := rigMayorRigDir(r.townRoot, r.rig)
	cache, err := orchestrator.OpenCodeCache(rigDir, r.task.WorkflowID)
	if err != nil {
		return ""
	}
	scoped := r.v.ForActivePhase()
	var hints []string
	for _, p := range scoped.RequiredFiles {
		if _, ok := cache.GetValidated(phaseIdx, p); ok {
			hints = append(hints, "  - "+p+" (validated \u2014 reuse existing content)")
		}
	}
	if len(hints) == 0 {
		return ""
	}
	return "Cached validated content available:\n" + strings.Join(hints, "\n")
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
