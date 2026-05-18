package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
	"github.com/steveyegge/gastown/internal/testrig"
)

func TestUnwrapBashLcMultiline_unclosedWrapperQuote(t *testing.T) {
	in := "bash -lc 'cd mockrig/mayor/rig && cat > plan.md <<EOF\n# Plan\nEOF\n"
	got := unwrapBashLcMultiline(in)
	if strings.HasPrefix(got, "'") {
		t.Fatalf("should strip opening quote: %q", got)
	}
	if !strings.Contains(got, "cat > plan.md") {
		t.Fatalf("got %q", got)
	}
}

func TestUnwrapBashLcMultiline_doubleQuoted(t *testing.T) {
	in := "bash -lc \"export BEADS_DIR=\\$GT_ROOT/mockrig/.beads && cd mockrig/mayor/rig && cat > plan.md <<'EOF'\n# Plan\nline two\nEOF\n\""
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
	rigDir := filepath.Join(dir, "mockrig", "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	cmd := "export GT_ROOT=" + dir + " && cd mockrig/mayor/rig && cat > plan.md <<'EOF'\n# Implementation Plan\n" +
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

func TestRewriteUnittestToWorkdir_skipsRequirementsHeredoc(t *testing.T) {
	cmd := "cd " + testrig.Worktree(testrig.Name) + " && cat > " + testrig.RequirementsFile + " <<'EOF'\npython3 -m pytest\nwidget-lib\nEOF"
	if fixed, ok := rewriteUnittestToWorkdir(cmd, testrig.Name, orchestrator.DefaultWorkflowValidation()); ok {
		t.Fatalf("must not rewrite requirements heredoc: ok=%v cmd=%q", ok, fixed)
	}
}

func TestRewriteUnittestToWorkdir(t *testing.T) {
	cmd := "python3 -m unittest backend.test_fizzbuzz -v"
	defV := orchestrator.DefaultWorkflowValidation()
	fixed, ok := rewriteUnittestToWorkdir(cmd, "mockrig", defV)
	if !ok || !strings.Contains(fixed, "cd mockrig/mayor/rig &&") {
		t.Fatalf("got ok=%v cmd=%q", ok, fixed)
	}
	already := "cd mockrig/mayor/rig && python3 -m unittest backend.test_fizzbuzz -v"
	if _, ok := rewriteUnittestToWorkdir(already, "mockrig", defV); ok {
		t.Fatal("should not rewrite when cd present")
	}
	pipCmd := "pip install -r requirements.txt"
	fixed, ok = rewriteUnittestToWorkdir(pipCmd, "mockrig", defV)
	if !ok || !strings.Contains(fixed, "python3 -m pip") || !strings.Contains(fixed, "cd mockrig/mayor/rig &&") {
		t.Fatalf("pip rewrite: ok=%v cmd=%q", ok, fixed)
	}
}

func TestRewriteBdListImplementScope(t *testing.T) {
	cmd := "export BEADS_DIR=x && bd list --status=open --flat"
	fixed, ok := rewriteBdListImplementScope(cmd, "Implement linkshelf/")
	if !ok {
		t.Fatal("expected rewrite")
	}
	if !strings.Contains(fixed, "--status=open,in_progress") {
		t.Fatalf("want open,in_progress: %q", fixed)
	}
	if !strings.Contains(fixed, "grep -Fi") || !strings.Contains(fixed, "|| true") {
		t.Fatalf("want scoped grep with || true: %q", fixed)
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

func TestOrchestratedCommandWorkDir_rigFlowUsesTownRoot(t *testing.T) {
	town := "/gt"
	rig := testrig.Name
	for _, state := range []string{"kickoff", "design", "planning", "plan_review", "project_setup", "implementation", "qa_review"} {
		if got := orchestratedCommandWorkDir(town, rig, state); got != town {
			t.Fatalf("%s cwd = %q, want town root %q", state, got, town)
		}
	}
}

func TestCommandHasMayorRigCD(t *testing.T) {
	rig := testrig.Name
	if !commandHasMayorRigCD("cd "+rig+"/mayor/rig && pytest", rig) {
		t.Fatal("relative cd")
	}
	if !commandHasMayorRigCD("cd ~/gt/"+rig+"/mayor/rig && pytest", rig) {
		t.Fatal("home cd")
	}
	if commandHasMayorRigCD("pytest -q backend", rig) {
		t.Fatal("no cd")
	}
}

func TestRewriteUnittestToWorkdir_skipsWhenAlreadyCD(t *testing.T) {
	rig := testrig.Name
	already := "cd ~/gt/" + rig + "/mayor/rig && python3 -m pytest -q defender/backend"
	if fixed, ok := rewriteUnittestToWorkdir(already, rig, orchestrator.DefaultWorkflowValidation()); ok {
		t.Fatalf("should not prepend cd: %q", fixed)
	}
}

func TestRewriteUnittestToWorkdir_goLayout(t *testing.T) {
	v := orchestrator.WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go run ./cmd/server",
		TestRunner:      "custom",
	}
	cmd := "go run ./cmd/server"
	fixed, ok := rewriteUnittestToWorkdir(cmd, "mockrig", v)
	if !ok {
		t.Fatal("expected rewrite")
	}
	if !strings.Contains(fixed, "cd mockrig/mayor/rig/linkshelf &&") {
		t.Fatalf("want single cd into module: %q", fixed)
	}
	if strings.Count(fixed, "cd linkshelf") > 1 || strings.HasPrefix(fixed, "cd linkshelf && cd ") {
		t.Fatalf("must not double-cd layout: %q", fixed)
	}
}

func TestRewriteUnittestToWorkdir_alreadyInModule(t *testing.T) {
	v := orchestrator.WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go mod tidy",
	}
	cmd := "cd testgt3/mayor/rig/linkshelf && go mod tidy"
	fixed, ok := rewriteUnittestToWorkdir(cmd, "testgt3", v)
	if ok && strings.HasPrefix(fixed, "cd linkshelf && cd testgt3") {
		t.Fatalf("must not prepend extra layout cd: %q", fixed)
	}
}

func TestRewriteUnittestToWorkdir_mayorRigCDIntoModule(t *testing.T) {
	v := orchestrator.WorkflowValidation{LayoutRoot: "linkshelf", TestRunner: "custom", QAVerifyCommand: "go build ./..."}
	cmd := "cd testgt3/mayor/rig && go build ./..."
	fixed, ok := rewriteUnittestToWorkdir(cmd, "testgt3", v)
	if !ok {
		t.Fatal("expected rewrite")
	}
	want := "cd testgt3/mayor/rig/linkshelf && go build ./..."
	if fixed != want {
		t.Fatalf("got %q want %q", fixed, want)
	}
}
