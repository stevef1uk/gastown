package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func pythonVerifyCommandMatches(cmd, verify string, v orchestrator.WorkflowValidation) bool {
	if commandMatchesQAVerify(cmd, verify) {
		return true
	}
	c := strings.ToLower(cmd)
	verifyLow := strings.ToLower(verify)
	if strings.Contains(verifyLow, "import pytest") {
		return strings.Contains(c, "import pytest")
	}
	if strings.Contains(verifyLow, "-m pytest") || strings.Contains(verifyLow, "pytest") {
		if strings.Contains(c, "pytest") {
			return true
		}
	}
	if strings.Contains(c, "compileall") && strings.Contains(verifyLow, "compileall") {
		cPath := pythonCompileallTarget(c)
		vPath := pythonCompileallTarget(verifyLow)
		if cPath != "" && vPath != "" {
			if cPath == vPath || strings.HasSuffix(vPath, "/"+cPath) || strings.HasSuffix(cPath, "/"+vPath) {
				return true
			}
		}
	}
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" || layout == "." {
		return false
	}
	if !strings.Contains(c, layout) {
		return false
	}
	for _, part := range strings.Split(verify, "&&") {
		p := strings.ToLower(strings.TrimSpace(part))
		if strings.HasPrefix(p, "cd ") {
			continue
		}
		if p == "" {
			continue
		}
		if !strings.Contains(c, p) {
			return false
		}
	}
	return len(strings.Fields(verify)) > 0
}

func pythonCompileallTarget(lowerCmd string) string {
	idx := strings.Index(lowerCmd, "compileall")
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(lowerCmd[idx+len("compileall"):])
	rest = strings.TrimPrefix(rest, "-q")
	rest = strings.TrimSpace(rest)
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return ""
	}
	return strings.Trim(fields[len(fields)-1], `"'`)
}

func taskValidation(townRoot string, task *orchestrator.Task) orchestrator.WorkflowValidation {
	if task == nil {
		return orchestrator.DefaultWorkflowValidation()
	}
	v := orchestrator.ClampProfileValidation(task.Validation.WithDefaults())
	// fetch_task JSON can omit delivery_phases; always prefer the rig profile on disk so
	// phased QA smoke and bead scope match workflow-profile.json.
	if townRoot != "" && task.Rig != "" {
		if prof, ok, err := orchestrator.LoadRigWorkflowProfileFile(townRoot, task.Rig); err == nil && ok {
			v = orchestrator.ClampProfileValidation(prof.WithDefaults())
		}
		// Always enrich from architecture to ensure required_files includes all paths
		// from SPEC.md and architecture.md. Without this, the profile may lack files
		// like go.mod that the architecture lists, causing the planner to skip creating
		// beads for them while the triad judge detects the mismatch (infinite loop).
		mayorRig := filepath.Join(townRoot, task.Rig, "mayor", "rig")
		v = orchestrator.EnrichWorkflowValidationFromArchitecture(v, mayorRig)
	}
	return v.ForActivePhase()
}

func isProjectSetupVerifyCommandOK(cmd string, v orchestrator.WorkflowValidation) bool {
	if orchestrator.WorkflowUsesPython(v) {
		if commandMatchesQAVerify(cmd, orchestrator.PythonProjectSetupVerifyCommand(v)) {
			return true
		}
		return isPipInstallRequirementsCommand(cmd)
	}
	if !orchestrator.WorkflowUsesGo(v) {
		return isQATestCommandOK(cmd, v)
	}
	lower := strings.ToLower(strings.Join(strings.Fields(cmd), " "))
	if strings.Contains(lower, "go build") || strings.Contains(lower, "go run") ||
		strings.Contains(lower, "go test") || strings.Contains(lower, "curl ") {
		return false
	}
	return strings.Contains(lower, "go mod tidy") || strings.Contains(lower, "go mod download") ||
		strings.Contains(lower, "go mod init") || strings.Contains(lower, "go get ")
}

func isQATestCommandOK(cmd string, v orchestrator.WorkflowValidation) bool {
	verify := strings.TrimSpace(v.QAVerifyCommand)
	if verify != "" {
		if commandMatchesQAVerify(cmd, verify) {
			return true
		}
		if orchestrator.WorkflowUsesGo(v) && goVerifyCommandMatches(cmd, verify, v) {
			return true
		}
		if orchestrator.WorkflowUsesPython(v) && pythonVerifyCommandMatches(cmd, verify, v) {
			return true
		}
		if orchestrator.WorkflowUsesDocker(v) && dockerVerifyCommandMatches(cmd, verify) {
			return true
		}
		return false
	}
	return isUnittestCommand(cmd, v.UnittestModule)
}

