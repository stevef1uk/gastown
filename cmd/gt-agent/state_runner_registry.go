package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

// Rig-flow hook registries: keys match hooks.cmd_guard / hooks.track / hooks.artifacts
// in templates/rig-flow.yaml — not task.State switches in state_runner.go.

type cmdGuardFn func(r *stateRunner, cmd string) error
type trackFn func(r *stateRunner, cmd string, cmdErr error)
type artifactValidateFn func(r *stateRunner, outcome string) error
type artifactAutoCompleteFn func(r *stateRunner) error

var cmdGuardHandlers = map[string]cmdGuardFn{
	"analysis": func(r *stateRunner, cmd string) error {
		return validateAnalysisCommand(cmd, r.rig)
	},
	"spec_review": func(r *stateRunner, cmd string) error {
		return validateSpecReviewCommand(cmd, r.rig)
	},
	"design": func(r *stateRunner, cmd string) error {
		return validateDesignCommand(cmd, r.rig)
	},
	"design_review": func(r *stateRunner, cmd string) error {
		return validateQACommand(cmd, r.rig, r.townRoot, r.v)
	},
	"planning": func(r *stateRunner, cmd string) error {
		return validatePlanningCommandWithProfile(cmd, r.townRoot, r.rig, r.v)
	},
	"project_setup": func(r *stateRunner, cmd string) error {
		return validateProjectSetupCommand(cmd, r.rig, r.v)
	},
	"implementation": func(r *stateRunner, cmd string) error {
		if err := rejectInventedBdVerifyCommand(cmd, r.townRoot, r.rig, r.track.activeBead, r.v); err != nil {
			return err
		}
		if err := validateImplementationCommandWithState(cmd, r.townRoot, r.rig, r.effectiveImplementBeadID(), r.v, r.track.verifyOK, r.qaReworkWriteScope(), r.track.lastVerifyOutput); err != nil {
			return err
		}
		if err := r.validateImplementationFencedCodeGuard(cmd); err != nil {
			return err
		}
		if err := r.validateImplementationMissingFileRead(cmd); err != nil {
			return err
		}
		if r.hooks.NativeEditTools {
			lower := strings.ToLower(cmd)
			if strings.Contains(lower, "cat >") || strings.Contains(lower, "cat>>") || strings.Contains(lower, "<<") {
				return fmt.Errorf("do not use shell heredocs (cat >) when NativeEditTools is enabled — use WRITE: and EDIT: tags instead")
			}
		}
		return validateImplementationBeadOrder(r.townRoot, r.rig, cmd, r.v)
	},
	"plan_review": func(r *stateRunner, cmd string) error {
		return validatePlanReviewCommand(cmd, r.rig)
	},
	"qa": func(r *stateRunner, cmd string) error {
		return validateQACommand(cmd, r.rig, r.townRoot, r.v)
	},
}

var cmdGuardRejectScope = map[string]string{
	"analysis":       "analyst scope",
	"spec_review":    "spec review scope",
	"design":         "architect scope",
	"planning":       "planner scope",
	"project_setup":  "project setup scope",
	"implementation": "polecat scope",
	"plan_review":    "plan review scope",
	"design_review":  "QA scope",
	"qa":             "QA scope",
}

