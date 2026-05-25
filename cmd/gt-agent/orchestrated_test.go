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

func TestParseOrchestratedCommands_gluedDoubleQuoteCMD(t *testing.T) {
	in := `CMD: export BEADS_DIR=$GT_ROOT/testgt3/.beads && cd testgt3/mayor/rig && bd create "Implement linkshelf/go.mod" && bd create "Implement linkshelf/cmd/server/main.go"CMD: cd testgt3/mayor/rig/linkshelf && go mod tidy`
	cmds := parseOrchestratedCommands(in)
	if len(cmds) != 2 {
		t.Fatalf("want 2 commands, got %d: %v", len(cmds), cmds)
	}
	if strings.Contains(cmds[0], "go mod tidy") || strings.Contains(cmds[0], "CMD:") {
		t.Fatalf("cmd[0] should be bd create only: %q", cmds[0])
	}
	if !strings.Contains(cmds[1], "go mod tidy") {
		t.Fatalf("cmd[1]: %q", cmds[1])
	}
}

func TestParseOrchestratedCommands_dropsStandaloneEOF(t *testing.T) {
	in := "CMD: cat > plan.md <<'EOF'\n# Plan\nline\nEOF\nCMD: EOF\nCMD: wc -c plan.md"
	cmds := parseOrchestratedCommands(in)
	for _, c := range cmds {
		if strings.TrimSpace(c) == "EOF" {
			t.Fatalf("standalone EOF should be dropped, got %v", cmds)
		}
	}
}

func TestResponseHasUnterminatedHeredoc(t *testing.T) {
	truncated := "CMD: cat > plan.md <<'EOF'\n# Plan\nline two\nno terminator"
	if !responseHasUnterminatedHeredoc(truncated) {
		t.Fatal("expected truncated heredoc")
	}
	complete := "CMD: cat > plan.md <<'EOF'\n# Plan\nline two\nEOF\n"
	if responseHasUnterminatedHeredoc(complete) {
		t.Fatal("expected complete heredoc")
	}
}

