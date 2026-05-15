package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestParseOrchestratedCommands_gluedPlannerLine(t *testing.T) {
	in := "CMD: ls -R testgt2/mayor/rig/CMD: cat testgt2/mayor/rig/SPEC.mdCMD: cat testgt2/mayor/rig/architecture.md{\"outcome\":\"failure\"}"
	cmds := parseOrchestratedCommands(in)
	if len(cmds) != 3 {
		t.Fatalf("want 3 commands, got %d: %v", len(cmds), cmds)
	}
	if cmds[0] != "ls -R testgt2/mayor/rig" {
		t.Fatalf("cmd[0]: %q", cmds[0])
	}
	if cmds[1] != "cat testgt2/mayor/rig/SPEC.md" {
		t.Fatalf("cmd[1]: %q", cmds[1])
	}
	if strings.Contains(cmds[2], "outcome") {
		t.Fatalf("cmd[2] should not include JSON tail: %q", cmds[2])
	}
}

func TestStripOutcomeLinesForCmdParse(t *testing.T) {
	in := "CMD: echo hi\n{\"outcome\":\"success\"}\nOUTCOME: success\n"
	out := stripOutcomeLinesForCmdParse(in)
	if strings.Contains(out, "outcome") && strings.Contains(out, "{") {
		t.Fatalf("outcome/json should be stripped: %q", out)
	}
	if !strings.Contains(out, "CMD: echo hi") {
		t.Fatalf("CMD line should remain: %q", out)
	}
}

func TestValidateOrchestratedArtifacts_design(t *testing.T) {
	dir := t.TempDir()
	rig := filepath.Join(dir, "myrig", "mayor", "rig")
	if err := os.MkdirAll(rig, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(rig, "architecture.md")
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	task := &orchestrator.Task{State: "design"}
	if err := validateOrchestratedArtifacts(task, dir, "myrig", "success"); err == nil {
		t.Fatal("expected size validation error")
	}
	if err := os.WriteFile(path, make([]byte, 200), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateOrchestratedArtifacts(task, dir, "myrig", "success"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateDesignCommand_forbidsImplementation(t *testing.T) {
	cases := []string{
		"git -C testgt2/mayor/rig commit -m x",
		"mkdir -p testgt2/mayor/rig/backend",
		"cat > testgt2/mayor/rig/backend/fizzbuzz.py <<'EOF'",
		"python3 testgt2/mayor/rig/backend/main.py",
	}
	for _, cmd := range cases {
		if err := validateDesignCommand(cmd, "testgt2"); err == nil {
			t.Fatalf("expected reject for %q", cmd)
		}
	}
	if err := validateDesignCommand("cat > testgt2/mayor/rig/architecture.md <<'EOF'", "testgt2"); err != nil {
		t.Fatalf("architecture heredoc should be allowed: %v", err)
	}
	if err := validateDesignCommand("head -n 60 testgt2/mayor/rig/SPEC.md", "testgt2"); err != nil {
		t.Fatalf("head SPEC should be allowed: %v", err)
	}
	heredoc := "cat > testgt2/mayor/rig/architecture.md <<'EOF'\n# Architecture\nDescribe backend/fizzbuzz.py here.\nEOF"
	if err := validateDesignCommand(heredoc, "testgt2"); err != nil {
		t.Fatalf("architecture heredoc may mention backend/: %v", err)
	}
}

func TestValidatePlanningCommand_forbidsImplementation(t *testing.T) {
	cases := []string{
		"cat > backend/fizzbuzz.py <<'EOF'",
		"git commit -m x",
		"python3 backend/main.py",
	}
	for _, cmd := range cases {
		if err := validatePlanningCommand(cmd, "testgt2"); err == nil {
			t.Fatalf("expected reject for %q", cmd)
		}
	}
	plan := "cat > testgt2/mayor/rig/plan.md <<'EOF'\n# Plan\nEOF"
	if err := validatePlanningCommand(plan, "testgt2"); err != nil {
		t.Fatalf("plan heredoc should be allowed: %v", err)
	}
	if err := validatePlanningCommand("gt bd add -t task -m hello", "testgt2"); err != nil {
		t.Fatalf("gt bd add should be allowed: %v", err)
	}
}

func TestValidateDesignArtifacts_allowsStaleBackendPy(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "myrig", "mayor", "rig")
	backend := filepath.Join(rigDir, "backend")
	if err := os.MkdirAll(backend, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backend, "fizzbuzz.py"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), make([]byte, 250), 0644); err != nil {
		t.Fatal(err)
	}
	task := &orchestrator.Task{State: "design"}
	if err := validateOrchestratedArtifacts(task, dir, "myrig", "success"); err != nil {
		t.Fatalf("stale backend/*.py must not block design: %v", err)
	}
}

func TestOrchestratedArtifactAutoOutcome_design(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "myrig", "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), make([]byte, 250), 0644); err != nil {
		t.Fatal(err)
	}
	task := &orchestrator.Task{State: "design", AllowedOutcomes: []string{"success", "failure"}}
	o, _, ok := orchestratedArtifactAutoOutcome(task, dir, "myrig")
	if !ok || o != "success" {
		t.Fatalf("want auto success, got ok=%v outcome=%q", ok, o)
	}
}
