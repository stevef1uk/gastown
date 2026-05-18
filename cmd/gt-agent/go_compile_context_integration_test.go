package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

// writeBrokenLinkshelfTree creates a minimal module that fails go mod tidy (wrong sqlite import path).
func writeBrokenLinkshelfTree(t *testing.T, mayorRig string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(mayorRig, "linkshelf", "cmd", "server"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(mayorRig, "linkshelf", "internal", "store"), 0755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"linkshelf/go.mod": "module linkshelf\n\ngo 1.22\n",
		"linkshelf/cmd/server/main.go": `package main

import _ "github.com/modernc.org/sqlite"

func main() {}
`,
		"linkshelf/internal/store/store.go": `package store

import "modernc.org/sqlite"

type Store struct{}
`,
	}
	for rel, body := range files {
		p := filepath.Join(mayorRig, filepath.FromSlash(rel))
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func buildFailedGoCommandFeedback(runner *stateRunner, cmd string, out []byte, cmdErr error) string {
	var combined strings.Builder
	combined.WriteString(fmt.Sprintf("Command: %s\nError: %v\nOutput: %s\n\n", cmd, cmdErr, string(out)))
	if runner.hooks.AppendGoCompileContext && orchestrator.WorkflowUsesGo(runner.v) {
		appendGoCompileSourceContext(&combined, rigMayorRigDir(runner.townRoot, runner.rig), runner.v.LayoutRoot, cmd, string(out))
	}
	return combined.String()
}

func TestRigFlowImplementationHook_appendGoCompileContextEnabled(t *testing.T) {
	t.Parallel()
	hooks, err := orchestrator.RigFlowStateHooks("implementation")
	if err != nil {
		t.Fatal(err)
	}
	if !hooks.AppendGoCompileContext {
		t.Fatal("rig-flow implementation must set append_go_compile_context: true")
	}
}

func TestImplementationGoFailureFeedback_includesSourceContext(t *testing.T) {
	town := t.TempDir()
	rig := "testrig"
	mayor := filepath.Join(town, rig, "mayor", "rig")
	writeBrokenLinkshelfTree(t, mayor)

	v := orchestrator.WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go test ./...",
	}
	task := rigFlowTask(t, "implementation", v)
	runner := newStateRunner(task, town, rig)
	if !runner.hooks.AppendGoCompileContext {
		t.Fatal("test requires append_go_compile_context from rig-flow.yaml")
	}

	cmd := fmt.Sprintf("cd %s/mayor/rig/linkshelf && go mod tidy", rig)
	workDir := town
	out, cmdErr := runOrchestratedCommand(cmd, workDir, "test-session", os.Environ())
	if cmdErr == nil {
		t.Fatalf("expected go mod tidy to fail on broken imports; output:\n%s", out)
	}
	if !strings.Contains(string(out), "cannot find module") && !strings.Contains(string(out), "repository not found") {
		t.Fatalf("expected module resolution error in output, got:\n%s", out)
	}

	feedback := buildFailedGoCommandFeedback(runner, cmd, out, cmdErr)
	if !strings.Contains(feedback, "### Source context") {
		t.Fatalf("feedback must include source context block:\n%s", feedback)
	}
	if !strings.Contains(feedback, "github.com/modernc.org/sqlite") {
		t.Fatalf("feedback must show broken import from disk:\n%s", feedback)
	}
	if !strings.Contains(feedback, "--- linkshelf/") {
		t.Fatalf("feedback must label file paths:\n%s", feedback)
	}
}

func TestImplementationGoFailureFeedback_hookDisabledOmitsSourceContext(t *testing.T) {
	town := t.TempDir()
	rig := "testrig"
	mayor := filepath.Join(town, rig, "mayor", "rig")
	writeBrokenLinkshelfTree(t, mayor)

	v := orchestrator.WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go test ./...",
	}
	task := rigFlowTask(t, "implementation", v)
	task.Hooks.AppendGoCompileContext = false
	runner := newStateRunner(task, town, rig)

	cmd := fmt.Sprintf("cd %s/mayor/rig/linkshelf && go mod tidy", rig)
	out, cmdErr := runOrchestratedCommand(cmd, town, "test-session", os.Environ())
	if cmdErr == nil {
		t.Fatal("expected go mod tidy to fail")
	}

	feedback := buildFailedGoCommandFeedback(runner, cmd, out, cmdErr)
	if strings.Contains(feedback, "### Source context") {
		t.Fatalf("hook disabled must not inject source context:\n%s", feedback)
	}
}

func TestAfterCommand_autoVerifyFailure_appendsSourceContext(t *testing.T) {
	town := t.TempDir()
	rig := "testrig"
	mayor := filepath.Join(town, rig, "mayor", "rig")
	writeBrokenLinkshelfTree(t, mayor)

	v := orchestrator.WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go test ./...",
	}
	task := rigFlowTask(t, "implementation", v)
	runner := newStateRunner(task, town, rig)

	var combined strings.Builder
	verifyCmd := orchestrator.GoImplementationVerifyCommand(v, mayor)
	out, verifyErr := runOrchestratedCommand(
		fmt.Sprintf("cd %s/mayor/rig/linkshelf && go mod tidy && go build ./...", rig),
		town,
		"test-session",
		os.Environ(),
	)
	runner.afterCommand("cat > testrig/mayor/rig/linkshelf/internal/store/store.go <<'EOF'\npackage store\nEOF", nil, town, "test-session", os.Environ(), &combined)
	// Simulate auto-verify failure path (same as state_runner.runAutoVerify).
	if verifyErr != nil {
		combined.WriteString(fmt.Sprintf("Auto-verify: %s\nError: %v\nOutput: %s\n\n", verifyCmd, verifyErr, string(out)))
		if runner.hooks.AppendGoCompileContext && orchestrator.WorkflowUsesGo(runner.v) {
			appendGoCompileSourceContext(&combined, rigMayorRigDir(runner.townRoot, runner.rig), runner.v.LayoutRoot, verifyCmd, string(out))
		}
	} else {
		t.Fatalf("expected implementation verify to fail on broken tree; output:\n%s", out)
	}

	feedback := combined.String()
	if !strings.Contains(feedback, "### Source context") {
		t.Fatalf("auto-verify failure must append source context:\n%s", feedback)
	}
	if !strings.Contains(feedback, "github.com/modernc.org/sqlite") {
		t.Fatalf("auto-verify feedback must include file contents:\n%s", feedback)
	}
}
