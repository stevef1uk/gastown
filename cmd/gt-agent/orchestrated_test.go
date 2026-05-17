package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestParseOrchestratedCommands_gluedQuoteCMD(t *testing.T) {
	in := "CMD: bash -lc 'cd mockrig/mayor/rig && bd list --status=open'CMD: bash -lc 'cd mockrig/mayor/rig && bd ready'"
	cmds := parseOrchestratedCommands(in)
	if len(cmds) != 2 {
		t.Fatalf("want 2 commands, got %d: %v", len(cmds), cmds)
	}
	if !strings.Contains(cmds[0], "--status=open") || strings.Contains(cmds[0], "CMD:") {
		t.Fatalf("cmd[0] should end at open quote, got %q", cmds[0])
	}
}

func TestParseOrchestratedCommands_gluedPlannerLine(t *testing.T) {
	in := "CMD: ls -R mockrig/mayor/rig/CMD: cat mockrig/mayor/rig/SPEC.mdCMD: cat mockrig/mayor/rig/architecture.md{\"outcome\":\"failure\"}"
	cmds := parseOrchestratedCommands(in)
	if len(cmds) != 3 {
		t.Fatalf("want 3 commands, got %d: %v", len(cmds), cmds)
	}
	if cmds[0] != "ls -R mockrig/mayor/rig" {
		t.Fatalf("cmd[0]: %q", cmds[0])
	}
	if cmds[1] != "cat mockrig/mayor/rig/SPEC.md" {
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
	if err := validateOrchestratedArtifacts(task, dir, "myrig", "success", true, false, false, false, false, false, false, false, false, false, false, false, false); err == nil {
		t.Fatal("expected size validation error")
	}
	if err := os.WriteFile(path, make([]byte, 200), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateOrchestratedArtifacts(task, dir, "myrig", "success", true, false, false, false, false, false, false, false, false, false, false, false, false); err != nil {
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
	if err := validatePlanReviewCommand("bd delete te-ajz --force", "mockrig"); err == nil {
		t.Fatal("expected reject")
	}
}

func TestParseOrchestratedCommands_stripsTOOLCALLSAndFakeLs(t *testing.T) {
	in := "CMD: ls -la mockrig/mayor/rig/backend/\n```[TOOL_CALLS]```\ntotal 24\ndrwxr-xr-x 2 user user 4096 Jun 25 10:00 .\n"
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
	in := "prose\n```CMD:\ncd mockrig/mayor/rig && bd list --status=closed\n```\n"
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
	if err := validateQACommand("cat /workspace/mockrig/src/foo.py", "mockrig", v); err == nil {
		t.Fatal("expected reject")
	}
	if err := validateQACommand("cd mockrig/mayor/rig && python3 -m unittest backend.test_fizzbuzz", "mockrig", v); err != nil {
		t.Fatalf("unittest should be allowed: %v", err)
	}
	if err := validateQACommand("python3 -m unittest backend.test_fizzbuzz -v", "mockrig", v); err == nil {
		t.Fatal("unittest without cd should be rejected")
	}
	if err := validateQACommand("python3 -m unittest backend.test_fizzbuzz -v | grep ok", "mockrig", v); err == nil {
		t.Fatal("unittest piped to grep should be rejected")
	}
}

func TestValidateDesignCommand_forbidsImplementation(t *testing.T) {
	cases := []string{
		"git -C mockrig/mayor/rig commit -m x",
		"mkdir -p mockrig/mayor/rig/backend",
		"cat > mockrig/mayor/rig/backend/fizzbuzz.py <<'EOF'",
		"python3 mockrig/mayor/rig/backend/main.py",
	}
	for _, cmd := range cases {
		if err := validateDesignCommand(cmd, "mockrig"); err == nil {
			t.Fatalf("expected reject for %q", cmd)
		}
	}
	if err := validateDesignCommand("cat > mockrig/mayor/rig/architecture.md <<'EOF'", "mockrig"); err != nil {
		t.Fatalf("architecture heredoc should be allowed: %v", err)
	}
	if err := validateDesignCommand("head -n 60 mockrig/mayor/rig/SPEC.md", "mockrig"); err != nil {
		t.Fatalf("head SPEC should be allowed: %v", err)
	}
	heredoc := "cat > mockrig/mayor/rig/architecture.md <<'EOF'\n# Architecture\nDescribe backend/fizzbuzz.py here.\nEOF"
	if err := validateDesignCommand(heredoc, "mockrig"); err != nil {
		t.Fatalf("architecture heredoc may mention backend/: %v", err)
	}
	heredocWithProse := "cat > mockrig/mayor/rig/architecture.md <<'EOF'\n# Architecture\nRun python3 -m unittest backend.test_fizzbuzz.\nThe workflow uses gt bd for CI.\nEOF"
	if err := validateDesignCommand(heredocWithProse, "mockrig"); err != nil {
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
	cmd := `bash -lc 'cd mockrig/mayor/rig && cat > mockrig/mayor/rig/backend/fizzbuzz.py <<EOF'`
	fixed, ok := rewriteBackendPathAfterCD(cmd, "mockrig")
	if !ok || strings.Contains(fixed, "mockrig/mayor/rig/backend") {
		t.Fatalf("want backend/ relative path, got ok=%v cmd=%q", ok, fixed)
	}
}

func TestRewriteOrchestratedRigPlaceholders(t *testing.T) {
	cmd := `export BEADS_DIR=$GT_ROOT/RIG/.beads && cd RIG/mayor/rig && bd create --type task --title "x"`
	fixed, ok := rewriteOrchestratedRigPlaceholders(cmd, "mockrig")
	if !ok {
		t.Fatal("expected rewrite")
	}
	if strings.Contains(fixed, "RIG/") {
		t.Fatalf("RIG placeholder should be gone: %q", fixed)
	}
	if !strings.Contains(fixed, "mockrig/mayor/rig") || !strings.Contains(fixed, "mockrig/.beads") {
		t.Fatalf("unexpected: %q", fixed)
	}
}

func TestRewritePlanMDPathAfterCD(t *testing.T) {
	cmd := `bash -lc 'cd mockrig/mayor/rig && cat > mockrig/mayor/rig/plan.md <<'"'"'EOF'"'"'
# Plan
EOF
'`
	fixed, ok := rewritePlanMDPathAfterCD(cmd, "mockrig")
	if !ok {
		t.Fatal("expected rewrite")
	}
	if strings.Contains(fixed, "mockrig/mayor/rig/plan.md") {
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
		if err := validatePlanningCommand(cmd, "mockrig"); err == nil {
			t.Fatalf("expected reject for %q", cmd)
		}
	}
	plan := "cat > mockrig/mayor/rig/plan.md <<'EOF'\n# Plan\nEOF"
	if err := validatePlanningCommand(plan, "mockrig"); err != nil {
		t.Fatalf("plan heredoc should be allowed: %v", err)
	}
	planBackend := "bash -lc 'cat > mockrig/mayor/rig/plan.md <<'EOF'\nBead 1: Implement backend/fizzbuzz.py\nEOF'"
	if err := validatePlanningCommand(planBackend, "mockrig"); err != nil {
		t.Fatalf("plan heredoc body may mention backend/: %v", err)
	}
	if err := validatePlanningCommand("gt bd add -t task -m hello", "mockrig"); err == nil {
		t.Fatal("gt bd add should be rejected")
	}
	if err := validatePlanningCommand(`bash -lc 'cd mockrig/mayor/rig && bd create --type task --title "Implement backend/fizzbuzz.py"'`, "mockrig"); err != nil {
		t.Fatalf("bd create with backend/ in title should be allowed: %v", err)
	}
	if err := validatePlanningCommand("head -n 40 mockrig/mayor/rig/architecture.md", "mockrig"); err != nil {
		t.Fatalf("head architecture should be allowed: %v", err)
	}
	if err := validatePlanningCommand("cat > mockrig/mayor/rig/backend/foo.py <<'EOF'", "mockrig"); err == nil {
		t.Fatal("writing backend file should be rejected")
	}
}

func TestValidatePlanningArtifacts(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "mockrig", "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	v := orchestrator.DefaultWorkflowValidation()
	if err := validatePlanningArtifacts(dir, "mockrig", false, false, false, v); err == nil {
		t.Fatal("expected error without plan and beads")
	}
	if err := os.WriteFile(filepath.Join(rigDir, "plan.md"), make([]byte, 250), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validatePlanningArtifacts(dir, "mockrig", false, false, false, v); err == nil {
		t.Fatal("expected error without bead create/repair")
	}
	if err := validatePlanningArtifacts(dir, "mockrig", true, true, false, v); err == nil {
		t.Fatal("expected error when commands failed")
	}
	if err := validatePlanningArtifacts(dir, "mockrig", false, true, false, v); err != nil {
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
		if err := validateImplementationCommand(cmd, "mockrig"); err == nil {
			t.Fatalf("expected reject: %q", cmd)
		}
	}
	ok := []string{
		`bash -lc 'cd mockrig/mayor/rig && bd list --status=open'`,
		`bash -lc 'cd mockrig/mayor/rig && bd update hq-abc --status=in_progress'`,
		`cat > mockrig/mayor/rig/backend/fizzbuzz.py <<'EOF'`,
		`bash -lc 'cd mockrig/mayor/rig && git add backend && git commit -m "Implement x"'`,
	}
	bad = append(bad, "git push origin main", "git add .", "git add -A", "git add typescript")
	for _, cmd := range ok {
		if err := validateImplementationCommand(cmd, "mockrig"); err != nil {
			t.Fatalf("expected allow %q: %v", cmd, err)
		}
	}
}

func TestOrchestratedArtifactAutoOutcome_planningRequiresBeads(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "mockrig", "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "plan.md"), make([]byte, 250), 0644); err != nil {
		t.Fatal(err)
	}
	task := &orchestrator.Task{State: "planning", AllowedOutcomes: []string{"success", "failure"}}
	if _, _, ok := orchestratedArtifactAutoOutcome(task, dir, "mockrig", false, false, false, false); ok {
		t.Fatal("should not auto-complete without bd create success")
	}
	if _, _, ok := orchestratedArtifactAutoOutcome(task, dir, "mockrig", false, false, true, false); !ok {
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
	if err := validateOrchestratedArtifacts(task, dir, "myrig", "success", true, false, false, false, false, false, false, false, false, false, false, false, false); err != nil {
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
	if err := validateImplementationArtifacts(dir, "mockrig", false, false, false, v); err == nil {
		t.Fatal("expected error without bd close")
	}
	if err := validateImplementationArtifacts(dir, "mockrig", true, true, false, v); err == nil {
		t.Fatal("expected error when commands failed")
	}
	writeImplementationBackendFiles(t, dir, "mockrig")
	if err := validateImplementationArtifacts(dir, "mockrig", false, true, false, v); err != nil {
		t.Fatalf("bd close ok with backend files should pass: %v", err)
	}
	vGo := v
	vGo.QAVerifyCommand = "cd linkshelf && go test ./..."
	if err := validateImplementationArtifacts(dir, "mockrig", false, true, false, vGo); err == nil {
		t.Fatal("expected error without verify when qa_verify_command set")
	}
	if err := validateImplementationArtifacts(dir, "mockrig", false, true, true, vGo); err != nil {
		t.Fatalf("bd close + verify should pass: %v", err)
	}
}

func TestValidateImplementationCommand_oneInProgressBead(t *testing.T) {
	cmd := `bd update tg-abc --status=in_progress`
	if err := validateImplementationCommandWithState(cmd, "mockrig", "tg-xyz"); err == nil {
		t.Fatal("expected reject second in_progress bead")
	}
	if err := validateImplementationCommandWithState(cmd, "mockrig", "tg-abc"); err != nil {
		t.Fatalf("same bead should be allowed: %v", err)
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
	rigBeads := filepath.Join(town, "mockrig", ".beads")
	for _, d := range []string{townBeads, rigBeads} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	base := []string{"BEADS_DIR=" + townBeads, "GT_ROOT=" + town}
	env := orchestratedCommandEnv(town, "mockrig", "planning", base, orchestrator.DefaultWorkflowValidation())
	if got := envLookup(env, "BEADS_DIR"); got != rigBeads {
		t.Fatalf("planning BEADS_DIR = %q, want %q", got, rigBeads)
	}
	env = orchestratedCommandEnv(town, "mockrig", "design", base, orchestrator.DefaultWorkflowValidation())
	if got := envLookup(env, "BEADS_DIR"); got != townBeads {
		t.Fatalf("design should not override BEADS_DIR: got %q", got)
	}
}

func TestOrchestratedCommandEnv_createsPythonVenv(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	town := t.TempDir()
	rigDir := filepath.Join(town, "mockrig", "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	v := orchestrator.WorkflowValidation{RequiredFiles: []string{"backend/requirements.txt"}}
	env := orchestratedCommandEnv(town, "mockrig", "implementation", os.Environ(), v)
	virt := envLookup(env, "VIRTUAL_ENV")
	if virt == "" {
		t.Fatal("missing VIRTUAL_ENV")
	}
	if !strings.HasSuffix(virt, filepath.Join("mockrig", "mayor", "rig", ".venv")) {
		t.Fatalf("VIRTUAL_ENV=%q", virt)
	}
	py := envLookup(env, "GT_PYTHON3")
	if py == "" || !strings.Contains(py, ".venv") {
		t.Fatalf("GT_PYTHON3=%q", py)
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
