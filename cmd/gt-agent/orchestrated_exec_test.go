package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestScrubOrphanHeredocDelimiterLines(t *testing.T) {
	in := "export X=1\ncat > plan.md <<'EOF'\nbody\nEOF\nEOF\nwc -c plan.md"
	got := scrubOrphanHeredocDelimiterLines(in)
	if strings.Contains(got, "\nEOF\nEOF") {
		t.Fatalf("duplicate EOF not scrubbed: %q", got)
	}
	if !strings.Contains(got, "wc -c plan.md") {
		t.Fatalf("got %q", got)
	}
}

func TestBenignPlanningShellNoise_standaloneEOF(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "EOF")
	if err := cmd.Run(); err != nil {
		if !benignPlanningShellNoise("EOF", err) {
			t.Fatalf("expected benign for standalone EOF: %v", err)
		}
	} else {
		t.Fatal("expected exit error")
	}
}

func TestScrubOrphanBashLcQuoteLines(t *testing.T) {
	in := "cd x && cat > f.go <<'EOF'\npackage main\nEOF\n'\nwc -c f.go"
	got := scrubOrphanBashLcQuoteLines(in)
	if strings.Contains(got, "\n'\n") || strings.HasSuffix(got, "'") {
		t.Fatalf("orphan wrapper quote must be removed: %q", got)
	}
	if !strings.Contains(got, "wc -c f.go") {
		t.Fatalf("got %q", got)
	}
}