var trackHandlers = map[string]trackFn{
	"analysis": func(r *stateRunner, cmd string, cmdErr error) {
		if cmdErr != nil {
			r.track.hadCmdFailure = true
		}
	},
	"spec_review": func(r *stateRunner, cmd string, cmdErr error) {
		if cmdErr != nil {
			r.track.hadCmdFailure = true
		}
	},
	"design": func(r *stateRunner, cmd string, cmdErr error) {
		if cmdErr == nil && isArchitectureMDWriteCommand(cmd) {
			r.track.designArchWritten = true
		}
	},
	"planning": func(r *stateRunner, cmd string, cmdErr error) {
		if cmdErr != nil && !benignPlanningShellNoise(cmd, cmdErr) {
			r.track.hadCmdFailure = true
		}
		if isBeadCreateCommand(cmd) && cmdErr == nil {
			r.track.beadCreateOK = true
		}
		if cmdErr == nil && isBeadDeleteCommand(cmd) {
			r.track.beadDeleteOK = true
		}
		if cmdErr == nil && isPlanMDWriteCommand(cmd) && planMDMeetsMinSize(r.townRoot, r.rig, r.v) {
			r.track.hadCmdFailure = false
		}
		if cmdErr == nil && isPlanMDSizeCheckCommand(cmd) && planMDMeetsMinSize(r.townRoot, r.rig, r.v) {
			r.track.hadCmdFailure = false
		}
	},
	"implementation": func(r *stateRunner, cmd string, cmdErr error) {
		// Reset tracking when starting a new bead
		if isBeadUpdateInProgressCommand(cmd) && cmdErr == nil {
			r.track.hadCmdFailure = false
			r.track.verifyOK = false
		}
		if cmdErr != nil {
			r.track.hadCmdFailure = true
			// Stale verifyOK must not allow bd close after a failed go test/build in the same turn.
			if !isBenignImplementationCmdFailure(cmd) &&
				!verifyFailureSupersededByCanonicalBuild(r.townRoot, r.rig, r.track.activeBead, r.track.activeBeadPath, r.track.verifyOK, r.v, cmd) {
				r.track.verifyOK = false
			}
			if isImplementationVerifyCommandAttempt(cmd, r.townRoot, r.rig, r.track.activeBead, r.track.activeBeadPath, r.v) &&
				!verifyFailureSupersededByCanonicalBuild(r.townRoot, r.rig, r.track.activeBead, r.track.activeBeadPath, r.track.verifyOK, r.v, cmd) {
				r.clearPersistedVerifyOnFailedVerifyCmd(cmd)
			}
		}
		if isBeadCloseCommand(cmd) && cmdErr == nil {
			// A successful bd close means the bead guards passed; do not also
			// require verifyOK here.  Frontend/web beads and other artifacts
			// may close after EDIT:/WRITE: without a separate verify command.
			r.track.beadCloseOK = true
			r.track.hadCmdFailure = false
			if id := extractBeadIDFromBdClose(cmd); id != "" && id == r.track.activeBead {
				r.track.activeBead = ""
				r.track.activeBeadPath = ""
			}
		}
		if isBeadUpdateInProgressCommand(cmd) && cmdErr == nil {
			if id := extractBeadIDFromBdUpdate(cmd); id != "" {
				if r.track.activeBead != id {
					r.track.verifyOK = false
				}
				r.track.activeBead = id
				r.track.activeBeadPath = orchestrator.ImplementBeadPathForID(r.townRoot, r.rig, id, r.v)
				r.ensureTestBeadSkeletonAfterInProgress(cmd)
			}
		}
		if cmdErr == nil && isImplementationVerifyCommandOK(cmd, r.townRoot, r.rig, r.track.activeBead, r.v) {
			r.track.verifyOK = true
			r.track.hadCmdFailure = false
			r.persistImplementationProgress(cmd)
		}
		if cmdErr == nil && isGitCommitLayoutCommand(cmd, r.v.LayoutRoot) {
			r.track.hadCmdFailure = false
		}
	},
	"project_setup": func(r *stateRunner, cmd string, cmdErr error) {
		if cmdErr != nil {
			r.track.hadCmdFailure = true
		}
		if cmdErr == nil && isProjectSetupVerifyCommandOK(cmd, r.v) {
			r.track.verifyOK = true
			r.track.hadCmdFailure = false
		}
	},
	"plan_review": func(r *stateRunner, cmd string, cmdErr error) {
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
	},
	"design_review": func(r *stateRunner, cmd string, cmdErr error) {
		if cmdErr != nil {
			r.track.hadCmdFailure = true
		}
		if cmdErr == nil && isQAReadOnlyCommand(cmd) {
			r.track.hadCmdFailure = false
		}
	},
	"qa": func(r *stateRunner, cmd string, cmdErr error) {
		if cmdErr != nil {
			r.track.hadCmdFailure = true
		}
		if cmdErr == nil && isBdListClosedCommand(cmd) {
			r.track.bdListClosedOK = true
		}
		if cmdErr == nil && isBdListOpenCommand(cmd) {
			r.track.listOpenOK = true
		}
		if cmdErr == nil && isQATestCommandOK(cmd, r.v) {
			r.track.unittestOK = true
			r.track.hadCmdFailure = false
		}
		if cmdErr == nil && isQARuntimeSmokeCommandOK(cmd, r.townRoot, r.rig, r.v) {
			r.track.qaSmokeOK = true
			r.track.hadCmdFailure = false
		}
		if cmdErr == nil && isQAReadOnlyCommand(cmd) {
			r.track.hadCmdFailure = false
		}
		if cmdErr == nil && isQAFileReadCommand(cmd) {
			r.track.qaFilesRead = true
		}
	},
}

