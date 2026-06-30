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

func TestSanitizeOrchestratedShellCommand_stripsLeadingMarkdownBold(t *testing.T) {
	t.Parallel()
	in := "** export BEADS_DIR=$GT_ROOT/testgt3/.beads && cd testgt3/mayor/rig && bd list --status=closed"
	fixed, changed := sanitizeOrchestratedShellCommand(in)
	if !changed {
		t.Fatal("expected change")
	}
	if strings.HasPrefix(fixed, "**") {
		t.Fatalf("leading ** still present: %q", fixed)
	}
	if !strings.HasPrefix(fixed, "export BEADS_DIR") {
		t.Fatalf("want export command: %q", fixed)
	}
}

func TestNormalizeOrchestratedToolLabel_cmdBoldColon(t *testing.T) {
	t.Parallel()
	got := normalizeOrchestratedToolLabel("CMD:** export BEADS_DIR=x && bd list")
	if got != "CMD: export BEADS_DIR=x && bd list" {
		t.Fatalf("got %q", got)
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

func TestUnwrapJSONCommandArray_convertsKeystrokesToMarkers(t *testing.T) {
	t.Parallel()
	in := `{
  "state_analysis": "need to fix tests",
  "commands": [
    {
      "keystrokes": "export BEADS_DIR=$GT_ROOT/testgt3/.beads && cd testgt3/mayor/rig && bd update te-toa --status=in_progress",
      "is_blocking": true,
      "timeout_sec": 10
    },
    {
      "keystrokes": "READ: linkshelf/internal/api/handlers_test.go",
      "is_blocking": true,
      "timeout_sec": 10
    }
  ],
  "is_task_complete": false
}`
	got := unwrapJSONCommandArray(in)
	if !strings.Contains(got, "CMD: export BEADS_DIR") {
		t.Fatalf("want CMD: prefix, got %q", got)
	}
	if !strings.Contains(got, "bd update te-toa --status=in_progress") {
		t.Fatalf("missing bd update: %q", got)
	}
	if !strings.Contains(got, "READ: linkshelf/internal/api/handlers_test.go") {
		t.Fatalf("missing READ: %q", got)
	}
}

func TestUnwrapJSONCommandArray_existingCMDKeystrokes(t *testing.T) {
	t.Parallel()
	in := `{"commands": [{"keystrokes": "CMD: export BEADS_DIR=$GT_ROOT/testgt3/.beads && cd testgt3/mayor/rig && bd list --status=open"}]}`
	got := unwrapJSONCommandArray(in)
	if !strings.Contains(got, "CMD: export BEADS_DIR") {
		t.Fatalf("want CMD preserved: %q", got)
	}
	// Should not double-prefix
	if strings.Contains(got, "CMD: CMD:") {
		t.Fatalf("double CMD: prefix: %q", got)
	}
}

func TestUnwrapJSONCommandArray_emptyCommands(t *testing.T) {
	t.Parallel()
	in := `{"commands": []}`
	got := unwrapJSONCommandArray(in)
	if got != in {
		t.Fatalf("empty array should return unchanged: %q", got)
	}
}

func TestUnwrapJSONCommandArray_noCommandsKey(t *testing.T) {
	t.Parallel()
	in := `{"outcome":"success","summary":"done"}`
	got := unwrapJSONCommandArray(in)
	if got != in {
		t.Fatalf("no commands key should return unchanged: %q", got)
	}
}

func TestPreprocessOrchestratedResponse_jsonCommandArray(t *testing.T) {
	t.Parallel()
	in := `{"commands": [{"keystrokes": "cd testgt3/mayor/rig/linkshelf && go test -count=1 ./internal/api/..."}]}`
	got := preprocessOrchestratedResponse(in)
	if !strings.Contains(got, "CMD: cd testgt3") {
		t.Fatalf("want CMD: prefix after preprocessing: %q", got)
	}
	cmds := parseOrchestratedCommands(in)
	if len(cmds) != 1 || !strings.Contains(cmds[0], "go test") {
		t.Fatalf("cmds = %v", cmds)
	}
}

func TestUnwrapJSONCommandArray_whitespaceOnlyKeystrokes(t *testing.T) {
	t.Parallel()
	in := `{"commands": [{"keystrokes": "   "}, {"keystrokes": "real command here"}]}`
	got := unwrapJSONCommandArray(in)
	if !strings.Contains(got, "CMD: real command here") {
		t.Fatalf("want CMD: real command: %q", got)
	}
	if strings.Count(got, "CMD:") != 1 {
		t.Fatalf("want exactly 1 CMD:, got %q", got)
	}
}

func TestUnwrapJSONCommandArray_cmdKey(t *testing.T) {
	t.Parallel()
	in := `{"outcome":"failure","summary":"need to read","commands":[{"cmd":"READ: linkshelf/internal/api/handlers_test.go"}]}`
	got := unwrapJSONCommandArray(in)
	if !strings.Contains(got, "READ: linkshelf/internal/api/handlers_test.go") {
		t.Fatalf("want READ: command extracted, got %q", got)
	}
}

func TestUnwrapJSONCommandArray_cmdKeyWithExtraFields(t *testing.T) {
	t.Parallel()
	in := `{"outcome":"failure","summary":"...","commands":[{"cmd":"export BEADS_DIR=x && cd testgt3/mayor/rig && bd update te-h2i --status=in_progress"},{"cmd":"READ: linkshelf/internal/api/handlers_test.go"}]}`
	got := unwrapJSONCommandArray(in)
	if !strings.Contains(got, "CMD: export BEADS_DIR") {
		t.Fatalf("want CMD: prefix added: %q", got)
	}
	if !strings.Contains(got, "READ: linkshelf/internal/api/handlers_test.go") {
		t.Fatalf("want READ: preserved: %q", got)
	}
}

func TestStripChannelMarkers_replacesWithNewline(t *testing.T) {
	in := "CMD: bd list --limit=0<|message|>prose text"
	got := stripChannelMarkers(in)
	if !strings.Contains(got, "\n") {
		t.Fatalf("want newline inserted, got %q", got)
	}
	// CMD line must end at --limit=0, prose on its own line
	cmds := parseOrchestratedCommands(got)
	if len(cmds) != 1 {
		t.Fatalf("want 1 cmd, got %d: %v", len(cmds), cmds)
	}
	if strings.Contains(cmds[0], "prose") {
		t.Fatalf("cmd contains prose: %q", cmds[0])
	}
	if !strings.Contains(cmds[0], "--limit=0") {
		t.Fatalf("cmd missing --limit=0: %q", cmds[0])
	}
}

func TestStripChannelMarkers_inlineCmdAfterProseSentence(t *testing.T) {
	// Realistic LLM output: prose sentence ending with "." followed by CMD:
	// normalizeGluedCMDMarkers splits on .CMD: → . is non-alphanumeric
	in := "CMD: bd list --all<|message|>We need output.CMD: bd close te-abc"
	got := stripChannelMarkers(in)
	if strings.Count(got, "CMD:") != 2 {
		t.Fatalf("want 2 CMD: markers, got %q", got)
	}
	cmds := parseOrchestratedCommands(got)
	if len(cmds) != 2 {
		t.Fatalf("want 2 cmds, got %d: %v", len(cmds), cmds)
	}
	if !strings.Contains(cmds[0], "bd list --all") {
		t.Fatalf("cmd[0] wrong: %q", cmds[0])
	}
	if !strings.Contains(cmds[1], "bd close te-abc") {
		t.Fatalf("cmd[1] wrong: %q", cmds[1])
	}
}

func TestStripChannelMarkers_proseAfterCommand(t *testing.T) {
	// Prose after a command on the same line should not be slurped
	in := "CMD: bd list --all<|message|>We need output."
	cmds := parseOrchestratedCommands(in)
	if len(cmds) != 1 {
		t.Fatalf("want 1 cmd, got %d: %v", len(cmds), cmds)
	}
	if strings.Contains(cmds[0], "We need") {
		t.Fatalf("cmd contains prose: %q", cmds[0])
	}
	if !strings.Contains(cmds[0], "bd list --all") {
		t.Fatalf("cmd wrong: %q", cmds[0])
	}
}

func TestStripChannelMarkers_roleAndChannelTags(t *testing.T) {
	in := "<|start|>assistant<|channel|>analysis<|message|>CMD: bd list --limit=0"
	got := stripChannelMarkers(in)
	// Each tag replaced with \n; CMD: should be on its own line
	if !strings.Contains(got, "\nCMD:") || !strings.Contains(got, "bd list --limit=0") {
		t.Fatalf("got %q", got)
	}
	cmds := parseOrchestratedCommands(got)
	if len(cmds) != 1 {
		t.Fatalf("want 1 cmd, got %d: %v", len(cmds), cmds)
	}
	if strings.Contains(cmds[0], "assistant") || strings.Contains(cmds[0], "analysis") {
		t.Fatalf("cmd contains role/channel text: %q", cmds[0])
	}
}

func TestStripChannelMarkers_noMarkersPassthrough(t *testing.T) {
	in := "CMD: bd list --limit=0\nCMD: bd close te-abc"
	got := stripChannelMarkers(in)
	if got != in {
		t.Fatalf("expected passthrough, got %q", got)
	}
}

func TestStripChannelMarkers_emptyReplacementBug_regression(t *testing.T) {
	// Empty string replacement caused:
	//   --limit=0<|message|>CMD: → --limit=0CMD: (CMD: split fails)
	// Newline replacement fixes it:
	//   --limit=0\nCMD: (CMD: on own line, split works)
	in := "CMD: bd list --limit=0<|message|>CMD: bd close te-abc"
	got := parseOrchestratedCommands(in)
	if len(got) != 2 {
		t.Fatalf("want 2 cmds (empty-replacement regression), got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], "bd list --limit=0") {
		t.Fatalf("cmd[0] wrong: %q", got[0])
	}
	if !strings.Contains(got[1], "bd close te-abc") {
		t.Fatalf("cmd[1] wrong: %q", got[1])
	}
}

func TestStripChannelMarkers_spaceReplacementBug_regression(t *testing.T) {
	// Space replacement caused bd list positional arg error:
	//   --limit=0<|message|>assistant analysis We need output.
	//   → --limit=0  assistant analysis We need output.
	//   → "bd list does not accept positional arguments"
	// Newline: prose lands on ignored lines.
	in := "CMD: export BEADS_DIR=x && cd rig && bd list --status=closed --limit=0<|message|><|start|>assistant<|channel|>analysis<|message|>We need output; likely shows closed beads."
	got := parseOrchestratedCommands(in)
	if len(got) != 1 {
		t.Fatalf("want 1 cmd, got %d: %v", len(got), got)
	}
	if strings.Contains(got[0], "We need") || strings.Contains(got[0], "assistant") || strings.Contains(got[0], "analysis") {
		t.Fatalf("cmd contains prose leftover: %q", got[0])
	}
	if !strings.HasSuffix(strings.TrimSpace(got[0]), "--limit=0") {
		t.Fatalf("cmd should end with --limit=0, got %q", got[0])
	}
}

func TestStripChannelMarkers_fullTurnRegression(t *testing.T) {
	// Exact reproduction of the LLM turn 8 pattern: single line, no \n, markers
	// as separators between commands and prose. After stripping, all CMDs must
	// be clean and the JSON outcome must be stripped.
	in := "CMD: export BEADS_DIR=$GT_ROOT/testgt4/.beads && cd $GT_ROOT/testgt4/mayor/rig && bd list --status=closed --limit=0<|message|><|start|>assistant<|channel|>analysis<|message|>We need output; likely shows many closed beads including one for index.html. Once we have ID, we will reopen and create file.CMD: export BEADS_DIR=$GT_ROOT/testgt4/.beads && cd $GT_ROOT/testgt4/mayor/rig && bd list --status=closed --limit=0<|message|><|start|>assistant<|channel|>analysis<|message|>We still need output; maybe the system not returning. Could be path issue.CMD: export BEADS_DIR=$GT_ROOT/testgt4/.beads && cd testgt4/mayor/rig && bd list --status=closed --limit=0<|message|><|start|>assistant<|channel|>analysis<|message|>We need output.{\"outcome\":\"failure\",\"summary\":\"Unable to list closed beads\"}"
	cmds := parseOrchestratedCommands(in)
	if len(cmds) < 3 {
		t.Fatalf("want >=3 cmds, got %d: %v", len(cmds), cmds)
	}
	for i, c := range cmds {
		if strings.Contains(c, "We need") || strings.Contains(c, "assistant") || strings.Contains(c, "analysis") || strings.Contains(c, "outcome") {
			t.Fatalf("cmd[%d] contains prose or JSON: %q", i, c)
		}
		if !strings.Contains(c, "bd list") {
			t.Fatalf("cmd[%d] missing bd list: %q", i, c)
		}
	}
}

func TestIsBeadCloseCommand_noFalsePositiveOnScopedBdList(t *testing.T) {
	// The old strings.Contains("bd") && strings.Contains(" close") matched
	// scoped bd list output containing '=== closed implement ==='.
	forbidden := []struct {
		name string
		line string
	}{
		{"scoped bd list echo prefix", "echo '=== closed implement ==='"},
		{"scoped bd list inline", "bd list --status=closed"},
		{"scoped bd list open", "bd list --status=open"},
	}
	for _, tc := range forbidden {
		t.Run(tc.name, func(t *testing.T) {
			if isBeadCloseCommand(tc.line) {
				t.Fatalf("isBeadCloseCommand(%q) = true, want false", tc.line)
			}
		})
	}
	allowed := []struct {
		name string
		line string
	}{
		{"bd close", "bd close te-abc"},
		{"bd close no id", "bd close"},
		{"bd close space id", "bd close te-def"},
	}
	for _, tc := range allowed {
		t.Run(tc.name, func(t *testing.T) {
			if !isBeadCloseCommand(tc.line) {
				t.Fatalf("isBeadCloseCommand(%q) = false, want true", tc.line)
			}
		})
	}
}