func isImplementationVerifyCommandOK(cmd, townRoot, rig, activeBead string, v orchestrator.WorkflowValidation) bool {
	if isQATestCommandOK(cmd, v) {
		return true
	}
	if orchestrator.WorkflowUsesPython(v) && rig != "" {
		mayorDir := filepath.Join(townRoot, rig, "mayor", "rig")
		beadPath := orchestrator.ImplementBeadPathForID(townRoot, rig, activeBead, v)
		impl := orchestrator.PythonImplementationVerifyCommandForBead(v, mayorDir, beadPath)
		if pythonVerifyCommandMatches(cmd, impl, v) {
			return true
		}
	}
	if orchestrator.WorkflowUsesDocker(v) && rig != "" {
		mayorDir := filepath.Join(townRoot, rig, "mayor", "rig")
		beadPath := orchestrator.ImplementBeadPathForID(townRoot, rig, activeBead, v)
		impl := orchestrator.DockerImplementationVerifyCommandForBead(v, mayorDir, beadPath)
		if dockerVerifyCommandMatches(cmd, impl) {
			return true
		}
	}
	if orchestrator.WorkflowUsesDocker(v) {
		return isQATestCommandOK(cmd, v)
	}
	if !orchestrator.WorkflowUsesGo(v) || rig == "" {
		return false
	}
	mayorDir := rigMayorRigDir(townRoot, rig)
	activeBead, _ = orchestrator.ResolveImplementBeadForVerify(townRoot, rig, activeBead, v)
	beadPath := orchestrator.ImplementBeadPathForID(townRoot, rig, activeBead, v)
	impl := orchestrator.ImplementationVerifyCommandForBead(v, mayorDir, beadPath)
	if commandMatchesQAVerify(cmd, impl) {
		return true
	}
	if orchestrator.WorkflowUsesGo(v) {
		if goVerifyCommandMatches(cmd, impl, v) {
			return true
		}
	}
	if orchestrator.WorkflowUsesPython(v) {
		if pythonVerifyCommandMatches(cmd, impl, v) {
			return true
		}
	}
	// Full-suite QA verify (e.g. final pytest -v) — only after per-bead checks miss.
	return isQATestCommandOK(cmd, v)
}

// isImplementationVerifyCommandAttempt reports whether cmd is a verify attempt for the active bead
// (shape match only — exit code not checked).
func isImplementationVerifyCommandAttempt(cmd, townRoot, rig, activeBead, activeBeadPath string, v orchestrator.WorkflowValidation) bool {
	if isImplementationVerifyCommandOK(cmd, townRoot, rig, activeBead, v) {
		return true
	}
	if !orchestrator.WorkflowUsesGo(v) || rig == "" || strings.TrimSpace(activeBead) == "" {
		return false
	}
	beadPath := orchestrator.ImplementBeadPathForID(townRoot, rig, activeBead, v)
	if beadPath == "" {
		beadPath = strings.TrimSpace(activeBeadPath)
	}
	if beadPath == "" {
		return false
	}
	mayorDir := filepath.Join(townRoot, rig, "mayor", "rig")
	compile := orchestrator.GoCompileVerifyCommandForBead(v, mayorDir, beadPath)
	if compile != "" && (goVerifyCommandMatches(cmd, compile, v) || commandMatchesQAVerify(cmd, compile)) {
		return true
	}
	lower := strings.ToLower(cmd)
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" || !strings.Contains(lower, layout) {
		return false
	}
	if !strings.Contains(lower, "go test") && !strings.Contains(lower, "go build") {
		return false
	}
	pkgDir := filepath.ToSlash(filepath.Dir(beadPath))
	if strings.Contains(lower, strings.ToLower(pkgDir)) {
		return true
	}
	relPkg := strings.TrimPrefix(pkgDir, layout+"/")
	if relPkg != "" && strings.Contains(lower, "./"+strings.ToLower(relPkg)) {
		return true
	}
	return false
}

func implementationVerifyAttemptBeforeBdClose(cmd, townRoot, rig, activeBead string, v orchestrator.WorkflowValidation) bool {
	if !strings.Contains(strings.ToLower(cmd), "bd close") || !strings.Contains(cmd, "&&") {
		return false
	}
	parts := strings.Split(cmd, "&&")
	for i, part := range parts {
		if isBeadCloseCommand(part) {
			for j := 0; j < i; j++ {
				if isImplementationVerifyCommandOK(strings.TrimSpace(parts[j]), townRoot, rig, activeBead, v) {
					return true
				}
			}
			return false
		}
	}
	return false
}

// goVerifyCommandMatches accepts agent commands that cd into layout via rig/mayor/rig paths
// while verify hints use layout-relative cd (cd linkshelf && go mod tidy).
func goVerifyCommandMatches(cmd, verify string, v orchestrator.WorkflowValidation) bool {
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" {
		return false
	}
	lower := strings.ToLower(cmd)
	if !strings.Contains(lower, layout) {
		return false
	}
	steps := goToolchainStepsFromVerify(verify)
	if len(steps) == 0 {
		return false
	}
	for _, step := range steps {
		if strings.HasPrefix(step, "go build ") {
			target := strings.TrimSpace(strings.TrimPrefix(step, "go build"))
			if target != "" && !strings.Contains(lower, target) {
				return false
			}
			if !strings.Contains(lower, "go build") {
				return false
			}
			continue
		}
		if !strings.Contains(lower, step) {
			return false
		}
	}
	return true
}

