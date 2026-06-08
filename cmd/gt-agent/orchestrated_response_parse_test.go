package main

import (
	"strings"
	"testing"
)

func TestParseOrchestratedCommands_skipsStandaloneEndEditLine(t *testing.T) {
	t.Parallel()
	in := `CMD: export BEADS_DIR=$GT_ROOT/testgt3/.beads && cd testgt3/mayor/rig && bd update te-93y --status=in_progress
---END EDIT---
CMD: cd linkshelf && go test -count=1 ./internal/api/...`
	cmds := parseOrchestratedCommands(in)
	if len(cmds) != 2 {
		t.Fatalf("cmds = %#v", cmds)
	}
	for _, c := range cmds {
		if strings.Contains(c, "END EDIT") {
			t.Fatalf("unexpected END EDIT in cmd: %q", c)
		}
	}
	if strings.Contains(cmds[0], "\n---END EDIT---") {
		t.Fatalf("glued END EDIT in first cmd: %q", cmds[0])
	}
}

func TestPreprocessOrchestratedResponse_gluedEndEditCMD(t *testing.T) {
	in := `EDIT: linkshelf/internal/store/store_test.go
<<<<<<< SEARCH
store, err := NewStore()
=======
store, err := New()
>>>>>>> REPLACE
---END EDIT---CMD: cd rig/mayor/rig && go test ./linkshelf/internal/store -count=1 -v`
	got := preprocessOrchestratedResponse(in)
	if !strings.Contains(got, "---END EDIT---\nCMD:") && !strings.Contains(got, ">>>>>>> REPLACE\nCMD:") {
		t.Fatalf("want split END EDIT from CMD, got %q", got)
	}
	cmds := parseOrchestratedCommands(in)
	if len(cmds) < 1 {
		t.Fatalf("cmds = %v", cmds)
	}
	if strings.Contains(cmds[0], "END EDIT") {
		t.Fatalf("first cmd should be shell only: %q", cmds[0])
	}
}

func TestSanitizeOrchestratedShellCommand_goTestEllipsisGluedProse(t *testing.T) {
	t.Parallel()
	in := "cd testgt3/mayor/rig/linkshelf && go test -count=1 ./internal/store/...We need to run command."
	fixed, changed := sanitizeOrchestratedShellCommand(in)
	if !changed {
		t.Fatal("expected change")
	}
	if strings.Contains(fixed, "...We") || strings.Contains(fixed, "need") {
		t.Fatalf("prose still glued: %q", fixed)
	}
	if fixed != "cd testgt3/mayor/rig/linkshelf && go test -count=1 ./internal/store/..." {
		t.Fatalf("got %q", fixed)
	}
}

func TestStripNativeToolBlocksForCmdParse(t *testing.T) {
	t.Parallel()
	in := "---END WRITE---\n" +
		"CMD: cd linkshelf && go test -count=1 ./internal/store/... -run 'TestInitSchema'\n" +
		`{"outcome":"success","summary":"Schema file and test implemented, verification passed"}` + "\n" +
		"WRITE: linkshelf/internal/store/schema_test.go\n" +
		"package store\n" +
		"CMD: ls linkshelf/internal/store/schema_test.go\n"
	out := stripNativeToolBlocksForCmdParse(in)
	if strings.Contains(out, "package store") || strings.Contains(out, "WRITE:") {
		t.Fatalf("WRITE body should be stripped: %q", out)
	}
	cmds := parseOrchestratedCommands(in)
	if len(cmds) != 2 {
		t.Fatalf("want 2 cmds, got %d: %v", len(cmds), cmds)
	}
	if strings.Contains(cmds[0], "outcome") || strings.Contains(cmds[1], "WRITE") {
		t.Fatalf("cmds=%v", cmds)
	}
}