func TestExtractBeadCreateTitle_quotedTitleInBashLc(t *testing.T) {
	cmd := `bash -lc 'export BEADS_DIR=$GT_ROOT/testgt3/.beads && cd testgt3/mayor/rig && bd create --type task --title "Implement linkshelf/go.mod per architecture" --description="Implement linkshelf/go.mod: see architecture.md"'`
	got := extractBeadCreateTitle(cmd)
	want := "Implement linkshelf/go.mod per architecture"
	if got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
	v := orchestrator.WorkflowValidation{
		BeadTitleContains: "Implement linkshelf/",
		RequiredFiles:     []string{"linkshelf/go.mod"},
	}
	if err := orchestrator.ValidateImplementBeadCreateTitle(got, v); err != nil {
		t.Fatalf("ValidateImplementBeadCreateTitle: %v", err)
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

func TestStripOutcomeLines_preservesGoBracesInsideHeredoc(t *testing.T) {
	t.Parallel()
	in := "CMD: cat > f.go <<'EOF'\npackage p\n\ntype T struct {\n\tx int\n}\n\nfunc F() {}\nEOF\n{\"outcome\":\"success\"}\n"
	out := stripOutcomeLinesForCmdParse(in)
	if !strings.Contains(out, "}\n\nfunc F") && !strings.Contains(out, "}\nfunc F") {
		t.Fatalf("heredoc closing braces stripped:\n%s", out)
	}
	if strings.Contains(out, `"outcome"`) {
		t.Fatalf("outcome json should be stripped after heredoc: %q", out)
	}
}

func TestIsOrchestratedOutcomeLine_goBraces(t *testing.T) {
	t.Parallel()
	if isOrchestratedOutcomeLine("}") || isOrchestratedOutcomeLine("{") {
		t.Fatal("bare braces are valid Go heredoc lines, not outcome JSON")
	}
	if !isOrchestratedOutcomeLine(`{"outcome":"success"}`) {
		t.Fatal("single-line outcome JSON should match")
	}
	if !isOrchestratedOutcomeLine(`  "outcome": "success",`) {
		t.Fatal("outcome field line should match")
	}
}

// writeMinimalDesignSPEC creates SPEC.md with no HTTP/store contract (passes design alignment checks).
func writeMinimalDesignSPEC(t *testing.T, rigDir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte("# Test rig\n\nFixture SPEC without HTTP API or store symbols.\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestValidateOrchestratedArtifacts_design(t *testing.T) {
	dir := t.TempDir()
	rig := filepath.Join(dir, "myrig", "mayor", "rig")
	if err := os.MkdirAll(rig, 0755); err != nil {
		t.Fatal(err)
	}
	writeMinimalDesignSPEC(t, rig)
	path := filepath.Join(rig, "architecture.md")
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	task := &orchestrator.Task{Hooks: orchestrator.StateHooks{Artifacts: "design"}}
	runner := newStateRunner(task, dir, "myrig")
	runner.track.designArchWritten = true
	if err := runner.validateArtifacts("success"); err == nil {
		t.Fatal("expected size validation error")
	}
	if err := os.WriteFile(path, make([]byte, 200), 0644); err != nil {
		t.Fatal(err)
	}
	if err := runner.validateArtifacts("success"); err != nil {
		t.Fatalf("aligned arch with SPEC should pass design validation: %v", err)
	}
}

// TestValidateDesignArtifacts_architectureMustMatchSPEC ensures design success is blocked when
// architecture.md drifts from SPEC (regression for planner loops on planning validation).
func TestValidateDesignArtifacts_architectureMustMatchSPEC(t *testing.T) {
	dir := t.TempDir()
	rig := "linkshelf"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	spec := `# Link Shelf MVP
| GET | / | 200, serve web/index.html | — |
| GET | /static/{file} | 200, file under web/ | 404 |
| GET | /api/links | 200, JSON array | — |
| POST | /api/links | 201 | 400 |
| DELETE | /api/links/{id} | 204 | 404 |

## Store
` + "```go\nfunc InitSchema(db *sql.DB) error\nfunc List(ctx context.Context) ([]Link, error)\nfunc Create(ctx context.Context, title, url string) (Link, error)\nfunc Delete(ctx context.Context, id int64) error\n```" + `

module linkshelf
`
	// Architect-style drift: /web/*, Store struct, InitDB, GetLinks — must fail at design, not planning.
	misalignedArch := strings.Repeat("# Architecture\n", 1) + `
| GET | /web/* | static assets |
Handlers use GetLinks, DeleteLink, NewHandler(store.Store). InitDB(db). type Store struct { DB *sql.DB }
` + strings.Repeat("detail line about packages and acceptance.\n", 40)

	alignedArch := `# Architecture for linkshelf

## HTTP
| GET | / | serve web/index.html |
| GET | /static/{file} | file under web/ |
| GET | /api/links | JSON list |
| POST | /api/links | create link |
| DELETE | /api/links/{id} | delete link |

## Store
Package-level List, Create, Delete, InitSchema on var DB — no Store struct.
` + strings.Repeat("Per-file layout and acceptance mapping for required_files.\n", 35)

	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	v := orchestrator.WorkflowValidation{
		LayoutRoot:            "linkshelf",
		MinArchitectureBytes:  200,
		BeadTitleContains:     "Implement ",
	}

	writeArch := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}

	writeArch(misalignedArch)
	err := validateDesignArtifacts(dir, rig, true, v)
	if err == nil {
		t.Fatal("expected design validation error for misaligned architecture")
	}
	if !strings.Contains(err.Error(), "align architecture.md with SPEC.md") {
		t.Fatalf("want design gate error, got: %v", err)
	}
	for _, frag := range []string{"/web", "GetLinks", "Store struct", "InitDB"} {
		if !strings.Contains(err.Error(), frag) {
			t.Errorf("error should mention %q: %v", frag, err)
		}
	}

	writeArch(alignedArch)
	if err := validateDesignArtifacts(dir, rig, true, v); err != nil {
		t.Fatalf("aligned architecture should pass design validation: %v", err)
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

func TestParseOrchestratedCommands_markdownFencedCMDNoColon(t *testing.T) {
	in := "```CMD\nexport BEADS_DIR=$GT_ROOT/testgt3/.beads && cd testgt3/mayor/rig && bd list --status=closed --limit=0\n```\n"
	cmds := parseOrchestratedCommands(in)
	if len(cmds) != 1 {
		t.Fatalf("want 1 cmd, got %d: %v", len(cmds), cmds)
	}
	if !strings.Contains(cmds[0], "bd list --status=closed") {
		t.Fatalf("got %q", cmds[0])
	}
}

func TestParseOrchestratedCommands_heredocPreservesGoClosingBraces(t *testing.T) {
	t.Parallel()
	in := `CMD: cd mockrig/mayor/rig && cat > linkshelf/internal/store/store.go <<'EOF'
package store

type Store interface {
	Get() string
}

func New() Store { return nil }
EOF
CMD: cd mockrig/mayor/rig/linkshelf && go build ./internal/store/...
{"outcome":"success","summary":"done"}`
	cmds := parseOrchestratedCommands(in)
	if len(cmds) < 1 {
		t.Fatalf("want heredoc cmd, got %v", cmds)
	}
	if !strings.Contains(cmds[0], "}\n\nfunc New") && !strings.Contains(cmds[0], "}\nfunc New") {
		t.Fatalf("closing brace line stripped from heredoc:\n%s", cmds[0])
	}
}

func TestRunOrchestratedCommand_heredocWritesGoWithClosingBraces(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "mockrig", "mayor", "rig", "linkshelf", "internal", "store")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	cmd := "export GT_ROOT=" + dir + " && cd mockrig/mayor/rig && cat > linkshelf/internal/store/store.go <<'EOF'\n" +
		"package store\n\ntype S struct {\n\tx int\n}\n\nfunc NewS() *S { return &S{} }\nEOF"
	env := []string{"GT_ROOT=" + dir, "HOME=" + dir, "PATH=/usr/bin:/bin"}
	if _, err := runOrchestratedCommand(cmd, dir, "", env, 0); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(rigDir, "store.go"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if !strings.Contains(body, "}\n\nfunc NewS") && !strings.Contains(body, "}\nfunc NewS") {
		t.Fatalf("written file missing closing braces:\n%s", body)
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
	if err := validateQACommand(`if [ "$API_RESPONSE" != "[]" ]; then echo fail; fi`, "mockrig", v); err == nil {
		t.Fatal("expected reject shell if-block in QA")
	}
	if err := validateQACommand(`bd update <identified-bead-id> --status=open`, "mockrig", v); err == nil {
		t.Fatal("expected reject placeholder bead ID in QA")
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
	fixed, ok := rewriteBackendPathAfterCD(cmd, "mockrig", "")
	if !ok || strings.Contains(fixed, "mockrig/mayor/rig/backend") {
		t.Fatalf("want backend/ relative path, got ok=%v cmd=%q", ok, fixed)
	}
}

func TestRewriteLayoutPathAfterCD_linkshelf(t *testing.T) {
	cmd := `cd mockrig/mayor/rig && cat > mockrig/mayor/rig/linkshelf/web/app.js <<'EOF'`
	fixed, ok := rewriteBackendPathAfterCD(cmd, "mockrig", "linkshelf")
	if !ok || strings.Contains(fixed, "mockrig/mayor/rig/linkshelf") {
		t.Fatalf("want linkshelf/ relative path, got ok=%v cmd=%q", ok, fixed)
	}
}

func TestRewriteHallucinatedAbsoluteTownRoot(t *testing.T) {
	town := t.TempDir()
	cmd := "cd /home/ubuntu/gt/testgt3/mayor/rig/linkshelf && go mod tidy"
	fixed, ok := rewriteOrchestratedRigPlaceholders(cmd, town, "testgt3")
	if !ok {
		t.Fatal("expected rewrite")
	}
	if strings.Contains(fixed, "/home/ubuntu/gt") {
		t.Fatalf("hallucinated path should be gone: %q", fixed)
	}
	if !strings.Contains(fixed, "testgt3/mayor/rig/linkshelf") {
		t.Fatalf("want relative rig path: %q", fixed)
	}
}

func TestRewriteHallucinatedHomeGT(t *testing.T) {
	town := t.TempDir()
	cmd := "cd ~/gt/testgt3/mayor/rig/linkshelf && go mod tidy"
	fixed, ok := rewriteOrchestratedRigPlaceholders(cmd, town, "testgt3")
	if !ok {
		t.Fatal("expected rewrite")
	}
	if strings.Contains(fixed, "~testgt3") || strings.Contains(fixed, "~/gt") {
		t.Fatalf("must not break ~/gt into ~rig: %q", fixed)
	}
	if !strings.Contains(fixed, "testgt3/mayor/rig/linkshelf") {
		t.Fatalf("want relative path: %q", fixed)
	}
}

func TestRewriteOrchestratedRigPlaceholders(t *testing.T) {
	cmd := `export BEADS_DIR=$GT_ROOT/RIG/.beads && cd RIG/mayor/rig && bd create --type task --title "x"`
	fixed, ok := rewriteOrchestratedRigPlaceholders(cmd, t.TempDir(), "mockrig")
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
		"cd linkshelf && go test ./...",
		"cd mockrig/mayor/rig/linkshelf && go run ./cmd/server",
		"curl -sf http://127.0.0.1:8080/api/links",
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
	planGitProse := `export BEADS_DIR=$GT_ROOT/finally/.beads && cd finally/mayor/rig && cat > plan.md <<'EOF'
### fi-ye7: db/.gitkeep
- Acceptance: File exists; commits to repo; directory persists in Git history.
EOF`
	if err := validatePlanningCommand(planGitProse, "finally"); err != nil {
		t.Fatalf("plan heredoc must allow git prose (.gitkeep, commits): %v", err)
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
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), make([]byte, 500), 0644); err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.WriteAlignedPlanningDocsForTest(rigDir); err != nil {
		t.Fatal(err)
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
	listOpenImplementationBeadsHook = func(_, _ string) ([]orchestrator.PlanBead, error) {
		return []orchestrator.PlanBead{{ID: "mo-1", Title: "Implement mock/main.go per architecture"}}, nil
	}
	defer func() { listOpenImplementationBeadsHook = nil }()
	if err := validatePlanningArtifacts(dir, "mockrig", false, true, false, v); err != nil {
		t.Fatalf("plan + implement beads should pass: %v", err)
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
	ok = append(ok, `bash -lc 'cd mockrig/mayor/rig && git add -A linkshelf && git commit -m "Implement x"'`)
	for _, cmd := range ok {
		if err := validateImplementationCommand(cmd, "mockrig"); err != nil {
			t.Fatalf("expected allow %q: %v", cmd, err)
		}
	}
}

func TestValidateProjectSetupCommand_rejectsImplementationWrites(t *testing.T) {
	v := orchestrator.WorkflowValidation{LayoutRoot: "linkshelf", QAVerifyCommand: "go test ./..."}
	cmds := []string{
		"cd testgt3/mayor/rig && touch linkshelf/web/app.js",
		"cd testgt3/mayor/rig/linkshelf && cat > cmd/server/main.go <<'EOF'",
		"mkdir -p linkshelf/internal/api",
		"go get -u <specific-dependency-if-any>",
		"cd testgt3/mayor/rig/linkshelf && go build ./...",
		"cd testgt3/mayor/rig/linkshelf && go run ./cmd/server",
		`bd create "x"CMD: go mod tidy`,
	}
	for _, cmd := range cmds {
		if err := validateProjectSetupCommand(cmd, "testgt3", v); err == nil {
			t.Fatalf("expected reject: %q", cmd)
		}
	}
	ok := []string{
		"cd testgt3/mayor/rig/linkshelf && go mod init linkshelf",
		"cd testgt3/mayor/rig/linkshelf && go mod tidy",
		"mkdir -p linkshelf",
	}
	for _, cmd := range ok {
		if err := validateProjectSetupCommand(cmd, "testgt3", v); err != nil {
			t.Fatalf("expected allow %q: %v", cmd, err)
		}
	}
}

func TestValidateProjectSetupArtifacts_rejectsHadCmdFailureForCustomGo(t *testing.T) {
	v := orchestrator.WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go run ./cmd/server",
		TestRunner:      "custom",
	}
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "mockrig", "mayor", "rig", "linkshelf")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "go.mod"), []byte("module linkshelf\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateProjectSetupArtifacts(dir, "mockrig", true, false, v); err == nil {
		t.Fatal("expected failure when commands failed even with custom Go verify")
	}
}

func TestOrchestratedArtifactAutoOutcome_planningRequiresBeads(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "mockrig", "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := orchestrator.WriteAlignedPlanningDocsForTest(rigDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "plan.md"), append(make([]byte, 250), '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	task := &orchestrator.Task{
		AllowedOutcomes: []string{"success", "failure"},
		Hooks:           orchestrator.StateHooks{Artifacts: "planning"},
	}
	runner := newStateRunner(task, dir, "mockrig")
	if _, _, ok := runner.tryAutoOutcome(); ok {
		t.Fatal("should not auto-complete without bd create success")
	}
	listOpenImplementationBeadsHook = func(_, _ string) ([]orchestrator.PlanBead, error) {
		return []orchestrator.PlanBead{{ID: "mo-1", Title: "Implement mock/main.go per architecture"}}, nil
	}
	defer func() { listOpenImplementationBeadsHook = nil }()
	runner.track.beadCreateOK = true
	if _, _, ok := runner.tryAutoOutcome(); !ok {
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
	writeMinimalDesignSPEC(t, rigDir)
	task := &orchestrator.Task{Hooks: orchestrator.StateHooks{Artifacts: "design"}}
	runner := newStateRunner(task, dir, "myrig")
	runner.track.designArchWritten = true
	if err := runner.validateArtifacts("success"); err != nil {
		t.Fatalf("stale backend/*.py must not block design: %v", err)
	}
}

func writeImplementationBackendFiles(t *testing.T, townRoot, rig string) {
	t.Helper()
	backend := filepath.Join(townRoot, rig, "mayor", "rig", "backend")
	if err := os.MkdirAll(backend, 0755); err != nil {
		t.Fatal(err)
	}
	body := []byte("def run():\n    return 1\n\nif __name__ == '__main__':\n    run()\n")
	for _, name := range []string{"fizzbuzz.py", "main.py", "test_fizzbuzz.py"} {
		if err := os.WriteFile(filepath.Join(backend, name), body, 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestValidateImplementationArtifacts_openBeadsRemain(t *testing.T) {
	countOpenMatchingBeadsHook = func(_, _, _ string) (int, error) { return 2, nil }
	defer func() { countOpenMatchingBeadsHook = nil }()
	v := orchestrator.DefaultWorkflowValidation()
	v.BeadTitleContains = "Implement backend/"
	err := validateImplementationArtifacts(t.TempDir(), "mockrig", false, true, true, v)
	if err == nil || !strings.Contains(err.Error(), "open implement bead") {
		t.Fatalf("want open-bead error, got %v", err)
	}
}

func TestValidateImplementationArtifacts(t *testing.T) {
	countOpenMatchingBeadsHook = func(_, _, _ string) (int, error) { return 0, nil }
	defer func() { countOpenMatchingBeadsHook = nil }()

	dir := t.TempDir()
	v := orchestrator.DefaultWorkflowValidation()
	if err := validateImplementationArtifacts(dir, "mockrig", false, false, false, v); err == nil {
		t.Fatal("expected error without bd close")
	}
	if err := validateImplementationArtifacts(dir, "mockrig", true, true, false, v); err == nil {
		t.Fatal("expected error when commands failed")
	}
	writeImplementationBackendFiles(t, dir, "mockrig")
	v.RequiredFiles = []string{"backend/fizzbuzz.py", "backend/main.py", "backend/test_fizzbuzz.py"}
	v.MinImplementationFileBytes = 1
	v.MinSubstantiveLines = 1
	if err := validateImplementationArtifacts(dir, "mockrig", false, true, false, v); err != nil {
		t.Fatalf("bd close ok with backend files should pass: %v", err)
	}
	if err := validateImplementationArtifacts(dir, "mockrig", false, false, false, v); err != nil {
		t.Fatalf("all closed on disk without bd close this session should pass: %v", err)
	}
	vGo := v
	vGo.QAVerifyCommand = "cd linkshelf && go test ./..."
	if err := validateImplementationArtifacts(dir, "mockrig", false, true, false, vGo); err == nil || !strings.Contains(err.Error(), "verification must pass") {
		t.Fatalf("go profile requires green session verify before success: %v", err)
	}
	countOpenMatchingBeadsHook = func(_, _, _ string) (int, error) { return 1, nil }
	if err := validateImplementationArtifacts(dir, "mockrig", false, true, false, vGo); err == nil {
		t.Fatal("expected error when open implement beads remain without session verify")
	}
	countOpenMatchingBeadsHook = func(_, _, _ string) (int, error) { return 0, nil }
}

func TestValidateImplementationBeadFileWrite_rejectsClosedPath(t *testing.T) {
	dir := t.TempDir()
	v := orchestrator.WorkflowValidation{
		LayoutRoot:        "linkshelf",
		BeadTitleContains: "Implement ",
		RequiredFiles: []string{
			"linkshelf/internal/api/handlers.go",
			"linkshelf/cmd/server/main.go",
		},
	}
	orchestrator.ListImplementBeadsByStatusHook = func(_, _ string, _ orchestrator.WorkflowValidation, status string) ([]orchestrator.PlanBead, error) {
		switch status {
		case "closed":
			return []orchestrator.PlanBead{{
				ID:    "te-h",
				Title: "Implement linkshelf/internal/api/handlers.go per architecture",
			}}, nil
		case "in_progress":
			return []orchestrator.PlanBead{{
				ID:    "te-main",
				Title: "Implement linkshelf/cmd/server/main.go per architecture",
			}}, nil
		default:
			return nil, nil
		}
	}
	t.Cleanup(func() { orchestrator.ListImplementBeadsByStatusHook = nil })
	cmd := `cd mockrig/mayor/rig && cat > linkshelf/internal/api/handlers.go <<'EOF'
package api
EOF`
	err := validateImplementationBeadFileWrite(cmd, dir, "mockrig", "te-main", v)
	if err == nil {
		t.Fatal("expected reject write to closed-only path while active bead is main")
	}
	if !strings.Contains(err.Error(), "closed") {
		t.Fatalf("want closed-path error, got %v", err)
	}
}

func TestValidateImplementationBeadFileWrite_rejectsFullHeredocAllowsSed(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	layout := "linkshelf"
	path := filepath.Join(dir, rig, "mayor", "rig", layout, "internal", "store")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "package store\n\n" + strings.Repeat("type Link struct { ID int; URL string }\nfunc (s *Store) GetAll() ([]Link, error) { return nil, nil }\n", 8)
	if err := os.WriteFile(filepath.Join(path, "store.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	v := orchestrator.DefaultWorkflowValidation()
	v.LayoutRoot = layout
	v.RequiredFiles = []string{layout + "/internal/store/store.go"}
	v.BeadTitleContains = "Implement "
	orchestrator.ListImplementBeadsByStatusHook = func(_, _ string, _ orchestrator.WorkflowValidation, status string) ([]orchestrator.PlanBead, error) {
		if status == "in_progress" {
			return []orchestrator.PlanBead{{
				ID:    "te-store",
				Title: "Implement linkshelf/internal/store/store.go per architecture",
			}}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { orchestrator.ListImplementBeadsByStatusHook = nil })
	heredoc := `cd mockrig/mayor/rig && cat > linkshelf/internal/store/store.go <<'EOF'
package store
EOF`
	errHeredoc := validateImplementationBeadFileWrite(heredoc, dir, rig, "te-store", v)
	if errHeredoc == nil {
		t.Fatal("expected reject full heredoc on existing file")
	}
	if !strings.Contains(errHeredoc.Error(), "sed") {
		t.Fatalf("err = %v", errHeredoc)
	}
	sedCmd := `cd mockrig/mayor/rig && sed -i 's/x/y/' linkshelf/internal/store/store.go`
	if err := validateImplementationBeadFileWrite(sedCmd, dir, rig, "te-store", v); err != nil {
		t.Fatalf("sed should be allowed: %v", err)
	}
	patchCmd := `cd mockrig/mayor/rig && patch -p0 linkshelf/internal/store/store.go < fix.patch`
	if err := validateImplementationBeadFileWrite(patchCmd, dir, rig, "te-store", v); err != nil {
		t.Fatalf("patch should be allowed: %v", err)
	}

	mainDir := filepath.Join(dir, rig, "mayor", "rig", layout, "cmd", "server")
	if err := os.MkdirAll(mainDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mainBody := "package main\n\n" + strings.Repeat("func handleGetLinks() {}\n", 10)
	if err := os.WriteFile(filepath.Join(mainDir, "main.go"), []byte(mainBody), 0o644); err != nil {
		t.Fatal(err)
	}
	vMain := v
	vMain.RequiredFiles = append(vMain.RequiredFiles, layout+"/cmd/server/main.go")
	orchestrator.ListImplementBeadsByStatusHook = func(_, _ string, _ orchestrator.WorkflowValidation, status string) ([]orchestrator.PlanBead, error) {
		if status == "in_progress" {
			return []orchestrator.PlanBead{{
				ID:    "te-main",
				Title: "Implement linkshelf/cmd/server/main.go per architecture",
			}}, nil
		}
		return nil, nil
	}
	mainHeredoc := `cd mockrig/mayor/rig && cat > linkshelf/cmd/server/main.go <<'EOF'
package main
EOF`
	if err := validateImplementationBeadFileWrite(mainHeredoc, dir, rig, "te-main", vMain); err != nil {
		t.Fatalf("cmd/main heredoc should be allowed: %v", err)
	}
}

func TestValidateImplementationCommand_oneInProgressBead(t *testing.T) {
	dir := t.TempDir()
	cmd := `bd update tg-abc --status=in_progress`
	if err := validateImplementationCommand(`bd update <identified-bead-id> --status=open`, "mockrig"); err == nil {
		t.Fatal("expected reject placeholder bead ID")
	}
	if err := validateImplementationCommand(`bd update BEAD_ID --status=in_progress`, "mockrig"); err == nil {
		t.Fatal("expected reject BEAD_ID template")
	}
	if err := validateImplementationCommandWithState(cmd, dir, "mockrig", "tg-xyz", orchestrator.DefaultWorkflowValidation(), false); err == nil {
		t.Fatal("expected reject second in_progress bead")
	}
	if err := validateImplementationCommandWithState(cmd, dir, "mockrig", "tg-abc", orchestrator.DefaultWorkflowValidation(), false); err != nil {
		t.Fatalf("same bead should be allowed: %v", err)
	}
}

func TestValidatePythonImplementationCommand(t *testing.T) {
	dir := t.TempDir()
	v := orchestrator.WorkflowValidation{
		RequiredFiles:   []string{"backend/requirements.txt"},
		QAVerifyCommand: "cd backend && python3 -m pytest -q",
	}
	if err := validatePythonImplementationCommand("python3 -m pip install -r backend/requirements.txt", dir, "mockrig", "", v, true); err == nil {
		t.Fatal("expected pip install rejected in implementation")
	}
	if err := validatePythonImplementationCommand("bd close tg-1", dir, "mockrig", "tg-1", v, false); err == nil {
		t.Fatal("expected bd close without verify rejected")
	}
}

func TestPythonImplementationVerifyAcceptance(t *testing.T) {
	v := orchestrator.WorkflowValidation{
		LayoutRoot:      "tasklist",
		PythonVenvDir:   ".venv",
		RequiredFiles:   []string{"tasklist/requirements.txt"},
		QAVerifyCommand: "pytest -v",
	}
	impl := orchestrator.ImplementationVerifyCommandForBead(v, t.TempDir(), "tasklist/requirements.txt")
	cmd := "cd testgt5/mayor/rig && test -x .venv/bin/python3 && .venv/bin/python3 -c 'import pytest'"
	if !commandMatchesQAVerify(cmd, impl) && !pythonVerifyCommandMatches(cmd, impl) {
		t.Fatalf("import pytest should satisfy per-bead verify %q", impl)
	}
}

func TestValidateGoImplementationCommand(t *testing.T) {
	dir := t.TempDir()
	mayor := filepath.Join(dir, "mockrig", "mayor", "rig")
	if err := os.MkdirAll(filepath.Join(mayor, "linkshelf"), 0755); err != nil {
		t.Fatal(err)
	}
	v := orchestrator.WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go test ./...",
	}
	town := dir
	rig := "mockrig"
	if err := validateGoImplementationCommand(`cat > linkshelf/go.sum <<'EOF'`, town, rig, mayor, "", v, true); err == nil {
		t.Fatal("expected reject go.sum heredoc")
	}
	if err := validateGoImplementationCommand("cd linkshelf && rm -f go.mod go.sum", town, rig, mayor, "", v, true); err == nil {
		t.Fatal("expected reject rm go.mod")
	}
	if err := validateGoImplementationCommand("bd close tg-1", town, rig, mayor, "", v, false); err == nil {
		t.Fatal("expected reject bd close without verify")
	}
	if err := validateGoImplementationCommand("bd close tg-1", town, rig, mayor, "", v, true); err != nil {
		t.Fatalf("bd close with verify ok should pass: %v", err)
	}
	if err := validateGoImplementationCommand("cd linkshelf && go run ./cmd/server", town, rig, mayor, "", v, true); err == nil {
		t.Fatal("expected reject go run before main.go exists")
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
	writeMinimalDesignSPEC(t, rigDir)
	task := &orchestrator.Task{
		AllowedOutcomes: []string{"success", "failure"},
		Hooks:           orchestrator.StateHooks{Artifacts: "design"},
	}
	runner := newStateRunner(task, dir, "myrig")
	if _, _, ok := runner.tryAutoOutcome(); ok {
		t.Fatal("stale architecture.md must not auto-complete design without write this run")
	}
	runner.track.designArchWritten = true
	o, _, ok := runner.tryAutoOutcome()
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
	planTask := &orchestrator.Task{
		Validation: orchestrator.DefaultWorkflowValidation(),
		Hooks:      orchestrator.StateHooks{Env: orchestrator.StateEnvHooks{BeadsDir: true}},
	}
	env := newStateRunner(planTask, town, "mockrig").commandEnv(base)
	if got := envLookup(env, "BEADS_DIR"); got != rigBeads {
		t.Fatalf("planning BEADS_DIR = %q, want %q", got, rigBeads)
	}
	designTask := &orchestrator.Task{Validation: orchestrator.DefaultWorkflowValidation()}
	env = newStateRunner(designTask, town, "mockrig").commandEnv(base)
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
	layoutDir := filepath.Join(rigDir, "tasklist")
	if err := os.MkdirAll(layoutDir, 0755); err != nil {
		t.Fatal(err)
	}
	v := orchestrator.WorkflowValidation{
		LayoutRoot:    "tasklist",
		RequiredFiles: []string{"tasklist/requirements.txt"},
	}
	task := &orchestrator.Task{
		Validation: v,
		Hooks: orchestrator.StateHooks{
			Env: orchestrator.StateEnvHooks{BeadsDir: true, PythonVenv: "create"},
		},
	}
	env := newStateRunner(task, town, "mockrig").commandEnv(os.Environ())
	virt := envLookup(env, "VIRTUAL_ENV")
	if virt == "" {
		t.Fatal("missing VIRTUAL_ENV")
	}
	wantVenv := filepath.Join(rigDir, ".venv")
	virtAbs, err := filepath.Abs(virt)
	if err != nil {
		t.Fatal(err)
	}
	wantAbs, err := filepath.Abs(wantVenv)
	if err != nil {
		t.Fatal(err)
	}
	if virtAbs != wantAbs {
		t.Fatalf("VIRTUAL_ENV=%q want mayor/rig/.venv at %q (not under layout)", virt, wantAbs)
	}
	if strings.Contains(virt, filepath.Join("tasklist", ".venv")) {
		t.Fatalf("venv must not be under layout_root: %q", virt)
	}
	if _, err := os.Stat(filepath.Join(wantVenv, "bin", "python3")); err != nil {
		t.Fatalf("venv python missing under mayor/rig: %v", err)
	}
	py := envLookup(env, "GT_PYTHON3")
	if py == "" || !strings.Contains(py, filepath.Join("mayor", "rig", ".venv")) {
		t.Fatalf("GT_PYTHON3=%q", py)
	}
}

func TestValidateQACommand_rejectsLayoutWrites(t *testing.T) {
	t.Parallel()
	v := linkshelfWebProfile()
	cases := []string{
		`cd mockrig/mayor/rig && sed -i 's|/static/app.js|/app.js|g' linkshelf/web/index.html`,
		`cd mockrig/mayor/rig && cat > linkshelf/web/index.html <<'EOF'\n<html></html>\nEOF`,
		`cd mockrig/mayor/rig/linkshelf && tee web/index.html <<'EOF'\n<html></html>\nEOF`,
	}
	for _, cmd := range cases {
		if err := validateQACommand(cmd, "mockrig", v); err == nil {
			t.Fatalf("expected reject for %q", cmd)
		} else if !strings.Contains(err.Error(), "must not modify") {
			t.Fatalf("unexpected error for %q: %v", cmd, err)
		}
	}
	if err := validateQACommand(`cd mockrig/mayor/rig && head -n 5 linkshelf/web/index.html`, "mockrig", v); err != nil {
		t.Fatalf("read-only head should pass: %v", err)
	}
}

func TestValidateQAArtifacts_failureAllowsFailedSmoke(t *testing.T) {
	v := orchestrator.DefaultWorkflowValidation()
	v.BeadTitleContains = "Implement "
	err := validateQAArtifacts(t.TempDir(), "r", "failure", true, true, false, false, v)
	if err != nil {
		t.Fatalf("failure outcome should pass with bd list only (failed smoke is the finding): %v", err)
	}
	err = validateQAArtifacts(t.TempDir(), "r", "all_passed", true, true, false, false, v)
	if err == nil || !strings.Contains(err.Error(), "failed commands") {
		t.Fatalf("all_passed should reject hadCmdFailure, got %v", err)
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