var artifactValidators = map[string]artifactValidateFn{
	"analysis": func(r *stateRunner, _ string) error {
		return validateAnalysisArtifacts(r.townRoot, r.rig)
	},
	"spec_review": func(r *stateRunner, _ string) error {
		return nil // spec_review is outcome-driven, no artifact checks
	},
	"design_review": func(r *stateRunner, _ string) error {
		// design_review is outcome-driven, but must also confirm the workflow profile
		// (spec-index guesswork) is sound before planning creates beads from it.
		if r.townRoot == "" || r.rig == "" {
			return nil
		}
		if defect := orchestrator.ValidateRigWorkflowProfileForQA(r.townRoot, r.rig, r.v); defect != "" {
			return fmt.Errorf("workflow profile has defects that will break planning — report failure to send the Architect back:\n%s", defect)
		}
		return nil
	},
	"design": func(r *stateRunner, _ string) error {
		return validateDesignArtifacts(r.townRoot, r.rig, r.track.designArchWritten, r.v)
	},
	"planning": func(r *stateRunner, _ string) error {
		return validatePlanningArtifacts(r.townRoot, r.rig, r.track.hadCmdFailure, r.track.beadCreateOK, r.track.beadDeleteOK, r.v)
	},
	"plan_review": func(r *stateRunner, _ string) error {
		return validatePlanReviewArtifacts(r.townRoot, r.rig, r.track.hadCmdFailure, r.track.listOpenOK, r.track.didDelete, r.v)
	},
	"project_setup": func(r *stateRunner, _ string) error {
		return validateProjectSetupArtifacts(r.townRoot, r.rig, r.track.hadCmdFailure, r.track.verifyOK, r.v)
	},
	"implementation": func(r *stateRunner, _ string) error {
		if false {
			return fmt.Errorf("QA rework pending — fix runtime/smoke issues named in Prior step failed before completing implementation")
		}
		return validateImplementationArtifacts(r.townRoot, r.rig, r.track.hadCmdFailure, r.track.beadCloseOK, r.track.verifyOK, r.v)
	},
	"qa": func(r *stateRunner, outcome string) error {
		return validateQAArtifacts(r.townRoot, r.rig, outcome, r.track.hadCmdFailure, r.track.bdListClosedOK, r.track.unittestOK, r.track.qaSmokeOK, r.track.qaFilesRead, r.v)
	},
}

var artifactAutoCompleters = map[string]artifactAutoCompleteFn{
	"design": func(r *stateRunner) error {
		return validateDesignArtifacts(r.townRoot, r.rig, r.track.designArchWritten, r.v)
	},
	"planning": func(r *stateRunner) error {
		return r.validateArtifacts("success")
	},
	"plan_review": func(r *stateRunner) error {
		return validatePlanReviewArtifacts(r.townRoot, r.rig, r.track.hadCmdFailure, r.track.listOpenOK, r.track.didDelete, r.v)
	},
	"implementation": func(r *stateRunner) error {
		if false {
			return fmt.Errorf("QA rework pending — fix runtime/smoke issues in Prior step failed (handlers, web/, routes); go test ./... alone is not enough")
		}
		// Do not auto-complete without green verify when profile defines QA (pytest/go test).
		if strings.TrimSpace(r.v.QAVerifyCommand) != "" && !r.track.verifyOK {
			return fmt.Errorf("profile verification must pass before auto-complete")
		}
		return validateImplementationArtifacts(r.townRoot, r.rig, r.track.hadCmdFailure, r.track.beadCloseOK, r.track.verifyOK, r.v)
	},
	"qa_review": func(r *stateRunner) error {
		return validateQAAutoComplete(r)
	},
}