func TestStripGluedOutcomeJSONFromLine_leadingColon(t *testing.T) {
	t.Parallel()
	in := `cd testgt3/mayor/rig/linkshelf && go test -count=1 ./internal/store/... -run 'TestInitSchema'
:"success","summary":"Schema file and test implemented, verification passed"}`
	got := stripGluedOutcomeJSONFromLine(in)
	if strings.Contains(got, "success") || strings.Contains(got, "summary") {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(got, "go test") {
		t.Fatalf("verify cmd should remain: %q", got)
	}
}

func TestParseOrchestratedCommands_goTestGluedProseAndJSON(t *testing.T) {
	t.Parallel()
	in := "CMD: cd testgt3/mayor/rig/linkshelf && go test -count=1 ./internal/store/...We need to run command.CMD: export BEADS_DIR=$GT_ROOT/testgt3/.beads && cd testgt3/mayor/rig && bd close te-thd{\"outcome\":\"success\"}"
	cmds := parseOrchestratedCommands(in)
	if len(cmds) < 2 {
		t.Fatalf("want >=2 commands, got %d: %v", len(cmds), cmds)
	}
	if strings.Contains(cmds[0], "We need") {
		t.Fatalf("cmd[0]: %q", cmds[0])
	}
	if strings.Contains(cmds[1], "{") || strings.Contains(cmds[1], "outcome") {
		t.Fatalf("cmd[1] should be bd close only: %q", cmds[1])
	}
}

func TestSanitizeBdListCommand_limitGluedWithProse(t *testing.T) {
	cmd := "export BEADS_DIR=x && cd testgt3/mayor/rig && bd list --limit=0We need to output result"
	fixed, changed := sanitizeBdListCommand(cmd)
	if !changed {
		t.Fatal("expected change")
	}
	if strings.Contains(fixed, "0We") {
		t.Fatalf("prose still glued to limit: %q", fixed)
	}
	if !strings.Contains(fixed, "--limit=0") {
		t.Fatalf("want --limit=0: %q", fixed)
	}
}

func TestSanitizeBdListCommand_stripsPastedOutput(t *testing.T) {
	cmd := "export BEADS_DIR=x && cd testgt3/mayor/rig && bd list --limit=0 --status=closed(no output) | grep -Fi 'Implement linkshelf/' || true"
	fixed, changed := sanitizeBdListCommand(cmd)
	if !changed {
		t.Fatal("expected change")
	}
	if strings.Contains(fixed, "(no output)") {
		t.Fatalf("output artifact still present: %q", fixed)
	}
	if !strings.Contains(fixed, "--status=closed") {
		t.Fatalf("want clean status flag: %q", fixed)
	}
}

func TestValidateBdCommandBeadID_rejectsNumericClose(t *testing.T) {
	err := validateBdCommandBeadID("bd close 12", "", "testgt3")
	if err == nil || !strings.Contains(err.Error(), "bare number") {
		t.Fatalf("err = %v", err)
	}
}

func TestIsBdInfrastructureFailure_detectsDoltMissing(t *testing.T) {
	out := `Error: failed to open database: database "testgt3" not found on Dolt server at 127.0.0.1:3307`
	if !isBdInfrastructureFailure(nil, out) {
		t.Fatal("expected dolt missing detection")
	}
}

func TestSanitizeNativeFileContent_stripsEOFAndFences(t *testing.T) {
	in := "```go\npackage store\n\nimport \"fmt\"\n```\nEOF\n"
	got := sanitizeNativeFileContent(in)
	if strings.Contains(got, "```") || strings.Contains(got, "EOF") {
		t.Fatalf("want clean Go only: %q", got)
	}
	if !strings.HasPrefix(got, "package store") {
		t.Fatalf("want package first: %q", got)
	}
}

func TestStripMarkdownCodeFencesFromSource_goWrite(t *testing.T) {
	in := "```go\npackage store\n\nimport \"fmt\"\n```\n"
	got := stripMarkdownCodeFencesFromSource(in)
	if strings.Contains(got, "```") {
		t.Fatalf("fences remain: %q", got)
	}
	if !strings.HasPrefix(got, "package store") {
		t.Fatalf("want package line first: %q", got)
	}
}

func TestStripMarkdownFencesInHeredocScripts_stripsEOFInBody(t *testing.T) {
	script := "cat > f.go <<'EOF'\npackage main\nEOF\nEOF\n"
	got := stripMarkdownFencesInHeredocScripts(script)
	const marker = "<<'EOF'\n"
	i := strings.Index(got, marker)
	if i < 0 {
		t.Fatalf("missing heredoc opener: %q", got)
	}
	rest := got[i+len(marker):]
	j := strings.Index(rest, "\nEOF\n")
	if j < 0 {
		t.Fatalf("missing heredoc close: %q", got)
	}
	body := rest[:j]
	if strings.TrimSpace(body) != "package main" {
		t.Fatalf("heredoc body should not contain stray EOF line: %q", body)
	}
}

func TestStripMarkdownFencesInHeredocScripts(t *testing.T) {
	script := "cat > f.go <<'EOF'\n```go\npackage main\n```\nEOF\n"
	got := stripMarkdownFencesInHeredocScripts(script)
	if strings.Contains(got, "```go") || strings.Contains(got, "\n```\nEOF") {
		t.Fatalf("fences in script: %q", got)
	}
	if !strings.Contains(got, "package main") {
		t.Fatalf("missing body: %q", got)
	}
}

func TestPreprocessOrchestratedResponse_gluedFenceWriteCMD(t *testing.T) {
	in := "WRITE: linkshelf/internal/store/schema.go\npackage store\n}\n```WRITE: linkshelf/internal/store/schema_test.go\npackage store\nCMD: go test ./...\n"
	got := preprocessOrchestratedResponse(in)
	if !strings.Contains(got, "\nWRITE:") {
		t.Fatalf("want newline before second WRITE:, got %q", got)
	}
	ops := parseOrchestratedNativeEdits(got)
	if len(ops) != 2 {
		t.Fatalf("ops=%+v", ops)
	}
	cmds := parseOrchestratedCommands(got)
	if len(cmds) != 1 || !strings.Contains(cmds[0], "go test") {
		t.Fatalf("cmds=%v", cmds)
	}
}

func TestUnwrapMarkdownInlineToolLines_backtickBdUpdate(t *testing.T) {
	t.Parallel()
	in := "`bd update te-rnd --status=in_progress`\n"
	got := preprocessOrchestratedResponse(in)
	if !strings.Contains(got, "CMD: bd update te-rnd") {
		t.Fatalf("want CMD prefix, got %q", got)
	}
	cmds := parseOrchestratedCommands(in)
	if len(cmds) != 1 || !strings.Contains(cmds[0], "bd update te-rnd") {
		t.Fatalf("cmds = %v", cmds)
	}
}

func TestFormatMalformedNativeEditFeedback_prosePath(t *testing.T) {
	t.Parallel()
	in := "EDIT: command with `<<<<<<< SEARCH` / `=======` / `>>>>>>> REPLACE` blocks.\n"
	got := FormatMalformedNativeEditFeedback(in)
	if got == "" || !strings.Contains(got, "rejected prose path") {
		t.Fatalf("got %q", got)
	}
	ops := parseOrchestratedNativeEdits(in)
	if len(ops) != 0 {
		t.Fatalf("prose EDIT must not parse, ops=%+v", ops)
	}
}

func TestFormatMalformedNativeEditFeedback_replaceOnly(t *testing.T) {
	t.Parallel()
	in := "EDIT: linkshelf/internal/api/handlers_test.go\n>>>>>>> REPLACE\n"
	got := FormatMalformedNativeEditFeedback(in)
	if got == "" || !strings.Contains(got, "<<<<<<< SEARCH") {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(got, "handlers_test.go") {
		t.Fatalf("got %q", got)
	}
}

func TestParseOrchestratedCommands_polecatMalformedTurn(t *testing.T) {
	t.Parallel()
	in := "`bd update te-rnd --status=in_progress`\nEDIT: linkshelf/internal/api/handlers_test.go\n>>>>>>> REPLACE\n"
	cmds := parseOrchestratedCommands(in)
	if len(cmds) != 1 || !strings.Contains(cmds[0], "bd update te-rnd") {
		t.Fatalf("want bd update as CMD, got %v", cmds)
	}
	ops := parseOrchestratedNativeEdits(in)
	if len(ops) != 0 {
		t.Fatalf("malformed EDIT must not parse, ops=%+v", ops)
	}
	if got := FormatMalformedNativeEditFeedback(in); got == "" {
		t.Fatal("expected malformed EDIT feedback")
	}
}

func TestUnwrapMarkdownFencedToolBlocks_pythonCMD(t *testing.T) {
	t.Parallel()
	in := "```python\nCMD: export BEADS_DIR=x && cd rig/mayor/rig && bd close te-rnd\n```\n"
	got := unwrapMarkdownFencedToolBlocks(in)
	if !strings.HasPrefix(strings.TrimSpace(got), "CMD:") {
		t.Fatalf("got %q", got)
	}
	cmds := parseOrchestratedCommands(in)
	if len(cmds) != 1 || !strings.Contains(cmds[0], "bd close te-rnd") {
		t.Fatalf("cmds = %v", cmds)
	}
}

func TestUnwrapMarkdownFencedToolBlocks_goEDIT(t *testing.T) {
	t.Parallel()
	in := "```go\nEDIT: linkshelf/internal/api/handlers_test.go\n<<<<<<< SEARCH\nx\n=======\ny\n>>>>>>> REPLACE\n```\n"
	ops := parseOrchestratedNativeEdits(in)
	if len(ops) != 1 || ops[0].kind != "edit" || ops[0].search != "x" {
		t.Fatalf("ops = %+v", ops)
	}
}

func TestParseOrchestratedNativeEdits_acceptsEndEditAlias(t *testing.T) {
	in := `EDIT: linkshelf/internal/store/store_test.go
<<<<<<< SEARCH
old
=======
new
---END EDIT---
`
	ops := parseOrchestratedNativeEdits(in)
	if len(ops) != 1 || ops[0].kind != "edit" || ops[0].search != "old" {
		t.Fatalf("ops = %+v", ops)
	}
}

func TestParseOrchestratedCommands_jsonCMDObject(t *testing.T) {
	t.Parallel()
	in := `{ "CMD": "export BEADS_DIR=$GT_ROOT/testgt3/.beads && cd testgt3/mayor/rig && bd list --status=open" }`
	cmds := parseOrchestratedCommands(in)
	if len(cmds) != 1 {
		t.Fatalf("cmds = %#v", cmds)
	}
	if !strings.Contains(cmds[0], "bd list --status=open") {
		t.Fatalf("unexpected cmd: %q", cmds[0])
	}
}

func TestParseOrchestratedCommands_jsonLowercaseCmd(t *testing.T) {
	t.Parallel()
	in := `{"cmd":"cd testgt3/mayor/rig/linkshelf && go mod tidy"}`
	cmds := parseOrchestratedCommands(in)
	if len(cmds) != 1 || !strings.Contains(cmds[0], "go mod tidy") {
		t.Fatalf("cmds = %#v", cmds)
	}
}
