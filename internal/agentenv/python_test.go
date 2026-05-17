package agentenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRewritePython3InCommand(t *testing.T) {
	py := "/home/user/.pyenv/shims/python3"
	in := "cd app && python3 -m pip install -r requirements.txt && python3 -m pytest -q"
	got := RewritePython3InCommand(in, py)
	if !strings.Contains(got, py) || strings.Contains(got, " python3 ") {
		t.Fatalf("got %q", got)
	}
}

func TestRewritePython3InCommand_preservesHeredoc(t *testing.T) {
	py := "/home/stevef/gt/mockrig/mayor/rig/.venv/bin/python3"
	in := "cd mockrig/mayor/rig && cat > backend/requirements.txt <<'EOF'\npython3 -m pytest\npytest>=7\nEOF"
	got := RewritePython3InCommand(in, py)
	if strings.Contains(got, py) {
		t.Fatalf("heredoc body must not be rewritten: %q", got)
	}
	if !strings.Contains(got, "python3 -m pytest") {
		t.Fatalf("expected literal in body: %q", got)
	}
}

func TestResolvePython3_prefersPyenvShim(t *testing.T) {
	home := t.TempDir()
	shims := filepath.Join(home, ".pyenv", "shims")
	if err := os.MkdirAll(shims, 0755); err != nil {
		t.Fatal(err)
	}
	py := filepath.Join(shims, "python3")
	if err := os.WriteFile(py, []byte("#!/bin/sh\necho pyenv\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	got := ResolvePython3(nil)
	if got != py {
		t.Fatalf("got %q want %q", got, py)
	}
}

func TestUnwrapBashLcSingleLine(t *testing.T) {
	t.Parallel()
	in := "bash -lc 'cd mockrig/mayor/rig && python3 -m pytest -q'"
	got := UnwrapBashLcSingleLine(in)
	if strings.HasPrefix(got, "bash -lc") {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(got, "cd mockrig/mayor/rig") {
		t.Fatalf("got %q", got)
	}
}
