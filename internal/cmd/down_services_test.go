package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestTownServicePIDs(t *testing.T) {
	root := filepath.Join(t.TempDir(), "town")
	other := t.TempDir()
	list := `101 /usr/local/bin/gt daemon run
102 gt daemon run
103 gt orchestrator run
104 /usr/local/bin/gt-orchestrator
105 sh -c gt daemon run
106 gt daemon status
107 gt daemon run
108 gt daemon run
109 gt-agent daemon run
110 gt daemon run
`
	dirs := map[int]string{101: root, 102: root, 103: root, 104: root, 107: other, 108: filepath.Join(root, "rig"), 110: root + "-other"}
	// Ownership checks resolve real directories. Missing mock paths would
	// consult the host's actual PID 108/110 and make this machine-dependent.
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	cwd := func(pid int) (string, error) {
		if dir, ok := dirs[pid]; ok {
			return dir, nil
		}
		return "", fmt.Errorf("exited")
	}
	for _, tc := range []struct {
		role string
		want string
	}{{"daemon", "[101 102]"}, {"orchestrator", "[103 104]"}} {
		got, err := townServicePIDs(list, root, tc.role, cwd)
		if err != nil || fmt.Sprint(got) != tc.want {
			t.Fatalf("%s: got %v, %v; want %s", tc.role, got, err, tc.want)
		}
	}
}

func TestServiceProcessRoleRejectsOtherCommands(t *testing.T) {
	for _, command := range []string{"sh -c gt daemon run", "gt down", "gt daemon status", "gt-agent daemon run", "echo gt orchestrator run", "other-gt daemon run"} {
		if got := serviceProcessRole(command); got != "" {
			t.Errorf("%q matched %q", command, got)
		}
	}
}

func TestTownServicePIDsAcceptsTownSymlink(t *testing.T) {
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "town link")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	got, err := townServicePIDs("101 gt daemon run", alias, "daemon", func(int) (string, error) { return root, nil })
	if err != nil || fmt.Sprint(got) != "[101]" {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestServiceProcessCwd(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	root := t.TempDir()
	cmd.Dir = root
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	got, err := serviceProcessCwd(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err = filepath.EvalSymlinks(got)
	if err != nil || got != want {
		t.Fatalf("got %q, %v; want %q", got, err, want)
	}
}
