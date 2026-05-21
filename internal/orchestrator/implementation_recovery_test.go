package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunOnTimeoutHook_recoverImplementationStall_unknown(t *testing.T) {
	t.Parallel()
	_, err := RunOnTimeoutHook("recover_implementation_stall_typo", "/tmp", "rig", DefaultWorkflowValidation())
	if err == nil || !strings.Contains(err.Error(), "unknown on_timeout") {
		t.Fatalf("err = %v", err)
	}
}

func TestRecoverImplementationStall_noBeads(t *testing.T) {
	t.Parallel()
	logLine, err := RecoverImplementationStall(t.TempDir(), "norigr", DefaultWorkflowValidation())
	if err != nil {
		t.Fatal(err)
	}
	if logLine != "" {
		t.Fatalf("expected no work without beads DB, got %q", logLine)
	}
}

func TestResetInProgressImplementBeads_integration(t *testing.T) {
	if os.Getenv("BD_TEST") == "" {
		t.Skip("set BD_TEST=1 to run bd integration test")
	}
	townRoot := os.Getenv("GT_TEST_TOWN")
	if townRoot == "" {
		t.Skip("GT_TEST_TOWN required")
	}
	rig := os.Getenv("GT_TEST_RIG")
	if rig == "" {
		rig = "testgt3"
	}
	v := DefaultWorkflowValidation()
	reset, err := ResetInProgressImplementBeads(townRoot, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	_ = reset
}

func TestStopRigDevServersScriptPath(t *testing.T) {
	dir := t.TempDir()
	scripts := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(scripts, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(scripts, "stop-rig-dev-servers.sh")
	if err := os.WriteFile(path, []byte("#!/bin/bash\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GASTOWN", dir)
	got := stopRigDevServersScriptPath()
	if got != path {
		t.Fatalf("got %q want %q", got, path)
	}
}
