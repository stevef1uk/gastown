package main

import "testing"

func TestPythonVerifyTarget_ignoresCdPrefix(t *testing.T) {
	cmd := "cd finally/mayor/rig && . .venv/bin/activate && python3 -m pytest -q backend/tests/test_market.py backend/tests/test_portfolio.py finally/"
	got := pythonVerifyTarget(cmd)
	want := "backend/tests/test_market.py"
	if got != want {
		t.Fatalf("pythonVerifyTarget(%q) = %q, want %q", cmd, got, want)
	}
}

func TestPythonVerifyTarget_plainPytest(t *testing.T) {
	got := pythonVerifyTarget("pytest tests/")
	if got != "tests/" {
		t.Fatalf("got %q, want tests/", got)
	}
}

func TestPythonVerifyTarget_noPath(t *testing.T) {
	got := pythonVerifyTarget("pytest --version")
	if got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}
