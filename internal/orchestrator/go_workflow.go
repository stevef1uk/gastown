package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
)

// GoToolOutputMissingDeps reports go build/test output where a required module is missing
// (not yet downloaded or not in go.mod). Running go mod tidy in the layout root should fix it.
func GoToolOutputMissingDeps(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "no required module") ||
		strings.Contains(lower, "missing go.sum") ||
		strings.Contains(lower, "go.sum is out of date") ||
		strings.Contains(lower, "cannot find module")
}

// GoToolOutputMatchedNoPackages reports go build/test output where the package pattern matched nothing
// (exit code can still be 0 when the directory has no .go files).
func GoToolOutputMatchedNoPackages(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "matched no packages") ||
		strings.Contains(lower, "no packages to test") ||
		strings.Contains(lower, "no modules specified") ||
		strings.Contains(lower, "no module dependencies to download")
}

// GoModScaffoldOnlyCommand reports verify commands that only touch the module graph
// (go mod tidy or go mod download — no test/build/run). Empty modules may warn "matched no packages".
func GoModScaffoldOnlyCommand(cmd string) bool {
	lower := strings.ToLower(strings.TrimSpace(cmd))
	if !strings.Contains(lower, "go mod tidy") && !strings.Contains(lower, "go mod download") {
		return false
	}
	for _, blocked := range []string{"go test", "go build", "go run", "go vet"} {
		if strings.Contains(lower, blocked) {
			return false
		}
	}
	return true
}

// GoModTidyOnlyCommand reports verify/shell commands that only run go mod tidy (no test/build/run).
// Empty modules warn "matched no packages" on tidy; that is expected before any .go files exist.
func GoModTidyOnlyCommand(cmd string) bool {
	lower := strings.ToLower(strings.TrimSpace(cmd))
	if !strings.Contains(lower, "go mod tidy") {
		return false
	}
	for _, blocked := range []string{"go test", "go build", "go run", "go vet"} {
		if strings.Contains(lower, blocked) {
			return false
		}
	}
	return true
}

// WorkflowUsesGo reports whether the rig workflow profile targets a Go module
// (go test / go mod in qa_verify_command, or test_runner: go).
func WorkflowUsesGo(v WorkflowValidation) bool {
	qa := strings.ToLower(strings.TrimSpace(v.QAVerifyCommand))
	if strings.Contains(qa, "go test") || strings.Contains(qa, "go vet") ||
		strings.Contains(qa, "go mod") || strings.Contains(qa, "go build") ||
		strings.Contains(qa, "go run") {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(v.TestRunner)) {
	case "go", "golang":
		return true
	}
	return false
}

// GoModPhaseQAVerifyCommand is phase QA for go.mod-only delivery (no .go sources yet).
func GoModPhaseQAVerifyCommand(v WorkflowValidation) string {
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" || layout == "." {
		return "go mod download"
	}
	return "cd " + layout + " && go mod download"
}

// GoProjectSetupVerifyCommand is the green check for project_setup only: module
// toolchain under layout_root, not go build/test/run (polecat implements code).
func GoProjectSetupVerifyCommand(v WorkflowValidation, mayorRigDir string) string {
	return GoModScaffoldVerifyCommand(v, mayorRigDir)
}

// GoVerifyCommandWithTidy returns the verify shell chain for Go rigs, always
// running go mod tidy before tests when tidy is not already present.
func GoVerifyCommandWithTidy(v WorkflowValidation, mayorRigDir string) string {
	cdClause := GoShellCDClause(mayorRigDir, v.LayoutRoot)
	base := strings.TrimSpace(v.QAVerifyCommand)
	if base == "" {
		return cdClause + "go mod tidy && go test ./... && go vet ./..."
	}
	lower := strings.ToLower(base)
	if strings.Contains(lower, "go mod tidy") {
		return base
	}
	prefix := cdClause
	if prefix == "" && mayorRigDir == "" {
		prefix = "cd . && "
	}
	chain := base
	if !strings.Contains(lower, "cd ") {
		chain = prefix + chain
	}
	lowerChain := strings.ToLower(chain)
	if prefix != "" && strings.HasPrefix(lowerChain, strings.ToLower(prefix)) {
		return prefix + "go mod tidy && " + strings.TrimSpace(chain[len(prefix):])
	}
	return prefix + "go mod tidy && " + chain
}

// GoServerMainPath returns linkshelf/cmd/server/main.go under mayor/rig.
func GoServerMainPath(mayorRigDir string, v WorkflowValidation) string {
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" {
		layout = "."
	}
	return filepath.Join(mayorRigDir, layout, "cmd", "server", "main.go")
}

// GoServerMainExists reports whether polecat may run go run/curl integration verify.
func GoServerMainExists(mayorRigDir string, v WorkflowValidation) bool {
	_, err := os.Stat(GoServerMainPath(mayorRigDir, v))
	return err == nil
}

// IsServerMainImplementBead reports whether the active implement bead is cmd/server/main.go.
func IsServerMainImplementBead(beadPath string) bool {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	return strings.HasSuffix(beadPath, "cmd/server/main.go")
}

// GoCompileOnlyVerifyCommand is the compile verify chain (tidy + build) for polecat prompts and go.mod beads.
func GoCompileOnlyVerifyCommand(v WorkflowValidation, mayorRigDir string) string {
	return GoShellCDClause(mayorRigDir, v.LayoutRoot) + "go mod tidy && go build ./..."
}

// GoImplementationVerifyCommand is the verify chain during implementation: compile-only
// until cmd/server/main.go exists, then the profile QA command (go run/curl or go test).
func GoImplementationVerifyCommand(v WorkflowValidation, mayorRigDir string) string {
	return GoImplementationVerifyCommandForBead(v, mayorRigDir, "")
}

// GoImplementationVerifyCommandForBead picks verify for the active implement bead path.
func GoImplementationVerifyCommandForBead(v WorkflowValidation, mayorRigDir, beadPath string) string {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	if beadPath == "" {
		scoped := v.ForActivePhase()
		for _, f := range scoped.RequiredFiles {
			f = filepath.ToSlash(strings.TrimSpace(f))
			if strings.HasSuffix(f, "/go.mod") || f == "go.mod" {
				return GoModBeadVerifyCommand(v, mayorRigDir)
			}
		}
	}
	if strings.HasSuffix(beadPath, "go.mod") {
		return GoModBeadVerifyCommand(v, mayorRigDir)
	}
	if IsFrontendImplementPath(beadPath) {
		return ""
	}
	// Integration (go run/curl) only on the server main bead; other beads build their package only.
	if IsServerMainImplementBead(beadPath) && GoServerMainExists(mayorRigDir, v) {
		return GoVerifyCommandWithTidy(v, mayorRigDir)
	}
	return GoCompileVerifyCommandForBead(v, mayorRigDir, beadPath)
}

// GoModBeadVerifyCommand is verify for the go.mod implement bead only (module graph, not full build).
// Uses go mod download — go mod tidy strips SPEC requires before any .go sources exist.
func GoModBeadVerifyCommand(v WorkflowValidation, mayorRigDir string) string {
	return GoModScaffoldVerifyCommand(v, mayorRigDir)
}

// GoModScaffoldVerifyCommand checks the module graph without mutating go.mod (no tidy on empty modules).
func GoModScaffoldVerifyCommand(v WorkflowValidation, mayorRigDir string) string {
	return GoShellCDClause(mayorRigDir, v.LayoutRoot) + "go mod download"
}
