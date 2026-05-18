package main

import (
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestGoVerifyCommandMatches_layoutPath(t *testing.T) {
	t.Parallel()
	v := orchestrator.WorkflowValidation{LayoutRoot: "linkshelf"}
	verify := orchestrator.GoModBeadVerifyCommand(v)
	cmd := "cd testgt3/mayor/rig/linkshelf && go mod tidy"
	if !goVerifyCommandMatches(cmd, verify, v) {
		t.Fatalf("expected match for %q vs %q", cmd, verify)
	}
}

func TestGoVerifyCommandMatches_packageBuild(t *testing.T) {
	t.Parallel()
	v := orchestrator.WorkflowValidation{LayoutRoot: "linkshelf"}
	verify := orchestrator.GoCompileVerifyCommandForBead(v, "linkshelf/internal/store/store.go")
	cmd := "cd testgt3/mayor/rig && cd linkshelf && go mod tidy && go build ./internal/store/..."
	if !goVerifyCommandMatches(cmd, verify, v) {
		t.Fatalf("expected package build match: verify=%q cmd=%q", verify, cmd)
	}
	if goVerifyCommandMatches("cd testgt3/mayor/rig/linkshelf && go build ./...", verify, v) {
		t.Fatal("full module build must not match package-scoped verify")
	}
}

func TestBenignGoCommandError_modInitExists(t *testing.T) {
	t.Parallel()
	cmd := "cd testgt3/mayor/rig/linkshelf && go mod init linkshelf"
	out := []byte("go: /tmp/linkshelf/go.mod already exists\n")
	if !benignGoCommandError(cmd, errExitStatus(1), out) {
		t.Fatal("expected benign when go.mod exists")
	}
}

type errExitStatus int

func (e errExitStatus) Error() string { return "exit status 1" }
