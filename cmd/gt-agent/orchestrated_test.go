package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestParseOrchestratedCommands_gluedQuoteCMD(t *testing.T) {
	in := "CMD: bash -lc 'cd testgt2/mayor/rig && bd list --status=open'CMD: bash -lc 'cd testgt2/mayor/rig && bd ready'"
	cmds := parseOrchestratedCommands(in)
	if len(cmds) != 2 {
		t.Fatalf("want 2 commands, got %d: %v", len(cmds), cmds)
	}
	if !strings.Contains(cmds[0], "--status=open") || strings.Contains(cmds[0], "CMD:") {
		t.Fatalf("cmd[0] should end at open quote, got %q", cmds[0])
	}
}

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
	if err := validateOrchestratedArtifacts(task, dir, "myrig", "success", false, false, false, false); err == nil {
		t.Fatal("expected size validation error")
	}
	if err := os.WriteFile(path, make([]byte, 200), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateOrchestratedArtifacts(task, dir, "myrig", "success", false, false, false, false); err != nil {
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
	planBackend := "bash -lc 'cat > testgt2/mayor/rig/plan.md <<'EOF'\nBead 1: Implement backend/fizzbuzz.py\nEOF'"
	if err := validatePlanningCommand(planBackend, "testgt2"); err != nil {
		t.Fatalf("plan heredoc body may mention backend/: %v", err)
	}
	if err := validatePlanningCommand("gt bd add -t task -m hello", "testgt2"); err == nil {
		t.Fatal("gt bd add should be rejected")
	}
	if err := validatePlanningCommand(`bash -lc 'cd testgt2/mayor/rig && bd create --type task --title "Implement backend/fizzbuzz.py"'`, "testgt2"); err != nil {
		t.Fatalf("bd create with backend/ in title should be allowed: %v", err)
	}
	if err := validatePlanningCommand("head -n 40 testgt2/mayor/rig/architecture.md", "testgt2"); err != nil {
		t.Fatalf("head architecture should be allowed: %v", err)
	}
	if err := validatePlanningCommand("cat > testgt2/mayor/rig/backend/foo.py <<'EOF'", "testgt2"); err == nil {
		t.Fatal("writing backend file should be rejected")
	}
}

func TestValidatePlanningArtifacts(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "testgt2", "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := validatePlanningArtifacts(dir, "testgt2", false, false); err == nil {
		t.Fatal("expected error without plan and beads")
	}
	if err := os.WriteFile(filepath.Join(rigDir, "plan.md"), make([]byte, 250), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validatePlanningArtifacts(dir, "testgt2", false, false); err == nil {
		t.Fatal("expected error without successful bd create")
	}
	if err := validatePlanningArtifacts(dir, "testgt2", true, true); err == nil {
		t.Fatal("expected error when commands failed")
	}
	if err := validatePlanningArtifacts(dir, "testgt2", false, true); err != nil {
		t.Fatalf("plan + beads should pass: %v", err)
	}
}

func TestValidateImplementationCommand(t *testing.T) {
	bad := []string{
		"gt bd list -t implementation -s open",
		"gt bd claim impl-1",
		"gt bead close 101",
		"```bash\ncmd\n```",
		"<<EOF > foo",
	}
	for _, cmd := range bad {
		if err := validateImplementationCommand(cmd, "testgt2"); err == nil {
			t.Fatalf("expected reject: %q", cmd)
		}
	}
	ok := []string{
		`bash -lc 'cd testgt2/mayor/rig && bd list --status=open'`,
		`bash -lc 'cd testgt2/mayor/rig && bd update hq-abc --status=in_progress'`,
		`cat > testgt2/mayor/rig/backend/fizzbuzz.py <<'EOF'`,
		`bash -lc 'cd testgt2/mayor/rig && git add backend && git commit -m "Implement x"'`,
	}
	bad = append(bad, "git push origin main", "git add .", "git add -A", "git add typescript")
	for _, cmd := range ok {
		if err := validateImplementationCommand(cmd, "testgt2"); err != nil {
			t.Fatalf("expected allow %q: %v", cmd, err)
		}
	}
}

func TestOrchestratedArtifactAutoOutcome_planningRequiresBeads(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "testgt2", "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "plan.md"), make([]byte, 250), 0644); err != nil {
		t.Fatal(err)
	}
	task := &orchestrator.Task{State: "planning", AllowedOutcomes: []string{"success", "failure"}}
	if _, _, ok := orchestratedArtifactAutoOutcome(task, dir, "testgt2", false, false); ok {
		t.Fatal("should not auto-complete without bd create success")
	}
	if _, _, ok := orchestratedArtifactAutoOutcome(task, dir, "testgt2", false, true); !ok {
		t.Fatal("should auto-complete with plan + bead create ok")
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
	if err := validateOrchestratedArtifacts(task, dir, "myrig", "success", false, false, false, false); err != nil {
		t.Fatalf("stale backend/*.py must not block design: %v", err)
	}
}

func TestValidateImplementationArtifacts(t *testing.T) {
	dir := t.TempDir()
	if err := validateImplementationArtifacts(dir, "testgt2", false, false); err == nil {
		t.Fatal("expected error without bd close")
	}
	if err := validateImplementationArtifacts(dir, "testgt2", true, true); err == nil {
		t.Fatal("expected error when commands failed")
	}
	if err := validateImplementationArtifacts(dir, "testgt2", false, true); err != nil {
		t.Fatalf("bd close ok should pass: %v", err)
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
	o, _, ok := orchestratedArtifactAutoOutcome(task, dir, "myrig", false, false)
	if !ok || o != "success" {
		t.Fatalf("want auto success, got ok=%v outcome=%q", ok, o)
	}
}
