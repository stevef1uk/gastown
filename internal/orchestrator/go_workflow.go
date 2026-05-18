package orchestrator

import (
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
