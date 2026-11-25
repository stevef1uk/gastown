package agentenv

import (
	"os"
	"strings"
	"testing"
)

func TestEnsurePATH_includesStandardDirs(t *testing.T) {
	env := EnsurePATH([]string{"HOME=/tmp/testhome"})
	var path string
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			path = strings.TrimPrefix(e, "PATH=")
			break
		}
	}
	if path == "" {
		t.Fatal("missing PATH")
	}
	for _, want := range []string{"/usr/bin", "/bin"} {
		if !strings.Contains(path, want) {
			t.Fatalf("PATH %q missing %q", path, want)
		}
	}
}

func TestEnsurePATH_preservesExtra(t *testing.T) {
	t.Setenv("GT_PATH_EXTRA", "/opt/custom/bin")
	env := EnsurePATH(os.Environ())
	var path string
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			path = strings.TrimPrefix(e, "PATH=")
			break
		}
	}
	if !strings.Contains(path, "/opt/custom/bin") {
		t.Fatalf("PATH %q missing GT_PATH_EXTRA", path)
	}
}

func TestPrependEnvPath(t *testing.T) {
	env := []string{"FOO=bar"}
	env = PrependEnvPath(env, "PYTHONPATH", "/dir/a")
	if len(env) != 2 || env[1] != "PYTHONPATH=/dir/a" {
		t.Fatalf("expected new key appended: %v", env)
	}
	env = PrependEnvPath(env, "PYTHONPATH", "/dir/b")
	want := "PYTHONPATH=/dir/b" + string(os.PathListSeparator) + "/dir/a"
	if env[1] != want {
		t.Fatalf("expected prepended: got %q, want %q", env[1], want)
	}
}

