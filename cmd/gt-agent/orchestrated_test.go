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

func TestStripOutcomeLines_multilineJSONAfterHeredoc(t *testing.T) {
	in := "CMD: cat > plan.md <<'EOF'\n# Plan\nEOF\n{\n  \"outcome\": \"success\",\n  \"summary\": \"architecture.md written\"\n}\n"
	out := stripOutcomeLinesForCmdParse(in)
	cmds := parseOrchestratedCommands(in)
	if strings.Contains(out, "\"outcome\"") {
		t.Fatalf("multiline outcome json should be stripped from filtered text: %q", out)
	}
	if len(cmds) != 1 {
		t.Fatalf("want 1 cmd, got %d: %v", len(cmds), cmds)
	}
	if strings.Contains(cmds[0], "outcome") {
		t.Fatalf("outcome json must not be in shell script: %q", cmds[0])
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
	if err := validateOrchestratedArtifacts(task, dir, "myrig", "success", true, false, false, false, false, false, false, false, false, false, false, false); err == nil {
		t.Fatal("expected size validation error")
	}
	if err := os.WriteFile(path, make([]byte, 200), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateOrchestratedArtifacts(task, dir, "myrig", "success", true, false, false, false, false, false, false, false, false, false, false, false); err != nil {
		t.Fatal(err)
	}
}

func TestParseOrchestratedJSON_usesLastOutcome(t *testing.T) {
	in := `Assessment
{"outcome":"failure","summary":"duplicate beads"}
After fixes
{"outcome":"success","summary":"beads ok"}`
	o, s, ok := parseOrchestratedJSON(in)
	if !ok || o != "success" || s != "beads ok" {
		t.Fatalf("got outcome=%q summary=%q ok=%v", o, s, ok)
	}
}

func TestUpdateOrchestratedRetryAfterComplete_clearsOnStateChange(t *testing.T) {
	state := AgentState{
		OrchestratedRetry: &OrchestratedRetry{WorkflowID: "wf-1", State: "plan_review", Summary: "old"},
	}
	task := &orchestrator.Task{WorkflowID: "wf-1", State: "plan_review"}
	updateOrchestratedRetryAfterComplete(&state, task, "failure", "dup beads", "", "planning")
	if state.OrchestratedRetry != nil {
		t.Fatalf("expected retry cleared on cross-step transition, got %+v", state.OrchestratedRetry)
	}
}

func TestValidatePlanReviewCommand_rejectsDelete(t *testing.T) {
	if err := validatePlanReviewCommand("bd delete te-ajz --force", "testgt2"); err == nil {
		t.Fatal("expected reject")
	}
}

func TestParseOrchestratedCommands_stripsTOOLCALLSAndFakeLs(t *testing.T) {
	in := "CMD: ls -la testgt2/mayor/rig/backend/\n```[TOOL_CALLS]```\ntotal 24\ndrwxr-xr-x 2 user user 4096 Jun 25 10:00 .\n"
	cmds := parseOrchestratedCommands(in)
	if len(cmds) != 1 {
		t.Fatalf("want 1 cmd, got %d: %v", len(cmds), cmds)
	}
	if !strings.Contains(cmds[0], "ls -la") {
		t.Fatalf("got %q", cmds[0])
	}
	if strings.Contains(cmds[0], "TOOL_CALLS") || strings.Contains(cmds[0], "total 24") {
		t.Fatalf("junk in cmd: %q", cmds[0])
	}
}

func TestParseOrchestratedCommands_markdownFencedCMD(t *testing.T) {
	in := "prose\n```CMD:\ncd testgt2/mayor/rig && bd list --status=closed\n```\n"
	cmds := parseOrchestratedCommands(in)
	if len(cmds) != 1 {
		t.Fatalf("want 1 cmd from fenced CMD, got %d: %v", len(cmds), cmds)
	}
	if !strings.Contains(cmds[0], "bd list") {
		t.Fatalf("got %q", cmds[0])
	}
}

func TestValidateQACommand_rejectsWorkspace(t *testing.T) {
	v := orchestrator.DefaultWorkflowValidation()
	if err := validateQACommand("cat /workspace/testgt2/src/foo.py", "testgt2", v); err == nil {
		t.Fatal("expected reject")
	}
	if err := validateQACommand("cd testgt2/mayor/rig && python3 -m unittest backend.test_fizzbuzz", "testgt2", v); err != nil {
		t.Fatalf("unittest should be allowed: %v", err)
	}
	if err := validateQACommand("python3 -m unittest backend.test_fizzbuzz -v", "testgt2", v); err == nil {
		t.Fatal("unittest without cd should be rejected")
	}
	if err := validateQACommand("python3 -m unittest backend.test_fizzbuzz -v | grep ok", "testgt2", v); err == nil {
		t.Fatal("unittest piped to grep should be rejected")
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
	heredocWithProse := "cat > testgt2/mayor/rig/architecture.md <<'EOF'\n# Architecture\nRun python3 -m unittest backend.test_fizzbuzz.\nThe workflow uses gt bd for CI.\nEOF"
	if err := validateDesignCommand(heredocWithProse, "testgt2"); err != nil {
		t.Fatalf("architecture heredoc may mention python3/gt bd in prose: %v", err)
	}
}

func TestNormalizeGluedEOFCMD(t *testing.T) {
	in := "EOF'CMD: bash -lc 'echo hi'"
	out := normalizeGluedCMDMarkers(in)
	if !strings.Contains(out, "EOF\nCMD:") {
		t.Fatalf("want EOF newline CMD, got %q", out)
	}
}

func TestRewriteBackendPathAfterCD(t *testing.T) {
	cmd := `bash -lc 'cd testgt2/mayor/rig && cat > testgt2/mayor/rig/backend/fizzbuzz.py <<EOF'`
	fixed, ok := rewriteBackendPathAfterCD(cmd, "testgt2")
	if !ok || strings.Contains(fixed, "testgt2/mayor/rig/backend") {
		t.Fatalf("want backend/ relative path, got ok=%v cmd=%q", ok, fixed)
	}
}

func TestRewriteOrchestratedRigPlaceholders(t *testing.T) {
	cmd := `export BEADS_DIR=$GT_ROOT/RIG/.beads && cd RIG/mayor/rig && bd create --type task --title "x"`
	fixed, ok := rewriteOrchestratedRigPlaceholders(cmd, "testgt2")
	if !ok {
		t.Fatal("expected rewrite")
	}
	if strings.Contains(fixed, "RIG/") {
		t.Fatalf("RIG placeholder should be gone: %q", fixed)
	}
	if !strings.Contains(fixed, "testgt2/mayor/rig") || !strings.Contains(fixed, "testgt2/.beads") {
		t.Fatalf("unexpected: %q", fixed)
	}
}

func TestRewritePlanMDPathAfterCD(t *testing.T) {
	cmd := `bash -lc 'cd testgt2/mayor/rig && cat > testgt2/mayor/rig/plan.md <<'"'"'EOF'"'"'
# Plan
EOF
'`
	fixed, ok := rewritePlanMDPathAfterCD(cmd, "testgt2")
	if !ok {
		t.Fatal("expected rewrite")
	}
	if strings.Contains(fixed, "testgt2/mayor/rig/plan.md") {
		t.Fatalf("should rewrite to plan.md only: %q", fixed)
	}
	if !strings.Contains(fixed, "cat > plan.md") {
		t.Fatalf("missing cat > plan.md: %q", fixed)
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
	v := orchestrator.DefaultWorkflowValidation()
	if err := validatePlanningArtifacts(dir, "testgt2", false, false, false, v); err == nil {
		t.Fatal("expected error without plan and beads")
	}
	if err := os.WriteFile(filepath.Join(rigDir, "plan.md"), make([]byte, 250), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validatePlanningArtifacts(dir, "testgt2", false, false, false, v); err == nil {
		t.Fatal("expected error without bead create/repair")
	}
	if err := validatePlanningArtifacts(dir, "testgt2", true, true, false, v); err == nil {
		t.Fatal("expected error when commands failed")
	}
	if err := validatePlanningArtifacts(dir, "testgt2", false, true, false, v); err != nil {
		t.Fatalf("plan + bd create should pass: %v", err)
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
	if _, _, ok := orchestratedArtifactAutoOutcome(task, dir, "testgt2", false, false, false, false); ok {
		t.Fatal("should not auto-complete without bd create success")
	}
	if _, _, ok := orchestratedArtifactAutoOutcome(task, dir, "testgt2", false, false, true, false); !ok {
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
	if err := validateOrchestratedArtifacts(task, dir, "myrig", "success", true, false, false, false, false, false, false, false, false, false, false, false); err != nil {
		t.Fatalf("stale backend/*.py must not block design: %v", err)
	}
}

func writeImplementationBackendFiles(t *testing.T, townRoot, rig string) {
	t.Helper()
	backend := filepath.Join(townRoot, rig, "mayor", "rig", "backend")
	if err := os.MkdirAll(backend, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"fizzbuzz.py", "main.py", "test_fizzbuzz.py"} {
		if err := os.WriteFile(filepath.Join(backend, name), []byte("pass\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestValidateImplementationArtifacts(t *testing.T) {
	dir := t.TempDir()
	v := orchestrator.DefaultWorkflowValidation()
	if err := validateImplementationArtifacts(dir, "testgt2", false, false, v); err == nil {
		t.Fatal("expected error without bd close")
	}
	if err := validateImplementationArtifacts(dir, "testgt2", true, true, v); err == nil {
		t.Fatal("expected error when commands failed")
	}
	writeImplementationBackendFiles(t, dir, "testgt2")
	if err := validateImplementationArtifacts(dir, "testgt2", false, true, v); err != nil {
		t.Fatalf("bd close ok with backend files should pass: %v", err)
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
	if _, _, ok := orchestratedArtifactAutoOutcome(task, dir, "myrig", false, false, false, false); ok {
		t.Fatal("stale architecture.md must not auto-complete design without write this run")
	}
	o, _, ok := orchestratedArtifactAutoOutcome(task, dir, "myrig", true, false, false, false)
	if !ok || o != "success" {
		t.Fatalf("want auto success after write this run, got ok=%v outcome=%q", ok, o)
	}
}

func TestOrchestratedCommandEnv_pinsRigBeadsForPlanning(t *testing.T) {
	town := t.TempDir()
	townBeads := filepath.Join(town, ".beads")
	rigBeads := filepath.Join(town, "testgt2", ".beads")
	for _, d := range []string{townBeads, rigBeads} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	base := []string{"BEADS_DIR=" + townBeads, "GT_ROOT=" + town}
	env := orchestratedCommandEnv(town, "testgt2", "planning", base)
	if got := envLookup(env, "BEADS_DIR"); got != rigBeads {
		t.Fatalf("planning BEADS_DIR = %q, want %q", got, rigBeads)
	}
	env = orchestratedCommandEnv(town, "testgt2", "design", base)
	if got := envLookup(env, "BEADS_DIR"); got != townBeads {
		t.Fatalf("design should not override BEADS_DIR: got %q", got)
	}
}

func envLookup(env []string, key string) string {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return e[len(prefix):]
		}
	}
	return ""
}