func validateQAAutoComplete(r *stateRunner) error {
	if !r.track.verifyOK && !r.track.qaSmokeOK {
		return fmt.Errorf("QA requires verify or smoke to pass before auto-complete")
	}
	if r.townRoot != "" && r.rig != "" && orchestrator.BeadsDatabaseReady(r.townRoot, r.rig) {
		open, err := orchestrator.ListOpenImplementBeads(r.townRoot, r.rig, r.v)
		if err == nil && len(open) > 0 {
			return fmt.Errorf("%d open implement bead(s) remain", len(open))
		}
	}
	return nil
}

var artifactFailureHints = map[string]func(*stateRunner) string{
	"design": func(r *stateRunner) string {
		return "Write architecture.md with a heredoc CMD in this session (stale files from prior runs do not count). Read SPEC with head -n 60."
	},
	"planning": func(r *stateRunner) string {
		work := rigMayorRigPath(r.rig)
		minPlan := orchestrator.EffectiveMinPlanBytes(rigMayorRigDir(r.townRoot, r.rig), r.v)
		return fmt.Sprintf("plan.md must be ≥ %d bytes (## Bead map with ### <id>: <full-path> per file — not a short checklist). Repair beads: `bd delete %s --force` for duplicates, `bd create` for missing paths, rewrite plan.md, `wc -c plan.md`, then JSON success. Work from %s with BEADS_DIR=$GT_ROOT/%s/.beads.",
			minPlan, beadIDExample(r.townRoot, r.rig), work, r.rig)
	},
	"plan_review": func(r *stateRunner) string {
		return fmt.Sprintf("Run `bd list --status=open` from %s; compare beads to required_files. Read SPEC.md — architecture.md and plan.md must match SPEC HTTP routes and store API names (no /links vs /api/links drift, no ListLinks). Use outcome failure to send the Planner back.", rigMayorRigPath(r.rig))
	},
	"implementation": func(r *stateRunner) string {
		hint := fmt.Sprintf("One bead at a time from %s (BEADS_DIR=$GT_ROOT/%s/.beads): bd update → heredoc under %s/ → %s → bd close → JSON.",
			rigMayorRigPath(r.rig), r.rig, strings.TrimSpace(r.v.LayoutRoot), r.v.QAVerifyHint())
		if orchestrator.WorkflowNeedsRuntimeSmoke(r.townRoot, r.rig, r.v) {
			hint += " Before success, gt-agent runs unit tests + doc-derived HTTP smoke (curl each route in SPEC/architecture) — green tests alone are not enough."
		}
		return hint
	},
	"qa": func(r *stateRunner) string {
		hint := "Run real CMD: lines (not markdown fences): bd list --status=closed, head SPEC.md, " + r.v.QAVerifyHint() + " from " + rigMayorRigPath(r.rig) + "."
		if requiresQARuntimeSmoke(r.townRoot, r.rig, r.v) {
			if orchestrator.WorkflowUsesPython(r.v) {
				hint += " HTTP smoke only if SPEC documents API routes and profile includes a server entrypoint."
			} else {
				hint += " Runtime smoke from SPEC only (go run when profile has cmd/server + web): static assets and API paths in docs — not invented /api/ routes. gt-agent stops the server when QA finishes."
			}
		}
		return hint + " If unit tests pass but smoke fails and code matches architecture, use architecture_failure (resets to architect). If tests fail or code violates SPEC, use failure. Do not repeat go run+curl — reply with JSON only. No /workspace paths."
	},
}

type verifyKindFn func(r *stateRunner) string

