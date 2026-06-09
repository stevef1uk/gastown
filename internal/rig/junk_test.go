package rig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveMayorRigAgentJunk(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"package.json":           `{}`,
		"test-execution-command": "#!/bin/sh\n",
		"tests_skipped.txt":      "skip\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "node_modules", "jest"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SPEC.md"), []byte("# spec\n"), 0644); err != nil {
		t.Fatal(err)
	}
	removed, err := RemoveMayorRigAgentJunk(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) < 4 {
		t.Fatalf("removed = %v", removed)
	}
	if _, err := os.Stat(filepath.Join(dir, "SPEC.md")); err != nil {
		t.Fatal("SPEC.md should remain")
	}
}

func TestRejectMayorRigRootShellCommand_blocksNpm(t *testing.T) {
	t.Parallel()
	err := RejectMayorRigRootShellCommand("cd testgt3/mayor/rig && npm init -y", "linkshelf")
	if err == nil {
		t.Fatal("expected npm init rejection")
	}
}
