package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestUnwrapBashLcMultiline_unclosedWrapperQuote(t *testing.T) {
	in := "bash -lc 'cd testgt2/mayor/rig && cat > plan.md <<EOF\n# Plan\nEOF\n"
	got := unwrapBashLcMultiline(in)
	if strings.HasPrefix(got, "'") {
		t.Fatalf("should strip opening quote: %q", got)
	}
	if !strings.Contains(got, "cat > plan.md") {
		t.Fatalf("got %q", got)
	}
}

func TestUnwrapBashLcMultiline_doubleQuoted(t *testing.T) {
	in := "bash -lc \"export BEADS_DIR=\\$GT_ROOT/testgt2/.beads && cd testgt2/mayor/rig && cat > plan.md <<'EOF'\n# Plan\nline two\nEOF\n\""
	got := unwrapBashLcMultiline(in)
	if strings.Contains(got, "bash -lc") {
		t.Fatalf("should unwrap: %q", got)
	}
	if !strings.Contains(got, "cat > plan.md") || !strings.Contains(got, "# Plan") {
		t.Fatalf("got %q", got)
	}
}

func TestPrepareOrchestratedScript_normalizesEOF(t *testing.T) {
	in := "cat > plan.md " + bashLcHeredocEOFMarker() + "\nbody\nEOF"
	got := prepareOrchestratedScript(in)
	if strings.Contains(got, `"'"`) {
		t.Fatalf("should normalize delimiter: %q", got)
	}
	if !strings.Contains(got, "<<'EOF'") {
		t.Fatalf("got %q", got)
	}
}

func TestRunOrchestratedCommand_heredocWritesFile(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "testgt2", "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	cmd := "export GT_ROOT=" + dir + " && cd testgt2/mayor/rig && cat > plan.md <<'EOF'\n# Implementation Plan\n" +
		strings.Repeat("x", 220) + "\nEOF"
	env := []string{"GT_ROOT=" + dir, "HOME=" + dir, "PATH=/usr/bin:/bin"}
	out, err := runOrchestratedCommand(cmd, dir, "", env)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	info, err := os.Stat(filepath.Join(rigDir, "plan.md"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() < orchestrator.DefaultWorkflowValidation().MinPlanBytes {
		t.Fatalf("plan.md size %d", info.Size())
	}
}

func TestRewriteUnittestToWorkdir(t *testing.T) {
	cmd := "python3 -m unittest backend.test_fizzbuzz -v"
	fixed, ok := rewriteUnittestToWorkdir(cmd, "testgt2")
	if !ok || !strings.Contains(fixed, "cd testgt2/mayor/rig &&") {
		t.Fatalf("got ok=%v cmd=%q", ok, fixed)
	}
	already := "cd testgt2/mayor/rig && python3 -m unittest backend.test_fizzbuzz -v"
	if _, ok := rewriteUnittestToWorkdir(already, "testgt2"); ok {
		t.Fatal("should not rewrite when cd present")
	}
}

func TestRewriteBdListLimit(t *testing.T) {
	cmd := "export BEADS_DIR=x && bd list --status=open | grep foo"
	fixed, ok := rewriteBdListLimit(cmd)
	if !ok || !strings.Contains(fixed, "--limit=0") {
		t.Fatalf("got %q ok=%v", fixed, ok)
	}
}

func TestNeedsOrchestratedScriptFile(t *testing.T) {
	if !needsOrchestratedScriptFile("echo <<EOF\nx\nEOF") {
		t.Fatal("heredoc should use script")
	}
	if needsOrchestratedScriptFile("head -n 5 foo") {
		t.Fatal("simple cmd should not")
	}
}