func TestPrepareOrchestratedScript_scrubsOrphanQuote(t *testing.T) {
	in := "bash -lc 'cd mockrig/mayor/rig && cat > plan.md <<'EOF'\n# Plan\nEOF\n'"
	got := prepareOrchestratedScript(in)
	if strings.Contains(got, "\n'\n") {
		t.Fatalf("prepare should drop orphan quote line: %q", got)
	}
	scriptPath := filepath.Join(t.TempDir(), "t.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/bash\nset -euo pipefail\n"+got+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("/bin/bash", "-n", scriptPath).CombinedOutput(); err != nil {
		t.Fatalf("bash -n syntax check: %v\n%s", err, out)
	}
}

func linkshelfSmokeTestRig(t *testing.T) (townRoot, rig string, v orchestrator.WorkflowValidation) {
	t.Helper()
	townRoot = t.TempDir()
	rig = "testgt3"
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	spec := "| GET | /api/links | 200, JSON array | — |\n| POST | /api/links | 201 | — |\n"
	if err := os.MkdirAll(filepath.Join(rigDir, "linkshelf", "web"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	v = linkshelfWebProfile()
	return townRoot, rig, v
}

func pythonAPISmokeTestRig(t *testing.T) (townRoot, rig string, v orchestrator.WorkflowValidation) {
	t.Helper()
	townRoot = t.TempDir()
	rig = "pyrig"
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	spec := "| GET | /api/items | 200, JSON array when empty |\n| POST | /api/items | 201 |\n"
	serverDoc := "## Runtime smoke server\n.venv/bin/python3 -m uvicorn backend.app:app --host 127.0.0.1 --port 8080\n"
	if err := os.MkdirAll(filepath.Join(rigDir, "backend"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(spec+serverDoc), 0644); err != nil {
		t.Fatal(err)
	}
	v = orchestrator.WorkflowValidation{
		LayoutRoot:      "backend",
		QAVerifyCommand: "python3 -m pytest -q",
		RequiredFiles:   []string{"backend/app.py"},
		PythonVenvDir:   ".venv",
	}
	return townRoot, rig, v
}

func TestSimplifyDevServerSmoke_pythonUvicorn(t *testing.T) {
	townRoot, rig, v := pythonAPISmokeTestRig(t)
	in := `cd pyrig/mayor/rig/backend && .venv/bin/python3 -m uvicorn app:app --port 8080 & sleep 4`
	got, ok := simplifyDevServerSmoke(in, townRoot, rig, v)
	if !ok {
		t.Fatal("expected profile-derived smoke for uvicorn CMD")
	}
	if strings.Contains(got, "go run") {
		t.Fatalf("python smoke must not use go run: %q", got)
	}
	if !strings.Contains(got, "uvicorn") || !strings.Contains(got, ".gt-smoke.pid") {
		t.Fatalf("want uvicorn background probe: %q", got)
	}
	if !strings.Contains(got, "/api/items") || !strings.Contains(got, `= "[]"`) {
		t.Fatalf("want doc-derived API curls: %q", got)
	}
	if !strings.Contains(got, "for _i in") {
		t.Fatalf("want root health poll: %q", got)
	}
	if !orchestrator.IsProfileDerivedSmokeCommand(got) {
		t.Fatalf("expected GT_SMOKE profile script: %q", got)
	}
	if !isQARuntimeSmokeCommandOK(got, townRoot, rig, v) {
		t.Fatalf("rewritten python smoke should qualify as QA runtime smoke: %q", got)
	}
}

func TestNormalizeGoDevServerSmokeCommand(t *testing.T) {
	townRoot, rig, v := linkshelfSmokeTestRig(t)
	in := `cd testgt3/mayor/rig && cd linkshelf && go mod tidy && go build ./... && go run ./cmd/server & sleep 2 && curl -s http://localhost:8080 > /dev/null && pkill -f "go run ./cmd/server"`
	got, ok := normalizeGoDevServerSmokeCommand(in, townRoot, rig, v)
	if !ok {
		t.Fatal("expected rewrite")
	}
	if strings.Contains(got, "pkill") || strings.Contains(got, "go build ./...") {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(got, ".gt-smoke.pid") || !strings.Contains(got, "--connect-timeout") {
		t.Fatalf("want subshell pid file and fast curl probes in smoke rewrite: %q", got)
	}
	if !strings.Contains(got, "kill ${_gtsrv}") {
		t.Fatalf("want smoke to kill background server so shell returns: %q", got)
	}
	if strings.Contains(got, "go mod tidy") || strings.Contains(got, "wait ${_gtsrv}") {
		t.Fatalf("want short smoke without tidy or wait: %q", got)
	}
}

func TestSimplifyGoDevServerSmokeCommand_shortProbe(t *testing.T) {
	townRoot, rig, v := linkshelfSmokeTestRig(t)
	in := `cd testgt3/mayor/rig && cd linkshelf && go mod tidy && go build ./... && go run ./cmd/server & sleep 2 && curl -sf http://127.0.0.1:8080/ && curl -sf http://127.0.0.1:8080/api/links`
	got, ok := simplifyGoDevServerSmoke(in, townRoot, rig, v)
	if !ok {
		t.Fatal("expected simplify")
	}
	if strings.Contains(got, "go mod tidy") || strings.Contains(got, "go build") {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(got, ".gt-smoke.pid") || !strings.Contains(got, "for _i in") || !strings.Contains(got, "/api/links") {
		t.Fatalf("want profile-derived smoke with API from SPEC: %q", got)
	}
	if !strings.Contains(got, "POST") || !strings.Contains(got, `= "[]"`) {
		t.Fatalf("want POST and empty-array GET from SPEC table: %q", got)
	}
	if strings.Contains(got, "sleep 6") {
		t.Fatalf("want curl poll not fixed sleep 6: %q", got)
	}
	for _, step := range strings.Split(got, "&&") {
		step = strings.TrimSpace(step)
		if strings.Contains(step, "curl") && strings.Contains(step, "|| true") {
			t.Fatalf("curl step must not use || true (masks failure): %q", step)
		}
	}
	if !strings.Contains(got, "testgt3/mayor/rig/linkshelf") {
		t.Fatalf("want deepest cd path in smoke: %q", got)
	}
	if !strings.HasPrefix(got, "set -euo pipefail;") {
		t.Fatalf("want fail-fast bash prefix: %q", got)
	}
}

func TestSimplifyGoDevServerSmoke_goRunWithoutCurl(t *testing.T) {
	townRoot, rig, v := linkshelfSmokeTestRig(t)
	in := `cd testgt3/mayor/rig && cd linkshelf && go run ./cmd/server & sleep 4`
	got, ok := simplifyGoDevServerSmoke(in, townRoot, rig, v)
	if !ok {
		t.Fatal("expected profile probe when agent omits curl")
	}
	if !strings.Contains(got, ".gt-smoke.pid") || !strings.Contains(got, "/api/links") {
		t.Fatalf("want full background smoke probe, got %q", got)
	}
	if strings.Contains(got, "sleep 4") {
		t.Fatalf("probe should poll / with curl, not sleep: %q", got)
	}
}

func TestNormalizeGoDevServerSmokeCommand_syncGoRunUpgradesToProbe(t *testing.T) {
	townRoot, rig, v := linkshelfSmokeTestRig(t)
	in := `cd testgt3/mayor/rig/linkshelf && go run ./cmd/server`
	got, ok := normalizeGoDevServerSmokeCommand(in, townRoot, rig, v)
	if !ok {
		t.Fatal("expected rewrite for synchronous go run")
	}
	if !strings.HasPrefix(got, "set -euo pipefail;") || !strings.Contains(got, "go run ./cmd/server") {
		t.Fatalf("want profile background probe, got %q", got)
	}
	if strings.Contains(got, "sleep 4") {
		t.Fatalf("sync go run must not become sleep-only wait: %q", got)
	}
}

// TestStateRunner_rewriteCommand_qaGoRunSleepOnly is the regression from testgt3 QA logs:
// agent sent go run & sleep with no curls; must become profile probe, not sleep-only rewrite.
func TestStateRunner_rewriteCommand_qaGoRunSleepOnly(t *testing.T) {
	townRoot, rig, v := linkshelfSmokeTestRig(t)
	task := &orchestrator.Task{
		State:      "qa_review",
		Hooks:      orchestrator.StateHooks{Track: "qa"},
		Validation: v,
	}
	r := newStateRunner(task, townRoot, rig)
	in := `cd testgt3/mayor/rig/linkshelf && go run ./cmd/server & sleep 4`
	got := r.rewriteCommand(in)
	if strings.Contains(got, "sleep 4") && !strings.Contains(got, ".gt-smoke.pid") {
		t.Fatalf("must not leave sleep-only smoke, got %q", got)
	}
	if !strings.Contains(got, ".gt-smoke.pid") || !strings.Contains(got, "for _i in") {
		t.Fatalf("want profile-derived background+curl probe, got %q", got)
	}
	if !orchestrator.IsProfileDerivedSmokeCommand(got) {
		t.Fatalf("rewritten cmd should be profile-derived smoke: %q", got)
	}
	if !isQARuntimeSmokeCommandOK(got, r.townRoot, r.rig, r.v) {
		t.Fatalf("rewritten cmd should count as QA runtime smoke (runner v): %q", got)
	}
	if d := orchestratedCommandTimeoutForTrack("qa", got); d != 45*time.Second {
		t.Fatalf("qa timeout = %v, want 45s", d)
	}
}

func TestIsQARuntimeSmokeCommandOK_profileProbeWithoutAgentCurl(t *testing.T) {
	townRoot, rig, v := linkshelfSmokeTestRig(t)
	built, ok := simplifyGoDevServerSmoke(
		`cd testgt3/mayor/rig/linkshelf && go run ./cmd/server & sleep 4`,
		townRoot, rig, v,
	)
	if !ok {
		t.Fatal("expected profile probe")
	}
	if !isQARuntimeSmokeCommandOK(built, townRoot, rig, v) {
		t.Fatalf("profile probe should qualify as QA smoke: %q", built)
	}
	if isQARuntimeSmokeCommandOK("go run ./cmd/server & sleep 4", townRoot, rig, v) {
		t.Fatal("raw sleep-only go run must not qualify before rewrite")
	}
}

func TestWrapStrictBashSmoke(t *testing.T) {
	t.Parallel()
	in := "go run ./cmd/server & curl -sf http://127.0.0.1:8080/"
	got := wrapStrictBashSmoke(in)
	if !strings.HasPrefix(got, "set -euo pipefail;") {
		t.Fatalf("got %q", got)
	}
	if wrapStrictBashSmoke(got) != got {
		t.Fatal("should not double-prefix")
	}
}

func TestRewriteUnittestToWorkdir_skipsGoDevServerSmoke(t *testing.T) {
	cmd := "cd linkshelf && go run ./cmd/server & _gtsrv=$!; sleep 6; curl -sf http://127.0.0.1:8080/"
	v := orchestrator.WorkflowValidation{LayoutRoot: "linkshelf", QAVerifyCommand: "go test ./..."}
	if _, ok := rewriteUnittestToWorkdir(cmd, "testgt3", v); ok {
		t.Fatal("smoke must not get unittest workdir rewrite")
	}
}

func TestPrepareOrchestratedScript_normalizesEOF(t *testing.T) {
	in := "cat > plan.md <<'EOF'\nbody\nEOF\nEOF\n"
	got := prepareOrchestratedScript(in)
	if strings.Contains(got, "\nEOF\nEOF") {
		t.Fatalf("duplicate closer should be scrubbed: %q", got)
	}
	in = "cat > plan.md " + bashLcHeredocEOFMarker() + "\nbody\nEOF"
	got = prepareOrchestratedScript(in)
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
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), make([]byte, 480), 0644); err != nil {
		t.Fatal(err)
	}
	cmd := "export GT_ROOT=" + dir + " && cd mockrig/mayor/rig && cat > plan.md <<'EOF'\n# Implementation Plan\n" +
		strings.Repeat("x", 230) + "\nEOF"
	env := []string{"GT_ROOT=" + dir, "HOME=" + dir, "PATH=/usr/bin:/bin"}
	out, err := runOrchestratedCommand(cmd, dir, "", env, 0)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	info, err := os.Stat(filepath.Join(rigDir, "plan.md"))
	if err != nil {
		t.Fatal(err)
	}
	minPlan := orchestrator.EffectiveMinPlanBytes(rigDir, orchestrator.DefaultWorkflowValidation())
	if info.Size() < minPlan {
		t.Fatalf("plan.md size %d < min %d", info.Size(), minPlan)
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

func TestRewriteUnittestToWorkdir_pythonVenvAtMayorRig(t *testing.T) {
	v := orchestrator.WorkflowValidation{
		LayoutRoot:      "tasklist",
		RequiredFiles:   []string{"tasklist/requirements.txt"},
		QAVerifyCommand: "pytest -v",
	}
	verify := orchestrator.PythonImplementationVerifyCommandForBead(v, "", "tasklist/tasklist/__init__.py")
	cmd := verify
	fixed, ok := rewriteUnittestToWorkdir(cmd, "testgt5", v)
	if !ok {
		t.Fatal("expected rewrite for compileall verify")
	}
	if strings.Contains(fixed, "mayor/rig/tasklist/.venv") || strings.Contains(fixed, "tasklist/.venv") {
		t.Fatalf("must not cd into layout for venv: %q", fixed)
	}
	want := "cd testgt5/mayor/rig && .venv/bin/python3 -m compileall -q tasklist/tasklist/__init__.py"
	if fixed != want {
		t.Fatalf("got %q want %q", fixed, want)
	}
}

func TestRewriteUnittestToWorkdir_bareLayoutCD(t *testing.T) {
	v := orchestrator.WorkflowValidation{
		LayoutRoot:      "tasklist",
		QAVerifyCommand: "cd tasklist && go test ./...",
		TestRunner:      "custom",
	}
	cmd := "cd tasklist && go mod tidy"
	fixed, ok := rewriteUnittestToWorkdir(cmd, "testgt4", v)
	if !ok {
		t.Fatal("expected rewrite")
	}
	want := "cd testgt4/mayor/rig/tasklist && go mod tidy"
	if fixed != want {
		t.Fatalf("got %q want %q", fixed, want)
	}
}

func TestRewriteUnittestToWorkdir_mayorRigCDIntoModule(t *testing.T) {
	town := t.TempDir()
	rig := "testgt3"
	mayor := filepath.Join(town, rig, "mayor", "rig")
	if err := os.MkdirAll(mayor, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mayor, "go.mod"), []byte("module linkshelf\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(town)

	v := orchestrator.WorkflowValidation{LayoutRoot: "linkshelf", TestRunner: "custom", QAVerifyCommand: "go build ./..."}
	cmd := "cd testgt3/mayor/rig && go build ./..."
	fixed, ok := rewriteUnittestToWorkdir(cmd, rig, v)
	if !ok {
		t.Fatal("expected rewrite")
	}
	want := "cd testgt3/mayor/rig && go build ./..."
	if fixed != want {
		t.Fatalf("got %q want %q (flat module at mayor/rig)", fixed, want)
	}
}
