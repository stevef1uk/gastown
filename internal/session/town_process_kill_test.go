package session

import (
	"os"
	"os/exec"
	"testing"
)

func TestProcessBelongsToTown_sleepChild(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("sleep", "60")
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill() }()
	// Generic sleep is not a Gas Town process; ensure we do not false-positive.
	if processBelongsToTown(cmd.Process.Pid, dir) {
		t.Fatal("sleep should not match arbitrary temp dir as town without GT session metadata")
	}
}

func TestProcessCmdline_currentProcess(t *testing.T) {
	cmd := processCmdline(os.Getpid())
	if cmd == "" {
		t.Fatal("expected non-empty cmdline")
	}
}
