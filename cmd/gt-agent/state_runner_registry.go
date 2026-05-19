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
	"design": func(r *stateRunner, cmd string) error {
		return validateDesignCommand(cmd, r.rig)
	},
	"planning": func(r *stateRunner, cmd string) error {
		return validatePlanningCommandWithProfile(cmd, r.rig, r.v)
	},
	"project_setup": func(r *stateRunner, cmd string) error {
		return validateProjectSetupCommand(cmd, r.rig, r.v)
	},
	"implementation": func(r *stateRunner, cmd string) error {
		if err := validateImplementationCommandWithState(cmd, r.townRoot, r.rig, r.track.activeBead, r.v, r.track.verifyOK); err != nil {
			return err
		}
		return validateImplementationBeadOrder(r.townRoot, r.rig, cmd, r.v)
	},
	"plan_review": func(r *stateRunner, cmd string) error {
		return validatePlanReviewCommand(cmd, r.rig)
	},
	"qa": func(r *stateRunner, cmd string) error {
		return validateQACommand(cmd, r.rig, r.v)
	},
}

var cmdGuardRejectScope = map[string]string{
	"design":         "architect scope",
	"planning":       "planner scope",
	"project_setup":  "project setup scope",
	"implementation": "polecat scope",
	"plan_review":    "plan review scope",
	"qa":             "QA scope",
}

var trackHandlers = map[string]trackFn{
	"design": func(r *stateRunner, cmd string, cmdErr error) {
		if cmdErr == nil && isArchitectureMDWriteCommand(cmd) {
			r.track.designArchWritten = true
		}
	},
	"planning": func(r *stateRunner, cmd string, cmdErr error) {
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
	},
	"implementation": func(r *stateRunner, cmd string, cmdErr error) {
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
		if cmdErr == nil && isImplementationVerifyCommandOK(cmd, r.townRoot, r.rig, r.track.activeBead, r.v) {
			r.track.verifyOK = true
			r.track.hadCmdFailure = false
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
	"qa": func(r *stateRunner, cmd string, cmdErr error) {
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
	},
}

var artifactValidators = map[string]artifactValidateFn{
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
		return validateImplementationArtifacts(r.townRoot, r.rig, r.track.hadCmdFailure, r.track.beadCloseOK, r.track.verifyOK, r.v)
	},
	"qa": func(r *stateRunner, outcome string) error {
		return validateQAArtifacts(r.townRoot, r.rig, outcome, r.track.hadCmdFailure, r.track.bdListClosedOK, r.track.unittestOK, r.v)
	},
}

var artifactAutoCompleters = map[string]artifactAutoCompleteFn{
	"design": func(r *stateRunner) error {
		return validateDesignArtifacts(r.townRoot, r.rig, r.track.designArchWritten, r.v)
	},
	"planning": func(r *stateRunner) error {
		return r.validateArtifacts("success")
	},
}

var artifactFailureHints = map[string]func(*stateRunner) string{
	"design": func(r *stateRunner) string {
		return "Write architecture.md with a heredoc CMD in this session (stale files from prior runs do not count). Read SPEC with head -n 60."
	},
	"planning": func(r *stateRunner) string {
		work := rigMayorRigPath(r.rig)
		return fmt.Sprintf("Repair beads: `bd delete %s --force` for duplicates, `bd create` only for missing paths, rewrite plan.md if needed — then JSON success. Work from %s with BEADS_DIR=$GT_ROOT/%s/.beads. No python/git/backend code.",
			beadIDExample(r.townRoot, r.rig), work, r.rig)
	},
	"plan_review": func(r *stateRunner) string {
		return fmt.Sprintf("Run `bd list --status=open` from %s with BEADS_DIR set; compare titles to architecture required_files. Use outcome failure to send the Planner back to fix duplicates or missing paths.", rigMayorRigPath(r.rig))
	},
	"implementation": func(r *stateRunner) string {
		return fmt.Sprintf("One bead at a time from %s (BEADS_DIR=$GT_ROOT/%s/.beads): bd update → heredoc under %s/ → %s → bd close → JSON.",
			rigMayorRigPath(r.rig), r.rig, strings.TrimSpace(r.v.LayoutRoot), r.v.UnittestCommandHint())
	},
	"qa": func(r *stateRunner) string {
		return "Run real CMD: lines (not markdown fences): bd list --status=closed, head SPEC.md, " + r.v.UnittestCommandHint() + " from " + rigMayorRigPath(r.rig) + ". No /workspace paths. Then JSON only."
	},
}

type verifyKindFn func(r *stateRunner) string

var verifyKindHandlers = map[string]verifyKindFn{
	"go_setup": func(r *stateRunner) string {
		if orchestrator.WorkflowUsesGo(r.v) {
			return orchestrator.GoProjectSetupVerifyCommand(r.v)
		}
		return ""
	},
	"go_with_tidy": func(r *stateRunner) string {
		if orchestrator.WorkflowUsesGo(r.v) {
			return orchestrator.GoVerifyCommandWithTidy(r.v)
		}
		return ""
	},
	"go_implementation": verifyImplementationBead,
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
	"profile": func(r *stateRunner) string {
		return strings.TrimSpace(r.v.QAVerifyCommand)
	},
}

func verifyImplementationBead(r *stateRunner) string {
	if !orchestrator.WorkflowUsesGo(r.v) && !orchestrator.WorkflowUsesPython(r.v) {
		return ""
	}
	mayor := filepath.Join(r.townRoot, r.rig, "mayor", "rig")
	beadPath := orchestrator.ImplementBeadPathForID(r.townRoot, r.rig, r.track.activeBead, r.v)
	return orchestrator.ImplementationVerifyCommandForBead(r.v, mayor, beadPath)
}

type autoVerifyWhenFn func(r *stateRunner, cmd string) bool

var autoVerifyWhenHandlers = map[string]autoVerifyWhenFn{
	"go_mod_tidy":           func(r *stateRunner, cmd string) bool { return isGoModTidyCommand(cmd) },
	"go_mod_init":           func(r *stateRunner, cmd string) bool { return isGoModInitCommand(cmd) },
	"pip_install":           func(r *stateRunner, cmd string) bool { return isPipInstallRequirementsCommand(cmd) },
	"python_venv":           func(r *stateRunner, cmd string) bool { return strings.Contains(strings.ToLower(cmd), "python3 -m venv") },
	"qa_test_ok":            func(r *stateRunner, cmd string) bool { return isQATestCommandOK(cmd, r.v) },
	"go_write_layout":       func(r *stateRunner, cmd string) bool { return orchestratedWritesGoUnderLayout(cmd, r.v) },
	"python_import_check":   func(r *stateRunner, cmd string) bool { return orchestrator.IsPythonImportCheckCommand(cmd) },
	"python_compileall":     func(r *stateRunner, cmd string) bool { return strings.Contains(strings.ToLower(cmd), "compileall") },
}
