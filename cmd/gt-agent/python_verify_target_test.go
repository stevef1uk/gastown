package main

import (
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

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

func TestPythonVerifyTarget_subshellParentheses(t *testing.T) {
	cmd := "cd testgt4/mayor/rig && test -f finally/.gitignore && (cd finally/backend && pytest tests/test_market_infrastructure.py)"
	got := pythonVerifyTarget(cmd)
	want := "tests/test_market_infrastructure.py"
	if got != want {
		t.Fatalf("pythonVerifyTarget(%q) = %q, want %q", cmd, got, want)
	}
}

func TestPythonVerifyTargetMatchesActiveBead_subpathSuffix(t *testing.T) {
	v := orchestrator.WorkflowValidation{LayoutRoot: "finally"}
	target := "tests/test_market_infrastructure.py"
	beadPath := "finally/backend/tests/test_market_infrastructure.py"
	if !pythonVerifyTargetMatchesActiveBead(target, beadPath, v) {
		t.Fatalf("expected %q to match active bead %q", target, beadPath)
	}
}

