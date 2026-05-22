package main

import (
	"strings"
	"testing"
)

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