var verifyKindHandlers = map[string]verifyKindFn{
	"go_setup": func(r *stateRunner) string {
		if orchestrator.WorkflowUsesGo(r.v) {
			return orchestrator.GoProjectSetupVerifyCommand(r.v, r.mayorRigWorkDir())
		}
		return ""
	},
	"go_with_tidy": func(r *stateRunner) string {
		if orchestrator.WorkflowUsesGo(r.v) {
			return orchestrator.GoVerifyCommandWithTidy(r.v, r.mayorRigWorkDir())
		}
		return ""
	},
	"go_implementation":     verifyImplementationBead,
	"python_implementation": verifyImplementationBead,
	"python_setup": func(r *stateRunner) string {
		if orchestrator.WorkflowUsesPython(r.v) {
			return orchestrator.PythonProjectSetupVerifyCommand(r.v)
		}
		return ""
	},
	"python": func(r *stateRunner) string {
		if orchestrator.WorkflowUsesPython(r.v) {
			return orchestrator.PythonVerifyCommand(r.v)
		}
		return ""
	},
	"node_setup": func(r *stateRunner) string {
		scoped := r.v.ForActivePhase()
		if orchestrator.WorkflowUsesNodeJS(scoped) {
			return orchestrator.NodeProjectSetupVerifyCommand(scoped)
		}
		return ""
	},
	"profile": func(r *stateRunner) string {
		return strings.TrimSpace(r.v.QAVerifyCommand)
	},
}

func verifyImplementationBead(r *stateRunner) string {
	if !orchestrator.WorkflowUsesGo(r.v) && !orchestrator.WorkflowUsesPython(r.v) {
		return ""
	}
	mayor := filepath.Join(r.townRoot, r.rig, "mayor", "rig")
	_, beadPath := r.effectiveImplementBead()
	return orchestrator.ImplementationVerifyCommandForBead(r.v, mayor, beadPath)
}

func (r *stateRunner) effectiveImplementBead() (id, path string) {
	if r == nil || r.track == nil {
		return "", ""
	}
	return orchestrator.ResolveImplementBeadForVerify(r.townRoot, r.rig, r.track.activeBead, r.v)
}

func (r *stateRunner) effectiveImplementBeadID() string {
	id, _ := r.effectiveImplementBead()
	return id
}

func (r *stateRunner) activeImplementBeadPath() string {
	if r != nil && r.track != nil {
		if p := strings.TrimSpace(r.track.activeBeadPath); p != "" {
			return p
		}
	}
	if r == nil || r.track == nil {
		return ""
	}
	return orchestrator.ImplementBeadPathForID(r.townRoot, r.rig, r.track.activeBead, r.v)
}

type autoVerifyWhenFn func(r *stateRunner, cmd string) bool

var autoVerifyWhenHandlers = map[string]autoVerifyWhenFn{
	"go_mod_tidy": func(r *stateRunner, cmd string) bool { return isGoModTidyCommand(cmd) },
	"go_mod_init": func(r *stateRunner, cmd string) bool { return isGoModInitCommand(cmd) },
	"pip_install": func(r *stateRunner, cmd string) bool { return isPipInstallRequirementsCommand(cmd) },
	"python_venv": func(r *stateRunner, cmd string) bool {
		return strings.Contains(strings.ToLower(cmd), "python3 -m venv")
	},
	"npm_install":         func(r *stateRunner, cmd string) bool { return isNodeInstallCommand(cmd, "npm") },
	"yarn_install":        func(r *stateRunner, cmd string) bool { return isNodeInstallCommand(cmd, "yarn") },
	"pnpm_install":        func(r *stateRunner, cmd string) bool { return isNodeInstallCommand(cmd, "pnpm") },
	"qa_test_ok":          func(r *stateRunner, cmd string) bool { return isQATestCommandOK(cmd, r.v) },
	"go_write_layout":     func(r *stateRunner, cmd string) bool { return orchestratedWritesGoUnderLayout(cmd, r.v) },
	"python_import_check": func(r *stateRunner, cmd string) bool { return orchestrator.IsPythonImportCheckCommand(cmd) },
	"python_compileall":   func(r *stateRunner, cmd string) bool { return strings.Contains(strings.ToLower(cmd), "compileall") },
	"python_test":         func(r *stateRunner, cmd string) bool { return isPythonTestCommand(cmd) },
}

func isNodeInstallCommand(cmd, manager string) bool {
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, manager+" install") || strings.Contains(lower, manager+" ci")
}

func isPythonTestCommand(cmd string) bool {
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "pytest") || strings.Contains(lower, "python3 -m unittest")
}