func goToolchainStepsFromVerify(verify string) []string {
	var steps []string
	for _, part := range strings.Split(verify, "&&") {
		p := strings.ToLower(strings.TrimSpace(part))
		if strings.HasPrefix(p, "go ") {
			steps = append(steps, p)
		}
	}
	return steps
}

// dockerVerifyCommandMatches accepts verify run from mayor/rig while hints use layout-relative cd.
func dockerVerifyCommandMatches(cmd, verify string) bool {
	if commandMatchesQAVerify(cmd, verify) {
		return true
	}
	c := dockerSubstantiveCommand(cmd)
	v := dockerSubstantiveCommand(verify)
	if c == "" || v == "" {
		return false
	}
	if c == v {
		return true
	}
	if strings.Contains(c, "docker build") && strings.Contains(v, "docker build") {
		return true
	}
	return (strings.Contains(c, "docker-compose") || strings.Contains(c, "docker compose")) &&
		strings.Contains(c, "config") &&
		(strings.Contains(v, "docker-compose") || strings.Contains(v, "docker compose")) &&
		strings.Contains(v, "config")
}

func dockerSubstantiveCommand(cmd string) string {
	s := strings.TrimSpace(cmd)
	if i := strings.LastIndex(strings.ToLower(s), "&&"); i >= 0 {
		s = strings.TrimSpace(s[i+2:])
	}
	return strings.ToLower(strings.Join(strings.Fields(orchestrator.NormalizeDockerCommand(s)), " "))
}

func commandMatchesQAVerify(cmd, verify string) bool {
	c := strings.ToLower(strings.TrimSpace(cmd))
	v := strings.ToLower(strings.TrimSpace(verify))
	if v == "" {
		return false
	}
	cNorm := strings.Join(strings.Fields(c), " ")
	vNorm := strings.Join(strings.Fields(v), " ")
	if strings.Contains(cNorm, vNorm) {
		return true
	}
	return strings.Contains(c, v)
}

// rejectInventedBdVerifyCommand blocks the common polecat mistake `bd verify <id>` (no such subcommand).
func rejectInventedBdVerifyCommand(cmd, townRoot, rig, activeBead string, v orchestrator.WorkflowValidation) error {
	lower := strings.ToLower(strings.TrimSpace(cmd))
	if !strings.Contains(lower, "bd verify") && !strings.Contains(lower, "bd  verify") {
		return nil
	}
	mayorDir := filepath.Join(townRoot, rig, "mayor", "rig")
	beadPath := orchestrator.ImplementBeadPathForID(townRoot, rig, activeBead, v)
	if beadPath == "" {
		if next, err := orchestrator.NextOpenImplementBead(townRoot, rig, v); err == nil && next != nil {
			beadPath = orchestrator.ImplementBeadPathForID(townRoot, rig, next.ID, v)
		}
	}
	hint := orchestrator.AgentShellVerifyCommand(rig, v, mayorDir, beadPath)
	if hint == "" && orchestrator.IsFrontendImplementPath(beadPath) {
		return fmt.Errorf("bd has no verify subcommand — frontend bead %s: use WRITE:/EDIT: on the file, then bd close when the artifact validates", beadPath)
	}
	if hint == "" {
		return fmt.Errorf("bd has no verify subcommand — run Verify from the Next bead line as CMD: <shell verify>")
	}
	return fmt.Errorf("bd has no verify subcommand — run Verify from the Next bead line: %s", hint)
}

// verifyFailureSupersededByCanonicalBuild reports failed go test CMDs that must not clear
// verifyOK when post-write or an explicit go build verify already passed for the active bead
// (foreign *_test.go from other beads makes go test fail while go build is the canonical verify).
func verifyFailureSupersededByCanonicalBuild(townRoot, rig, activeBead, activeBeadPath string, verifyOK bool, v orchestrator.WorkflowValidation, cmd string) bool {
	if !verifyOK || strings.TrimSpace(activeBead) == "" {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(cmd))
	if !strings.Contains(lower, "go test") {
		return false
	}
	mayorDir := filepath.Join(townRoot, rig, "mayor", "rig")
	beadPath := strings.TrimSpace(activeBeadPath)
	if beadPath == "" {
		beadPath = orchestrator.ImplementBeadPathForID(townRoot, rig, activeBead, v)
	}
	if beadPath == "" {
		return false
	}
	return orchestrator.CanonicalImplementationVerifyIsGoBuildOnly(v, mayorDir, beadPath)
}

// isBenignImplementationCmdFailure reports bd read/list/show failures that must not
// clear a green session verify from an earlier go test in the same implementation turn.
func isBenignImplementationCmdFailure(cmd string) bool {
	lower := strings.ToLower(strings.TrimSpace(cmd))
	if !strings.Contains(lower, "bd ") {
		return false
	}
	for _, sub := range []string{"bd list", "bd show", "bd mol current", "bd prime"} {
		if strings.Contains(lower, sub) {
			return true
		}
	}
	return false
}
