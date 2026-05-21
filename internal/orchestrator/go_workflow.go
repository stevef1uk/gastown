package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
)

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

// GoProjectSetupVerifyCommand is the green check for project_setup only: module
// toolchain under layout_root, not go build/test/run (polecat implements code).
func GoProjectSetupVerifyCommand(v WorkflowValidation) string {
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" {
		layout = "."
	}
	return "cd " + layout + " && go mod tidy"
}

// GoVerifyCommandWithTidy returns the verify shell chain for Go rigs, always
// running go mod tidy before tests when tidy is not already present.
func GoVerifyCommandWithTidy(v WorkflowValidation) string {
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" {
		layout = "."
	}
	base := strings.TrimSpace(v.QAVerifyCommand)
	if base == "" {
		return "cd " + layout + " && go mod tidy && go test ./... && go vet ./..."
	}
	lower := strings.ToLower(base)
	if strings.Contains(lower, "go mod tidy") {
		return base
	}
	prefix := "cd " + layout + " && "
	chain := base
	if !strings.Contains(lower, "cd ") {
		chain = prefix + chain
	}
	if strings.HasPrefix(strings.ToLower(chain), prefix) {
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
func GoCompileOnlyVerifyCommand(v WorkflowValidation) string {
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" {
		layout = "."
	}
	return "cd " + layout + " && go mod tidy && go build ./..."
}

// GoImplementationVerifyCommand is the verify chain during implementation: compile-only
// until cmd/server/main.go exists, then the profile QA command (go run/curl or go test).
func GoImplementationVerifyCommand(v WorkflowValidation, mayorRigDir string) string {
	return GoImplementationVerifyCommandForBead(v, mayorRigDir, "")
}

// GoImplementationVerifyCommandForBead picks verify for the active implement bead path.
func GoImplementationVerifyCommandForBead(v WorkflowValidation, mayorRigDir, beadPath string) string {
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" {
		layout = "."
	}
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	if strings.HasSuffix(beadPath, "go.mod") {
		return GoModBeadVerifyCommand(v)
	}
	// Integration (go run/curl) only on the server main bead; other beads build their package only.
	if IsServerMainImplementBead(beadPath) && GoServerMainExists(mayorRigDir, v) {
		return GoVerifyCommandWithTidy(v)
	}
	return GoCompileVerifyCommandForBead(v, mayorRigDir, beadPath)
}

// GoModBeadVerifyCommand is verify for the go.mod implement bead only (module graph, not full build).
func GoModBeadVerifyCommand(v WorkflowValidation) string {
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" {
		layout = "."
	}
	return "cd " + layout + " && go mod tidy"
}
