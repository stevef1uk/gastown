package agentenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureVenv_createsAndReuses(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	work := t.TempDir()
	py1, created, err := EnsureVenv(work, ".venv", "python3")
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("expected first create")
	}
	if !isExecutable(py1) {
		t.Fatalf("missing %s", py1)
	}
	_, created2, err := EnsureVenv(work, ".venv", "python3")
	if err != nil || created2 {
		t.Fatalf("reuse: created=%v err=%v", created2, err)
	}
	stat, err := os.Stat(filepath.Join(work, ".venv"))
	if err != nil || !stat.IsDir() {
		t.Fatalf("venv dir: %v", err)
	}
}

func TestEnsureVenv_underWorkDirNotSubdir(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	mayorRig := t.TempDir()
	layout := filepath.Join(mayorRig, "tasklist")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	py, _, err := EnsureVenv(mayorRig, ".venv", "python3")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(py, mayorRig) {
		t.Fatalf("venv python %q should live under workDir %q", py, mayorRig)
	}
	if strings.HasPrefix(py, layout) {
		t.Fatalf("venv must not be created under layout subdir: %q", py)
	}
}

func TestWithRigVenv_setsVirtualEnv(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	work := t.TempDir()
	env, vpy, _, err := WithRigVenv(os.Environ(), work, ".venv")
	if err != nil {
		t.Fatal(err)
	}
	if vpy == "" {
		t.Fatal("empty vpy")
	}
	var virt string
	for _, e := range env {
		if strings.HasPrefix(e, "VIRTUAL_ENV=") {
			virt = strings.TrimPrefix(e, "VIRTUAL_ENV=")
		}
	}
	if virt == "" {
		t.Fatal("missing VIRTUAL_ENV")
	}
}
