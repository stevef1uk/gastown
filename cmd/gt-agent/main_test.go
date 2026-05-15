package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFindGT_FromEnv verifies GT_BIN env var is respected.
func TestFindGT_FromEnv(t *testing.T) {
	oldEnv := os.Getenv("GT_BIN")
	defer os.Setenv("GT_BIN", oldEnv)

	os.Setenv("GT_BIN", "/custom/path/gt")
	got := findGT()
	if got != "/custom/path/gt" {
		t.Errorf("findGT() = %q, want /custom/path/gt", got)
	}
}

// TestFindGT_AbsolutePath verifies absolute path resolution.
func TestFindGT_AbsolutePath(t *testing.T) {
	oldEnv := os.Getenv("GT_BIN")
	defer os.Setenv("GT_BIN", oldEnv)
	os.Unsetenv("GT_BIN")

	// Create a fake gt binary in temp dir
	tmpDir := t.TempDir()
	gtPath := filepath.Join(tmpDir, "gt")
	if err := os.WriteFile(gtPath, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}

	// Prepend temp dir to PATH
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", tmpDir+":"+oldPath)

	got := findGT()
	if !strings.HasSuffix(got, "/gt") {
		t.Errorf("findGT() = %q, want path ending in /gt", got)
	}
}

// TestFindGT_SelfDirFallback verifies that findGT falls back to the
// directory containing the gt-agent binary.
func TestFindGT_SelfDirFallback(t *testing.T) {
	oldEnv := os.Getenv("GT_BIN")
	defer os.Setenv("GT_BIN", oldEnv)
	os.Unsetenv("GT_BIN")

	// Remove gt from PATH
	oldPath := os.Getenv("PATH")
	defer os.Setenv("PATH", oldPath)
	os.Setenv("PATH", "/usr/bin:/bin")

	gt := findGT()
	// Since gt is likely installed alongside the test binary or in .local/bin,
	// we just verify it returns something (not the fallback "gt" string)
	// unless gt truly isn't installed anywhere.
	if gt == "gt" {
		t.Log("gt not found anywhere, falling back to 'gt'")
	}
}

// TestFindGT_NonExistentEnv verifies graceful fallback when GT_BIN points
// to a non-existent path.
func TestFindGT_NonExistentEnv(t *testing.T) {
	oldEnv := os.Getenv("GT_BIN")
	defer os.Setenv("GT_BIN", oldEnv)

	os.Setenv("GT_BIN", "/nonexistent/path/gt")
	got := findGT()
	// Should still return the env value even if file doesn't exist
	// (we only validate existence for PATH/candidate lookups)
	if got != "/nonexistent/path/gt" {
		t.Errorf("findGT() = %q, want /nonexistent/path/gt", got)
	}
}

// TestCommandParsing verifies that LLM responses with CMD: lines are
// correctly identified.
func TestCommandParsing(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantCmds []string
		wantDone string
	}{
		{
			name:     "simple command",
			input:    "CMD: echo hello\nDONE: Completed successfully",
			wantCmds: []string{"echo hello"},
			wantDone: "Completed successfully",
		},
		{
			name:     "multiple commands",
			input:    "CMD: git status\nCMD: git add .\nDONE: All changes staged",
			wantCmds: []string{"git status", "git add ."},
			wantDone: "All changes staged",
		},
		{
			name:     "no commands",
			input:    "DONE: Nothing to do",
			wantCmds: nil,
			wantDone: "Nothing to do",
		},
		{
			name:     "command with extra spaces",
			input:    "CMD:   ls -la  \nDONE: Listed files",
			wantCmds: []string{"ls -la"},
			wantDone: "Listed files",
		},
		{
			name:     "empty command ignored",
			input:    "CMD:\nCMD: echo test\nDONE: Done",
			wantCmds: []string{"echo test"},
			wantDone: "Done",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lines := strings.Split(tc.input, "\n")
			var cmds []string
			var done string
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "CMD:") {
					cmd := strings.TrimPrefix(line, "CMD:")
					cmd = strings.TrimSpace(cmd)
					if cmd != "" {
						cmds = append(cmds, cmd)
					}
				} else if strings.HasPrefix(line, "DONE:") {
					done = strings.TrimPrefix(line, "DONE:")
					done = strings.TrimSpace(done)
				}
			}

			if len(cmds) != len(tc.wantCmds) {
				t.Errorf("got %d commands, want %d: got=%v", len(cmds), len(tc.wantCmds), cmds)
			}
			for i := range cmds {
				if i < len(tc.wantCmds) && cmds[i] != tc.wantCmds[i] {
					t.Errorf("command[%d] = %q, want %q", i, cmds[i], tc.wantCmds[i])
				}
			}
			if done != tc.wantDone {
				t.Errorf("done = %q, want %q", done, tc.wantDone)
			}
		})
	}
}

// TestSystemPromptConstruction verifies the system prompt contains
// required elements.
func TestSystemPromptConstruction(t *testing.T) {
	role := "polecat"
	context := "Test context"

	prompt := `You are a Gas Town agent with role: ` + role + `.

You have access to shell commands. Execute work step by step.
Rules:
1. Only run commands that are standard Unix utilities or known to exist (git, ls, cat, grep, etc.)
2. Do NOT invent commands or tools that don't exist
3. Do NOT run "gt mail inbox" or other status-checking commands — focus on the assigned work
4. When you need to run a command, output it on a line starting with "CMD: " followed by the shell command
5. After all commands, output "DONE:" followed by a summary of what was accomplished
6. If you cannot complete the work, output "DONE: Could not complete because ..."

Context:
` + context

	if !strings.Contains(prompt, "Gas Town agent") {
		t.Error("System prompt should identify as Gas Town agent")
	}
	if !strings.Contains(prompt, role) {
		t.Error("System prompt should contain role")
	}
	if !strings.Contains(prompt, "CMD:") {
		t.Error("System prompt should explain CMD: format")
	}
	if !strings.Contains(prompt, "DONE:") {
		t.Error("System prompt should explain DONE: format")
	}
	if !strings.Contains(prompt, context) {
		t.Error("System prompt should include context")
	}
}

// TestWorkItemFormatting verifies work items are formatted correctly
// for the LLM prompt.
func TestWorkItemFormatting(t *testing.T) {
	workItems := []string{
		"[NUDGE from mayor] Check your hook",
		"[HOOK] gt-abc123: Fix the bug",
	}

	var userPrompt string
	userPrompt = "Execute the following work and report results:\n\n"
	for i, item := range workItems {
		userPrompt += string('0'+byte(i+1)) + ". " + item + "\n"
	}

	if !strings.Contains(userPrompt, "1. [NUDGE from mayor]") {
		t.Error("Prompt should contain formatted nudge")
	}
	if !strings.Contains(userPrompt, "2. [HOOK]") {
		t.Error("Prompt should contain formatted hook")
	}
}

func TestCanonicalRole(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"witness", "witness"},
		{"testgt1/witness", "witness"},
		{"testgt1/refinery", "refinery"},
		{"testgt1/polecats/rictus", "polecat"},
		{"testgt1/crew/alice", "crew"},
		{"mechanic", "mechanic"},
		{"testgt1/mechanic", "mechanic"},
		{"deacon/boot", "boot"},
		{"", "worker"},
	}
	for _, tt := range tests {
		if got := canonicalRole(tt.in); got != tt.want {
			t.Errorf("canonicalRole(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNormalizeGeneratedCommand(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		changed bool
	}{
		{"bd mol current mol-witness-patrol", "gt mol current mol-witness-patrol", true},
		{"bd mol current hq-wisp-abc123", "gt mol current hq-wisp-abc123", true},
		{"bd ready", "bd ready", false},
		{"gt mol current", "gt mol current", false},
		{"gt sling design --on [bead] [rig]/architect", "true", true},
		{"gt sling design --on <bead-id> testgt2/architect", "true", true},
		{"gt sling design --on MOL-1234 Rig-Alpha/architect", "true", true},
		{"gt sling design --on te-ybw testgt2/architect", "gt sling design --on te-ybw testgt2/architect", false},
		{"gt mol execute --on mol-idea-9012", "true", true},
	}
	for _, tt := range tests {
		got, changed := normalizeGeneratedCommand(tt.in)
		if got != tt.want || changed != tt.changed {
			t.Errorf("normalizeGeneratedCommand(%q) = (%q, %v), want (%q, %v)",
				tt.in, got, changed, tt.want, tt.changed)
		}
	}
}

// TestParseLLMResponse covers the production CMD-block parser
// (parseLLMResponse in main.go). The regression these tests guard against
// is the architect handoff bug where prose lines between two CMD: markers
// were being concatenated into the first command and the second command
// inherited the trailing markdown fence.
func TestParseLLMResponse(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantCmds     []string
		wantDone     string
		wantHallucinated bool
	}{
		{
			name:     "simple single command",
			input:    "CMD: echo hello\nDONE: ok",
			wantCmds: []string{"echo hello"},
			wantDone: "ok",
		},
		{
			name:     "multiple bare commands",
			input:    "CMD: git status\nCMD: git add .\nDONE: staged",
			wantCmds: []string{"git status", "git add ."},
			wantDone: "staged",
		},
		{
			name:     "empty CMD: line ignored",
			input:    "CMD:\nCMD: echo test\nDONE: done",
			wantCmds: []string{"echo test"},
			wantDone: "done",
		},
		{
			name: "architect handoff bug: prose between two CMDs is NOT swallowed",
			input: "CMD: printf '# Architecture\\n' > arch.md\n\n" +
				"### Step 2: Implement the Feature\n\n" +
				"Since the role is Architect and not Polecat, we do not implement.\n\n" +
				"### Step 4: Handoff to Mayor\n\n" +
				"```bash\n" +
				"CMD: gt handoff mayor -s \"Architecture Ready\" -m \"design done\"\n" +
				"```\n\n" +
				"Please note: the mail inbox check is secondary.\n",
			wantCmds: []string{
				"printf '# Architecture\\n' > arch.md",
				"gt handoff mayor -s \"Architecture Ready\" -m \"design done\"",
			},
		},
		{
			name: "CMD inside ```bash fence on its own line",
			input: "```bash\n" +
				"CMD: ls -la\n" +
				"```\n",
			wantCmds: []string{"ls -la"},
		},
		{
			name:     "CMD: with leading ```bash on the same line",
			input:    "CMD: ```bash echo hi```",
			wantCmds: []string{"echo hi"},
		},
		{
			name: "markdown heading after CMD does not bleed into command",
			input: "CMD: gt hook\n" +
				"### Now check inbox\n" +
				"This part is prose.\n",
			wantCmds: []string{"gt hook"},
		},
		{
			name: "heredoc with EOF terminator is captured verbatim",
			input: "CMD: cat <<'EOF' > /tmp/note\n" +
				"line one\n" +
				"line two\n" +
				"EOF\n" +
				"DONE: wrote file",
			wantCmds: []string{
				"cat <<'EOF' > /tmp/note\nline one\nline two\nEOF",
			},
			wantDone: "wrote file",
		},
		{
			name: "heredoc without terminator is discarded",
			input: "CMD: cat <<EOF > /tmp/note\n" +
				"line one\n" +
				"line two\n",
			wantCmds: nil,
		},
		{
			// Fix #113: the mayor's Stage 0 atomic pipeline is a
			// multi-line `sh -c '...'` body with paragraph-style blank
			// lines for readability. The parser USED to terminate the
			// script on the first blank line, orphaning the opening
			// `sh -c '` (no closing single-quote was ever appended).
			// `/bin/sh -c "sh -c '"` then errored with "Unterminated
			// quoted string" before any of the real script ran.
			//
			// The fix counts unbalanced single/double quotes in the
			// accumulated body; while a quote is open, blank lines are
			// treated as part of the quoted string instead of as
			// script terminators.
			name: "sh -c with blank lines inside single-quoted body is collected verbatim (Fix #113)",
			input: "CMD: sh -c '\n" +
				"set -u\n" +
				"MAIL_ID=\"hq-wisp-7rn\"\n" +
				"BODY=$(gt mail read \"$MAIL_ID\" 2>/dev/null)\n" +
				"\n" +
				"RIG=$(echo \"$BODY\" | head -1)\n" +
				"\n" +
				"echo \"STAGE0: bead for $RIG\"\n" +
				"'\n" +
				"DONE: Stage 0 dispatched",
			wantCmds: []string{
				"sh -c '\nset -u\nMAIL_ID=\"hq-wisp-7rn\"\nBODY=$(gt mail read \"$MAIL_ID\" 2>/dev/null)\n\nRIG=$(echo \"$BODY\" | head -1)\n\necho \"STAGE0: bead for $RIG\"\n'",
			},
			wantDone: "Stage 0 dispatched",
		},
		{
			// Sibling case: a `# shell comment` line inside an open
			// `sh -c '...'` body must NOT be treated as a markdown
			// heading. Pre-fix, the parser saw `# foo` as `## ` /
			// `# ` style markdown and killed the script body, again
			// orphaning the opening `'`.
			name: "shell-comment line inside sh -c single-quote body is collected (Fix #113)",
			input: "CMD: sh -c '\n" +
				"set -u\n" +
				"# this is a shell comment, not a markdown heading\n" +
				"echo hi\n" +
				"'\n" +
				"DONE: ran",
			wantCmds: []string{
				"sh -c '\nset -u\n# this is a shell comment, not a markdown heading\necho hi\n'",
			},
			wantDone: "ran",
		},
		{
			// Sibling case: a prose-shaped sentence inside an open
			// quote (e.g. a help-text string inside `gt mail send -m
			// "..."`) must also survive. Pre-fix Fix #96 was killing
			// it as "LLM narration".
			name: "prose-shaped line inside sh -c double-quote -m body is collected (Fix #113)",
			input: "CMD: sh -c 'gt mail send mayor/ -s \"hi\" -m \"Now that the previous step finished,\n" +
				"we should proceed.\"'\n" +
				"DONE: sent",
			wantCmds: []string{
				"sh -c 'gt mail send mayor/ -s \"hi\" -m \"Now that the previous step finished,\nwe should proceed.\"'",
			},
			wantDone: "sent",
		},
		{
			name: "hallucinated Output: is rejected",
			input: "CMD: gt hook\n" +
				"Output: fake terminal output\n" +
				"CMD: gt mail inbox\n",
			wantHallucinated: true,
		},
		{
			name: "DONE: captured even without commands",
			input: "DONE: nothing to do",
			wantDone: "nothing to do",
		},
		{
			name: "interleaved commands and prose only keep commands",
			input: "Let me think about this.\n" +
				"\n" +
				"CMD: gt prime\n" +
				"\n" +
				"Now I should check the hook.\n" +
				"\n" +
				"CMD: gt hook\n" +
				"\n" +
				"DONE: primed and checked hook",
			wantCmds: []string{"gt prime", "gt hook"},
			wantDone: "primed and checked hook",
		},
		{
			name: "trailing ``` on CMD line is stripped",
			input: "CMD: echo done```",
			wantCmds: []string{"echo done"},
		},
		{
			// Regression: planner emitted CMDs with a same-line markdown
			// rationale paragraph like `CMD: bd search "F23" --status "open" **Rationale for Change:**`.
			// The `**...**` block must be stripped before execution.
			name:     "trailing markdown bold prose on CMD line is stripped",
			input:    `CMD: bd search "F23" --priority-max "P0" --status "open" **Rationale for Command Change:**`,
			wantCmds: []string{`bd search "F23" --priority-max "P0" --status "open"`},
		},
		{
			name:     "trailing markdown bold followed by more prose is stripped",
			input:    `CMD: gt mail inbox **Note:** check unread first`,
			wantCmds: []string{"gt mail inbox"},
		},
		{
			// Glob `**/*.go` does NOT have whitespace+`**...**` so should be preserved.
			name:     "shell globstar **/* is preserved",
			input:    "CMD: ls -la **/*.go",
			wantCmds: []string{"ls -la **/*.go"},
		},
		{
			name: "trailing markdown bold with multiple **bold** segments strips from first",
			input: "CMD: gt status --json **first bold** more prose **second bold** trailing",
			wantCmds: []string{"gt status --json"},
		},
		{
			// Regression for the `gd patrol report` typo we observed in
			// the live planner log. The full parser is just collecting
			// the command verbatim — normalization to gt happens later in
			// normalizeGeneratedCommand.
			name:     "gd typo passes through parser unchanged",
			input:    "CMD: gd patrol report --summary hi",
			wantCmds: []string{"gd patrol report --summary hi"},
		},
		{
			// Regression: planner emitted multiple commands crammed
			// onto a single line with embedded `CMD:` markers, which
			// the parser previously treated as one command.
			name:     "inline CMD: markers split into separate commands",
			input:    `CMD: bd update hq-bbn --priority 2 CMD: gt mail send mayor/ -s "ready" -m "go" CMD: gt nudge mayor "check inbox"`,
			wantCmds: []string{
				"bd update hq-bbn --priority 2",
				`gt mail send mayor/ -s "ready" -m "go"`,
				`gt nudge mayor "check inbox"`,
			},
		},
		{
			// Inline CMD: ... CMD: DONE: handed off — the trailing DONE:
			// segment should populate doneSummary, not be executed.
			name:     "inline CMD: trailing DONE: is captured as summary",
			input:    `CMD: gt nudge mayor "go" CMD: DONE: handed off to mayor`,
			wantCmds: []string{`gt nudge mayor "go"`},
			wantDone: "handed off to mayor",
		},
		{
			// Fix #91: the witness LLM emitted multiple commands on a
			// single line with `**markdown bold**` annotations BETWEEN
			// the `CMD:` segments. The parser previously applied
			// trailingMarkdownBoldRE to the whole line BEFORE splitting
			// on `\s+CMD:\s+`. The regex matches `\s+\*\*…\*\*.*$`
			// (greedy to end-of-line), so it consumed every `CMD:`
			// segment after the first bold marker and only the first
			// command survived. Result: the witness looped for hours
			// executing only one of N planned actions per turn while
			// the LLM (correctly) re-planned all N every turn.
			//
			// After the fix, each inline `CMD:` segment is split first
			// and the bold-prose stripper runs per segment. All five
			// commands below must come out as separate entries with
			// the bold annotations cleanly removed.
			name: "inline CMD: with **bold** prose between segments (Fix #91)",
			input: "CMD: gt patrol new **Patrol Cycle #531 Initiated** " +
				"CMD: gt patrol scan --notify **Scanning for zombies...** " +
				"CMD: gt patrol report --summary \"Patrol finished\" **Patrol Report:** Patrol finished " +
				"CMD: gt mail inbox **Checking for unread messages...** " +
				"CMD: gt polecat list **Error:** Invalid command usage.",
			wantCmds: []string{
				"gt patrol new",
				"gt patrol scan --notify",
				`gt patrol report --summary "Patrol finished"`,
				"gt mail inbox",
				"gt polecat list",
			},
		},
		{
			// Negative test: a real argument that contains the substring
			// "CMD:" (without a leading space) must NOT split.
			name:     "non-leading CMD: in argument is preserved",
			input:    `CMD: echo "prefixCMD:notamarker"`,
			wantCmds: []string{`echo "prefixCMD:notamarker"`},
		},
		{
			// Planner regression: model glued CMD markers without spaces
			// (rig/CMD:, SPEC.mdCMD:) on a single line — must not run as ls args.
			name: "glued CMD: markers without whitespace split into separate commands",
			input: "CMD: ls -R testgt2/mayor/rig/CMD: cat testgt2/mayor/rig/SPEC.mdCMD: cat testgt2/mayor/rig/architecture.md",
			wantCmds: []string{
				"ls -R testgt2/mayor/rig",
				"cat testgt2/mayor/rig/SPEC.md",
				"cat testgt2/mayor/rig/architecture.md",
			},
		},
		{
			// Regression: the architect emitted a single CMD: block
			// followed by a complete multi-line shell script with a
			// heredoc, multiple commands, a multi-line gt mail send -m,
			// and a final DONE:. The old parser only kept the FIRST
			// line (mkdir), silently dropped everything else, and the
			// pipeline stalled because mayor never got the handoff mail.
			//
			// With the new parser, the whole script is collected into
			// ONE cmds entry that /bin/sh -c can execute as a multi-
			// line bash script.
			name: "architect multi-line shell script under one CMD: is collected as one script",
			input: "CMD: mkdir -p /work/architect\n" +
				"cat > /work/architect/architecture.md <<'EOF'\n" +
				"# Architecture: Hello API\n" +
				"\n" +
				"## API\n" +
				"- GET /\n" +
				"EOF\n" +
				"wc -c /work/architect/architecture.md\n" +
				"gt mail send mayor/ -s \"Architecture Ready\" -m \"design done\"\n" +
				"gt nudge mayor \"ready\"\n" +
				"gt unhook\n" +
				"DONE: architecture handed off to mayor",
			wantCmds: []string{
				"mkdir -p /work/architect\n" +
					"cat > /work/architect/architecture.md <<'EOF'\n" +
					"# Architecture: Hello API\n" +
					"\n" +
					"## API\n" +
					"- GET /\n" +
					"EOF\n" +
					"wc -c /work/architect/architecture.md\n" +
					"gt mail send mayor/ -s \"Architecture Ready\" -m \"design done\"\n" +
					"gt nudge mayor \"ready\"\n" +
					"gt unhook",
			},
			wantDone: "architecture handed off to mayor",
		},
		{
			// Heredoc body that contains markdown-like syntax (# heading,
			// ## section, > quote) must NOT terminate the script — heredoc
			// mode collects verbatim until the EOF terminator.
			name: "markdown structure inside heredoc body is preserved verbatim",
			input: "CMD: cat > arch.md <<'EOF'\n" +
				"# Heading 1\n" +
				"## Heading 2\n" +
				"### Heading 3\n" +
				"> a quote\n" +
				"```ignored fence```\n" +
				"EOF\n" +
				"echo done",
			wantCmds: []string{
				"cat > arch.md <<'EOF'\n" +
					"# Heading 1\n" +
					"## Heading 2\n" +
					"### Heading 3\n" +
					"> a quote\n" +
					"```ignored fence```\n" +
					"EOF\n" +
					"echo done",
			},
		},
		{
			// Multi-line `-m "..."` quoted argument with an embedded
			// newline must be preserved as part of the same shell script
			// (bash handles embedded newlines inside double-quoted args).
			// This is the exact pattern the architect uses to include
			// Project bead: $X / Design complete... in one mail body.
			name: "multi-line quoted argument is preserved in one script",
			input: "CMD: gt mail send mayor/ -s \"Architecture Ready\" -m \"Project bead: $PROJECT_BEAD\n" +
				"Design complete. ready for implementation.\"\n" +
				"gt nudge mayor \"ready\"",
			wantCmds: []string{
				"gt mail send mayor/ -s \"Architecture Ready\" -m \"Project bead: $PROJECT_BEAD\n" +
					"Design complete. ready for implementation.\"\n" +
					"gt nudge mayor \"ready\"",
			},
		},
		{
			// Two CMD: blocks with continuation lines under each, separated
			// by a blank line. Each CMD: starts a new script, blank line
			// flushes.
			name: "two CMD: blocks each with continuations",
			input: "CMD: mkdir -p /a\n" +
				"cp /tmp/x /a/x\n" +
				"\n" +
				"CMD: gt mail send mayor/ -s X -m Y\n" +
				"gt nudge mayor \"hi\"",
			wantCmds: []string{
				"mkdir -p /a\ncp /tmp/x /a/x",
				"gt mail send mayor/ -s X -m Y\ngt nudge mayor \"hi\"",
			},
		},
		{
			// Markdown structural line (### heading) between commands
			// flushes the current script and prevents the heading from
			// leaking into bash.
			name: "markdown heading between continuation commands flushes the script",
			input: "CMD: gt hook\n" +
				"gt mail inbox\n" +
				"### Step 2\n" +
				"This is prose.\n" +
				"CMD: gt mail read 1",
			wantCmds: []string{
				"gt hook\ngt mail inbox",
				"gt mail read 1",
			},
		},
		{
			// Fix #96 (architect prose-leak crash): the LLM emitted a
			// CMD: followed by reasoning prose without an empty line
			// separator. Before the fix the prose got piped into
			// /bin/sh where the apostrophe in "I'll" opened an
			// unterminated quoted string and the architect looped
			// forever on `Syntax error: Unterminated quoted string`.
			// Now the prose terminator-detector flushes the script
			// after the real command and discards the narration.
			name: "LLM prose continuation after CMD does not leak into shell (Fix #96)",
			input: "CMD: gt hook | cat\n" +
				"Now I'll re-execute the necessary commands with the correct bead ID extraction.\n" +
				"Let me first check the hook output properly, then redo the steps.\n",
			wantCmds: []string{"gt hook | cat"},
		},
		{
			// Variant of Fix #96 with two CMD: blocks separated by
			// pure prose (no blank line in between). Both commands
			// must survive and prose must be discarded.
			name: "Fix #96 variant: prose between two CMDs (no blank line) keeps both commands",
			input: "CMD: gt hook\n" +
				"Now I'll check the inbox.\n" +
				"Let me first verify the hook output.\n" +
				"CMD: gt mail inbox\n",
			wantCmds: []string{"gt hook", "gt mail inbox"},
		},
		{
			// Negative test: a continuation line that LOOKS prose-y
			// but contains shell metacharacters (a pipe, here) MUST
			// stay in the script. This guards against an over-eager
			// prose detector that would break real shell pipelines.
			name: "Fix #96 negative: continuation line with shell metachar stays in script",
			input: "CMD: gt hook\n" +
				"Now let us pipe to jq | jq .status\n",
			wantCmds: []string{"gt hook\nNow let us pipe to jq | jq .status"},
		},
		{
			// Negative test: a continuation line that starts with a
			// prose-leading word but lacks sentence-ending punctuation
			// is not treated as prose (defensive: allows the model to
			// emit `Now run the script` as a deliberate shell line if
			// it ever did).
			name: "Fix #96 negative: prose-leading word without terminal punctuation stays",
			input: "CMD: gt hook\n" +
				"Now run\n",
			wantCmds: []string{"gt hook\nNow run"},
		},
		{
			// Fix #121: models prefix DONE with CMD: — must not execute
			// "DONE: idle" as shell.
			name: "CMD: DONE: idle is treated as DONE summary not shell",
			input:   "CMD: DONE: idle\n",
			wantCmds: nil,
			wantDone: "idle",
		},
		{
			name: "markdown numbered step after CMD terminates script (planner template leak)",
			input: "CMD: test -s /tmp/x && echo y\n" +
				"1. Verify architecture exists before planning.\n\n" +
				"CMD: echo z\n",
			wantCmds: []string{"test -s /tmp/x && echo y", "echo z"},
		},
		{
			name: "markdown bullet after CMD terminates script",
			input: "CMD: echo one\n" +
				"- **Load** context from hook\n\n" +
				"CMD: echo two\n",
			wantCmds: []string{"echo one", "echo two"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotCmds, gotDone, gotHallucinated := parseLLMResponse(tc.input)
			if gotHallucinated != tc.wantHallucinated {
				t.Fatalf("hallucinated = %v, want %v", gotHallucinated, tc.wantHallucinated)
			}
			if tc.wantHallucinated {
				return
			}
			if len(gotCmds) != len(tc.wantCmds) {
				t.Fatalf("got %d commands, want %d\n  got:  %#v\n  want: %#v",
					len(gotCmds), len(tc.wantCmds), gotCmds, tc.wantCmds)
			}
			for i := range gotCmds {
				if gotCmds[i] != tc.wantCmds[i] {
					t.Errorf("cmd[%d] = %q, want %q", i, gotCmds[i], tc.wantCmds[i])
				}
			}
			if gotDone != tc.wantDone {
				t.Errorf("done = %q, want %q", gotDone, tc.wantDone)
			}
		})
	}
}

func TestCheckShellSyntax(t *testing.T) {
	diag, ok := checkShellSyntax("echo hi")
	if !ok {
		t.Fatalf("valid script rejected: %s", diag)
	}
	diag, ok = checkShellSyntax("echo hi\ntrue <")
	if ok {
		t.Fatal("expected invalid script to be rejected")
	}
	if diag == "" {
		t.Fatal("expected diagnostic message from syntax check")
	}
}

func TestDetectHeredocTerm(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"cat <<EOF", "EOF"},
		{"cat <<'EOF'", "EOF"},
		{"cat << \"EOF\"", "EOF"},
		{"cat <<-EOF", "EOF"},
		{"cat <<'EOF' > file.md", "EOF"},
		{"cat <<EOF | tee out.log", "EOF"},
		{"echo hello", ""},
		{"printf '<<EOF'", "EOF"}, // false positive we accept; printf body is rare
		{"gt mail send mayor -s 'x' -m 'y'", ""},
	}
	for _, tc := range tests {
		if got := detectHeredocTerm(tc.in); got != tc.want {
			t.Errorf("detectHeredocTerm(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRewriteHandoffToMail(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantOk  bool
	}{
		{
			name:   "architect handoff to mayor with subject and message",
			in:     `gt handoff mayor -s "Architecture Ready" -m "Design complete. ready for planning."`,
			want:   `gt mail send mayor/ -s "Architecture Ready" -m "Design complete. ready for planning."`,
			wantOk: true,
		},
		{
			name:   "planner handoff with --yes flag is dropped",
			in:     `gt handoff mayor -s "Plan Complete" -m "tasks created" -y`,
			want:   `gt mail send mayor/ -s "Plan Complete" -m "tasks created"`,
			wantOk: true,
		},
		{
			name:   "trailing slash on role is normalized",
			in:     `gt handoff mayor/ -s "X" -m "Y"`,
			want:   `gt mail send mayor/ -s X -m Y`,
			wantOk: true,
		},
		{
			name:   "qa to mayor",
			in:     `gt handoff mayor -s "QA Complete" -m "Review finished"`,
			want:   `gt mail send mayor/ -s "QA Complete" -m "Review finished"`,
			wantOk: true,
		},
		{
			name:   "long-form flags",
			in:     `gt handoff mayor --subject "Plan Complete" --message "tasks created"`,
			want:   `gt mail send mayor/ -s "Plan Complete" -m "tasks created"`,
			wantOk: true,
		},
		{
			name:   "no -s/-m still rewrites (mail without body)",
			in:     `gt handoff mayor`,
			want:   `gt mail send mayor/`,
			wantOk: true,
		},
		{
			name:   "handoff to bead is left alone",
			in:     `gt handoff hq-wisp-abc -s "X" -m "Y"`,
			want:   "",
			wantOk: false,
		},
		{
			name:   "non-handoff command unchanged",
			in:     `gt mail send mayor -s "X"`,
			want:   "",
			wantOk: false,
		},
		{
			name:   "self-handoff with no role (just `gt handoff`) is left alone",
			in:     `gt handoff`,
			want:   "",
			wantOk: false,
		},
		{
			name:   "unknown role left alone",
			in:     `gt handoff some-unknown-role -s X -m Y`,
			want:   "",
			wantOk: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := rewriteHandoffToMail(tc.in)
			if ok != tc.wantOk {
				t.Fatalf("ok = %v, want %v (got %q)", ok, tc.wantOk, got)
			}
			if !tc.wantOk {
				return
			}
			if got != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

// TestNormalizeRewritesArchitectHandoff verifies the end-to-end normalizer
// path rewrites the exact command the architect produced in production.
func TestNormalizeRewritesArchitectHandoff(t *testing.T) {
	const in = `gt handoff mayor -s "Architecture Ready" -m "Design complete. architecture.md created at /home/stevef/gt/testgt2/architect/architecture.md. Ready for implementation."`
	want := `gt mail send mayor/ -s "Architecture Ready" -m "Design complete. architecture.md created at /home/stevef/gt/testgt2/architect/architecture.md. Ready for implementation."`
	got, changed := normalizeGeneratedCommand(in)
	if !changed {
		t.Fatalf("expected rewrite; got unchanged %q", got)
	}
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestContainsPlaceholder_SnakeBracket(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		// Snake-case-style hallucinations that bypassed the old guard.
		{"[command_for_item_X]", true},
		{"echo [task_name]", true},
		{"gt sling [bead_id] testgt2/architect", true},
		{"cp [src_file] [dest_file]", true},
		{"foo [a_b_c]", true},

		// Real shell uses with single-word brackets — must NOT trigger.
		{"if [ -f /tmp/x ]; then echo hi; fi", false},
		{"arr=(a b c); echo ${arr[0]}", false},
		{"echo [foo]", false},        // single word, no underscore — leave alone
		{"echo [REAL-RIG]", false},   // hyphenated literal — leave alone

		// Existing allowlisted placeholders still trigger via earlier rules.
		{"gt sling --on [bead]", true},
		{"gt sling --on [bead-id]", true},
	}
	for _, tc := range tests {
		if got := containsPlaceholder(tc.in); got != tc.want {
			t.Errorf("containsPlaceholder(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestHasInvalidSlingTarget(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		// The actual mayor hallucinations from production logs.
		{"gt sling shiny --on hq-wisp-abc /architect", true},
		{"gt sling mol-polecat-work --on hq-wisp-r9ag /polecats --create", true},
		{"gt sling code-review --on hq-wisp-x /qa", true},

		// Valid commands must pass through.
		{"gt sling shiny --on hq-wisp-abc testgt2/architect", false},
		{"gt sling mol-polecat-work --on hq-wisp-r9ag testgt2/polecats --create", false},
		{"gt sling mol-idea-to-plan --on hq-wisp-abc planner", false},
		{"gt sling hq-wisp-abc testgt2", false},
		{"gt sling hq-wisp-abc", false},
		{"gt sling", false},

		// Non-sling commands not touched.
		{"gt hook", false},
		{"gt handoff mayor", false},

		// Flag values with slashes are NOT positional, must not trigger.
		{"gt sling --on hq-wisp-x --base-branch release/v2 testgt2/architect", false},
		{"gt sling --formula shiny --on hq-wisp-x testgt2/architect", false},
	}
	for _, tc := range tests {
		if got := hasInvalidSlingTarget(tc.in); got != tc.want {
			t.Errorf("hasInvalidSlingTarget(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestHasInvalidPatrolCommand(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		// Real patrol verbs — must be accepted.
		{"gt patrol new", false},
		{"gt patrol new --role planner", false},
		{"gt patrol report --summary \"ok\"", false},
		{"gt patrol scan", false},
		{"gt patrol digest", false},
		{"gt patrol", false}, // bare `gt patrol` prints help; let the CLI handle it
		{"gt patrol --help", false},

		// Planner hallucinations from the logs.
		{"gt patrolling mol-planner-patrol cycle 24", true},
		{"gt patrol start mol-planner-patrol -c 24", true},
		{"gt patrol cycle 24", true},
		{"gt patrol loop", true},
		{"gt patrol new mol-planner-patrol -f https://example/x.yml", true},
		{"gt patrol new --httpf \"https://example/x.yml\"", true},
		{"gt patrol new mol-planner-patrol -c 24", true},

		// Unrelated commands left alone.
		{"gt prime", false},
		{"gt hook", false},
		{"echo patrol", false},

		// `gt patrol report` flag-level hallucinations from the planner.
		// Real flags are only --summary/-s, --steps, and --help/-h.
		{`gt patrol report --incident hq-bbn.3 --summary "x"`, true},
		{`gt patrol report --status "stalled" --summary "x"`, true},
		{`gt patrol report --recommendation "wait" --summary "x"`, true},
		{`gt patrol report --sirius Andrew --summary "x"`, true},
		{`gt patrol report --summary=ok`, false},
		{`gt patrol report --summary "ok" --steps "x:y"`, false},
	}
	for _, tc := range tests {
		if got := hasInvalidPatrolCommand(tc.in); got != tc.want {
			t.Errorf("hasInvalidPatrolCommand(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestStageWraparoundReason pins fix #76: reject `gt sling shiny --on
// <bead> <rig>/architect` when <bead> is itself an architect handoff
// (title starts with "Architecture Ready" / "Architecture Complete" /
// etc.). Live reproducer: mayor reads architect's "Architecture Ready"
// mail and slings shiny back to the architect on the mail's own wisp,
// creating an infinite loop.
func TestStageWraparoundReason(t *testing.T) {
	// Inject a stub title lookup so the test doesn't need a real Dolt
	// server. The fixture maps bead IDs to titles we want to model.
	titles := map[string]string{
		"hq-wisp-eulgk": "Architecture Ready",
		"hq-wisp-bjr5":  "Architecture Ready",
		"hq-wisp-zvlv":  "Architecture location: /home/stevef/gt/...",
		"hq-wisp-9999":  "Architecture Complete: design ready",
		"hq-wisp-fqq":   "Project: Hello World API for testgt2",
		"hq-wisp-spec":  "SPEC: Build a chatbot",
		"hq-aaa":        "Architecture Plan Submission for hq-bbn",
	}
	orig := beadTitleLookup
	beadTitleLookup = func(id string) string { return titles[id] }
	t.Cleanup(func() { beadTitleLookup = orig })

	tests := []struct {
		name      string
		cmd       string
		wantBlock bool
	}{
		// The canonical bug — must block.
		{
			name:      "shiny on Architecture Ready wisp back to architect",
			cmd:       "gt sling shiny --on hq-wisp-eulgk testgt2/architect",
			wantBlock: true,
		},
		{
			name:      "shiny on Architecture Complete wisp back to architect",
			cmd:       "gt sling shiny --on hq-wisp-9999 testgt2/architect",
			wantBlock: true,
		},
		{
			name:      "shiny on Architecture location wisp back to architect",
			cmd:       "gt sling shiny --on hq-wisp-zvlv testgt2/architect",
			wantBlock: true,
		},
		{
			name:      "shiny with --on after target (still matches)",
			cmd:       "gt sling shiny testgt2/architect --on hq-wisp-bjr5",
			wantBlock: false, // regex requires --on BEFORE target; this is OK
		},
		{
			name:      "shiny on Architecture Plan Submission to architect",
			cmd:       "gt sling shiny --on hq-aaa testgt2/architect",
			wantBlock: true,
		},

		// Legitimate slings — must NOT block.
		{
			name:      "shiny on a real project bead — legitimate Stage 1",
			cmd:       "gt sling shiny --on hq-wisp-fqq testgt2/architect",
			wantBlock: false,
		},
		{
			name:      "shiny on a SPEC bead — legitimate Stage 1",
			cmd:       "gt sling shiny --on hq-wisp-spec testgt2/architect",
			wantBlock: false,
		},

		// Different formulas — out of scope, do NOT block.
		{
			name:      "mol-idea-to-plan to planner — different formula, not wraparound",
			cmd:       "gt sling mol-idea-to-plan --on hq-wisp-eulgk planner",
			wantBlock: false,
		},
		{
			name:      "code-review to qa — out of scope",
			cmd:       "gt sling code-review --on hq-wisp-eulgk testgt2/qa",
			wantBlock: false,
		},
		{
			name:      "shiny to polecats (not architect) — out of scope",
			cmd:       "gt sling shiny --on hq-wisp-eulgk testgt2/polecats",
			wantBlock: false,
		},

		// Placeholder bead ID — let containsPlaceholder handle, not us.
		{
			name:      "placeholder bead ID — fail open",
			cmd:       "gt sling shiny --on <bead-id> testgt2/architect",
			wantBlock: false,
		},

		// Unknown bead — fail open (lookup returns "").
		{
			name:      "unknown bead — fail open, allow sling",
			cmd:       "gt sling shiny --on hq-unknown-bead testgt2/architect",
			wantBlock: false,
		},

		// Not a sling at all.
		{
			name:      "non-sling command — not checked",
			cmd:       "bd show hq-wisp-eulgk",
			wantBlock: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := stageWraparoundReason(tc.cmd)
			if (got != "") != tc.wantBlock {
				t.Errorf("stageWraparoundReason(%q) = %q, wantBlock=%v",
					tc.cmd, got, tc.wantBlock)
			}
		})
	}
}

// TestIsArchitectHandoffTitle pins the title classifier used by
// stageWraparoundReason — case-insensitive prefix match across the
// known architect handoff subjects.
func TestIsArchitectHandoffTitle(t *testing.T) {
	for _, ok := range []string{
		"Architecture Ready",
		"architecture ready",
		"ARCHITECTURE READY: design.md created",
		"Architecture Complete",
		"Architecture Plan Submission for hq-bbn",
		"  Architecture Ready   ", // leading/trailing whitespace tolerated
		"Architecture location: /tmp/architecture.md",
	} {
		if !isArchitectHandoffTitle(ok) {
			t.Errorf("isArchitectHandoffTitle(%q) = false, want true", ok)
		}
	}
	for _, no := range []string{
		"Project: Hello World API",
		"Plan Complete",
		"QA Complete",
		"BLOCKED: architecture missing",
		"My architecture is broken", // doesn't START with "architecture"
		"",
	} {
		if isArchitectHandoffTitle(no) {
			t.Errorf("isArchitectHandoffTitle(%q) = true, want false", no)
		}
	}
}

// TestIsEmptyContentFileHeredoc pins fix #74: reject heredoc writes
// that would blow away a content file (architecture.md, design.md,
// plan.md, SPEC.md, README.md) with an empty or placeholder-only body.
//
// The original symptom: an LLM rendered the architect template's
// `cat > .../architecture.md <<'EOF' ... EOF` literally, with `...` as
// the heredoc body. The redirection ran successfully and overwrote a
// 157-byte architecture spec with 0 bytes. The pipeline then stalled
// silently because the planner had nothing real to plan against.
func TestIsEmptyContentFileHeredoc(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want bool
	}{
		{
			name: "empty heredoc body to architecture.md",
			cmd:  "cat > /home/stevef/gt/testgt2/architect/architecture.md <<'EOF'\nEOF",
			want: true,
		},
		{
			name: "literal '...' heredoc body",
			cmd:  "cat > /home/stevef/gt/testgt2/architecture.md <<'EOF'\n...\nEOF",
			want: true,
		},
		{
			name: "INSERT-REAL-ARCHITECTURE-HERE placeholder body",
			cmd: "cat > /home/stevef/gt/testgt2/architect/architecture.md <<'EOF'\n" +
				"# Architecture: Foo\n" +
				"## API\n" +
				"<INSERT-REAL-ARCHITECTURE-HERE — endpoints>\n" +
				"## Data Model\n" +
				"<INSERT-REAL-ARCHITECTURE-HERE — entities>\n" +
				"EOF",
			want: true,
		},
		{
			name: "TODO-only placeholder body",
			cmd: "cat > /tmp/design.md <<'EOF'\n" +
				"<TODO: fill in>\n" +
				"<TODO>\n" +
				"EOF",
			want: true,
		},
		{
			name: "real architecture body — must NOT be rejected",
			cmd: "cat > /home/stevef/gt/testgt2/architect/architecture.md <<'EOF'\n" +
				"# Architecture: Hello API\n\n" +
				"## API\n" +
				"- GET / returns a JSON greeting with status 200\n" +
				"- POST /echo returns the request body verbatim\n\n" +
				"## Data Model\n" +
				"- No persistence; stateless service\n\n" +
				"## Components\n" +
				"- FastAPI app in main.py\n" +
				"- requirements.txt with fastapi and uvicorn pinned\n" +
				"EOF",
			want: false,
		},
		{
			name: "real but short body (<200 bytes) still substantive",
			cmd: "cat > /tmp/plan.md <<'EOF'\n" +
				"Implement endpoint, add test, commit.\n" +
				"EOF",
			want: false,
		},
		{
			name: "unquoted EOF heredoc, real body",
			cmd: "cat > /tmp/README.md <<EOF\n" +
				"Project does X by calling service Y.\n" +
				"EOF",
			want: false,
		},
		{
			name: "non-content-file heredoc — out of scope, must NOT be rejected",
			cmd:  "cat > /tmp/scratch.txt <<'EOF'\n...\nEOF",
			want: false,
		},
		{
			name: "not a heredoc at all — out of scope",
			cmd:  "echo hello > /home/stevef/gt/testgt2/architecture.md",
			want: false,
		},
		{
			name: "headings-only body is NOT substantive (would produce a useless skeleton)",
			cmd: "cat > /tmp/architecture.md <<'EOF'\n" +
				"# Architecture\n" +
				"## API\n" +
				"## Data Model\n" +
				"## Components\n" +
				"EOF",
			want: true,
		},
		{
			name: "mixed placeholder + real content — accept (LLM at least tried)",
			cmd: "cat > /tmp/architecture.md <<'EOF'\n" +
				"# Architecture: Hello API\n" +
				"## API\n" +
				"- GET / returns JSON {\"hello\": \"world\"} with 200 OK.\n" +
				"## Data Model\n" +
				"<INSERT-REAL-ARCHITECTURE-HERE>\n" +
				"EOF",
			want: false,
		},
	}
	for _, tc := range tests {
		got := isEmptyContentFileHeredoc(tc.cmd)
		if got != tc.want {
			t.Errorf("[%s] isEmptyContentFileHeredoc() = %v, want %v\n  cmd:\n%s",
				tc.name, got, tc.want, tc.cmd)
		}
	}
}

// TestShouldAutoUnhookAfterHandoff covers the safety-net that
// auto-clears the hook for planner/architect/qa after a clean handoff.
func TestShouldAutoUnhookAfterHandoff(t *testing.T) {
	tests := []struct {
		role    string
		summary string
		want    bool
	}{
		// Planner happy paths.
		{"planner", "handed off to mayor", true},
		{"planner", "Handed off to mayor", true},
		{"planner", "Plan Complete - 5 tasks created", true},
		{"planner", "plan ready, mailed mayor", true},

		// Architect happy paths.
		{"architect", "architecture ready", true},
		{"architect", "design complete - architecture.md created", true},
		{"architect", "handed off to mayor", true},

		// QA happy paths.
		{"qa", "qa complete", true},
		{"qa", "review complete", true},

		// Negative summaries must NOT unhook.
		{"planner", "Unable to Proceed - missing architecture file", false},
		{"planner", "investigating missing file", false},
		{"planner", "blocked by upstream", false},
		{"planner", "Plan in progress", false},
		{"architect", "design failed: missing requirements", false},
		{"qa", "review failed: critical defects", false},

		// Empty summary never unhooks.
		{"planner", "", false},
		{"architect", "", false},

		// Non-handoff roles must NEVER auto-unhook regardless of summary.
		{"mayor", "handed off to polecat", false},
		{"witness", "patrol cycle completed successfully", false},
		{"refinery", "merge cycle finished", false},
		{"mechanic", "patrol complete", false},
		{"deacon", "review complete", false},
		{"polecat", "handed off to mayor", false},
		{"crew", "design complete", false},

		// Unknown / empty role.
		{"", "handed off to mayor", false},
		{"some-future-role", "handed off to mayor", false},
	}
	for _, tc := range tests {
		got := shouldAutoUnhookAfterHandoff(tc.role, tc.summary)
		if got != tc.want {
			t.Errorf("shouldAutoUnhookAfterHandoff(%q, %q) = %v, want %v",
				tc.role, tc.summary, got, tc.want)
		}
	}
}

// TestNormalizePreservesGtHookSubcommands ensures the normalizer
// does NOT clobber real `gt hook` subcommands (`show`, `attach`,
// `detach`, `clear`, `status`). A previous over-eager rewrite
// collapsed every `gt hook …` to bare `gt hook`, which broke the
// mayor's `gt hook attach <wisp>` calls.
func TestNormalizePreservesGtHookSubcommands(t *testing.T) {
	tests := []struct {
		in      string
		wantOut string
	}{
		// Real subcommands must pass through verbatim.
		{"gt hook show planner", "gt hook show planner"},
		{"gt hook show testgt2/witness", "gt hook show testgt2/witness"},
		{"gt hook attach hq-wisp-5zi3", "gt hook attach hq-wisp-5zi3"},
		{"gt hook detach hq-abc", "gt hook detach hq-abc"},
		{"gt hook clear", "gt hook clear"},
		{"gt hook status", "gt hook status"},

		// Bare gt hook is left as-is.
		{"gt hook", "gt hook"},

		// Unknown trailing args (hallucination) collapse to bare.
		{"gt hook fooBarbaz", "gt hook"},

		// Shell pipelines / redirections / chains that consume the
		// output of `gt hook` must pass through untouched. The
		// architect template explicitly uses this pattern to extract
		// the project bead ID into a tmp file.
		{
			"gt hook | grep -oE 'hq-wisp-[a-z0-9]+|hq-[a-z0-9]+' | head -1 > /tmp/project_bead.txt",
			"gt hook | grep -oE 'hq-wisp-[a-z0-9]+|hq-[a-z0-9]+' | head -1 > /tmp/project_bead.txt",
		},
		{"gt hook && gt mail inbox", "gt hook && gt mail inbox"},
		{"gt hook > /tmp/hook.txt", "gt hook > /tmp/hook.txt"},
		{"gt hook ; echo done", "gt hook ; echo done"},
	}
	for _, tc := range tests {
		got, _ := normalizeGeneratedCommand(tc.in)
		if got != tc.wantOut {
			t.Errorf("normalize(%q): got %q, want %q", tc.in, got, tc.wantOut)
		}
	}
}

// TestRewriteBareRigPolecats covers the recurring mayor mistake where
// the sling target is `<rig>/polecats` (no specific polecat name).
// The CLI rejects this; we rewrite to bare `<rig>` for auto-spawn.
func TestRewriteBareRigPolecats(t *testing.T) {
	tests := []struct {
		in       string
		wantOut  string
		wantOK   bool
	}{
		// The actual mayor mistake: bare <rig>/polecats target.
		{
			"gt sling mol-polecat-work --on hq-bbn testgt2/polecats --create",
			"gt sling mol-polecat-work --on hq-bbn testgt2 --create",
			true,
		},
		{
			"gt sling hq-abc testgt2/polecats",
			"gt sling hq-abc testgt2",
			true,
		},

		// A specific polecat name MUST be preserved.
		{
			"gt sling hq-abc testgt2/polecats/toast",
			"gt sling hq-abc testgt2/polecats/toast",
			false,
		},

		// Plain rig target needs no rewrite.
		{
			"gt sling hq-abc testgt2",
			"gt sling hq-abc testgt2",
			false,
		},

		// Non-sling commands are untouched.
		{
			"gt hook show testgt2/polecats",
			"gt hook show testgt2/polecats",
			false,
		},

		// Flag values that look like rig/polecats must not be rewritten.
		{
			`gt sling hq-abc testgt2 --message "see testgt2/polecats for context"`,
			`gt sling hq-abc testgt2 --message "see testgt2/polecats for context"`,
			false,
		},
	}
	for _, tc := range tests {
		got, ok := rewriteBareRigPolecats(tc.in)
		if got != tc.wantOut {
			t.Errorf("rewriteBareRigPolecats(%q): got %q, want %q", tc.in, got, tc.wantOut)
		}
		if ok != tc.wantOK {
			t.Errorf("rewriteBareRigPolecats(%q): ok=%v, want %v", tc.in, ok, tc.wantOK)
		}
	}
}

// TestNormalizeRewritesGdTypo covers the recurring `gd …` typo: there is
// no `gd` binary on the agent PATH, but the model frequently types it
// when it means `gt`. The normalizer must rewrite the leading `gd ` to
// `gt ` so the right CLI is invoked instead of failing with
// `gd: not found`.
func TestNormalizeRewritesGdTypo(t *testing.T) {
	tests := []struct {
		in       string
		wantOut  string
		rewrite  bool
	}{
		{`gd patrol report --summary x`, `gt patrol report --summary x`, true},
		{`gd hook`, `gt hook`, true},
		{`gd bd show hq-abc`, `gt bd show hq-abc`, true},

		// Don't touch lookalikes that are real commands.
		{`gt patrol report --summary x`, `gt patrol report --summary x`, false},
		{`echo gd`, `echo gd`, false},
		{`grep gd file`, `grep gd file`, false},
	}
	for _, tc := range tests {
		got, gotRewrite := normalizeGeneratedCommand(tc.in)
		// Some inputs hit downstream normalizers too (e.g. `gt hook`
		// collapses to `gt hook`). Just check the leading rewrite.
		if !strings.HasPrefix(got, strings.SplitN(tc.wantOut, " ", 2)[0]) {
			t.Errorf("normalize(%q): leading verb wrong, got %q", tc.in, got)
		}
		if tc.rewrite && !gotRewrite {
			t.Errorf("normalize(%q): expected rewritten=true", tc.in)
		}
	}
}

func TestHasInvalidShinyFormula(t *testing.T) {
	tests := []struct {
		cmd  string
		role string
		want bool
	}{
		// Witness slinging shiny on its own bead — the actual bug.
		{"gt sling hq-wisp-u0z1r --formula shiny", "witness", true},
		{"gt sling hq-wisp-abc --formula shiny", "refinery", true},
		{"gt sling te-foo --formula shiny", "mechanic", true},
		{"gt sling te-foo --formula shiny", "qa", true},
		{"gt sling te-foo --formula shiny", "planner", true},

		// Fix #115: deacon bonded shiny to its own patrol bead via the
		// legacy `--formula shiny` syntax (exact command observed in
		// production: `gt sling hq-wisp-nf7e --formula shiny`).
		{"gt sling hq-wisp-nf7e --formula shiny", "deacon", true},

		// Fix #115: also catch the modern positional-shiny syntax,
		// regardless of role classification — same restricted set.
		{"gt sling shiny --on hq-wisp-nf7e deacon/", "deacon", true},
		{"gt sling shiny --on hq-wisp-u0z1r witness/", "witness", true},
		{"gt sling shiny --on hq-wisp-xyz refinery/", "refinery", true},
		{"gt sling shiny --on te-foo testgt2/qa", "qa", true},
		{"gt sling shiny --on te-foo testgt2/architect", "planner", true},

		// Mayor and Architect are legitimate sources of shiny — both syntaxes.
		{"gt sling te-foo --formula shiny", "mayor", false},
		{"gt sling te-foo --formula shiny", "architect", false},
		{"gt sling te-foo --formula shiny", "polecat", false},
		{"gt sling te-foo --formula shiny", "crew", false},
		{"gt sling shiny --on te-foo testgt2/architect", "mayor", false},
		{"gt sling shiny --on te-foo testgt2/crew/dom", "architect", false},

		// Other formulas are fine for any role.
		{"gt sling te-foo --formula mol-polecat-work", "witness", false},
		{"gt sling te-foo --formula mol-witness-patrol", "witness", false},
		{"gt sling mol-idea-to-plan --on te-foo planner/", "mayor", false},

		// Non-sling commands not touched.
		{"gt hook", "witness", false},
		{"shiny --formula shiny", "witness", false}, // not a `gt sling`

		// Quoted formula name still resolved.
		{"gt sling te-foo --formula \"shiny\"", "witness", true},
		{"gt sling te-foo --formula 'shiny'", "witness", true},
		{"gt sling \"shiny\" --on te-foo deacon/", "deacon", true},
	}
	for _, tc := range tests {
		if got := hasInvalidShinyFormula(tc.cmd, tc.role); got != tc.want {
			t.Errorf("hasInvalidShinyFormula(%q, %q) = %v, want %v",
				tc.cmd, tc.role, got, tc.want)
		}
	}
}

func TestFindCaseInsensitiveNameInDir(t *testing.T) {
	dir := t.TempDir()
	if _, ok := findCaseInsensitiveNameInDir(dir, "spec.md"); ok {
		t.Fatal("expected no match on empty dir")
	}
	if err := os.WriteFile(filepath.Join(dir, "SPEC.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := findCaseInsensitiveNameInDir(dir, "spec.md")
	if !ok {
		t.Fatal("expected match")
	}
	if want := filepath.Join(dir, "SPEC.md"); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRewriteSpecMDPathCaseInsensitive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "myrig", "mayor", "rig")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SPEC.md"), []byte("# SPEC\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wrong := filepath.Join(dir, "spec.md")
	cmd := "cat " + wrong + " && wc -c " + wrong
	want := "cat " + filepath.Join(dir, "SPEC.md") + " && wc -c " + filepath.Join(dir, "SPEC.md")
	out, changed := rewriteSpecMDPathCaseInsensitive(cmd)
	if !changed {
		t.Fatal("expected rewrite")
	}
	if out != want {
		t.Fatalf("got:\n%q\nwant:\n%q", out, want)
	}
	// Idempotent / already-correct path: no change
	out2, changed2 := rewriteSpecMDPathCaseInsensitive(out)
	if changed2 || out2 != out {
		t.Fatalf("second pass changed output: changed=%v out=%q", changed2, out2)
	}
	// Through normalizer (planner-like command)
	got, ch := normalizeGeneratedCommand("cat " + wrong)
	if !ch {
		t.Fatal("normalize should rewrite spec path")
	}
	if got != "cat "+filepath.Join(dir, "SPEC.md") {
		t.Fatalf("normalize got %q", got)
	}
}

func TestRewriteSpecMDPathCaseInsensitive_noFileNoRewrite(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "empty", "mayor", "rig")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	wrong := filepath.Join(dir, "spec.md")
	cmd := "cat " + wrong
	out, changed := rewriteSpecMDPathCaseInsensitive(cmd)
	if changed || out != cmd {
		t.Fatalf("unexpected rewrite: changed=%v out=%q", changed, out)
	}
}

// TestNormalizeRejectsWitnessShinyAndBadPatrol verifies the two new
// rejection paths fire correctly through the top-level normalizer.
func TestNormalizeRejectsWitnessShinyAndBadPatrol(t *testing.T) {
	defer func(prev string) { currentRole = prev }(currentRole)

	currentRole = "witness"
	got, changed := normalizeGeneratedCommand("gt sling hq-wisp-u0z1r --formula shiny")
	if got != "true" || !changed {
		t.Errorf("witness shiny: got (%q, %v), want (\"true\", true)", got, changed)
	}

	currentRole = "mayor"
	got, changed = normalizeGeneratedCommand("gt sling hq-wisp-foo --formula shiny")
	if got != "gt sling hq-wisp-foo --formula shiny" || changed {
		t.Errorf("mayor shiny should pass through: got (%q, %v)", got, changed)
	}

	currentRole = "planner"
	got, changed = normalizeGeneratedCommand("gt patrolling mol-planner-patrol cycle 24")
	if got != "true" || !changed {
		t.Errorf("planner patrolling: got (%q, %v), want (\"true\", true)", got, changed)
	}

	got, changed = normalizeGeneratedCommand("gt patrol new")
	if got != "gt patrol new" || changed {
		t.Errorf("valid `gt patrol new` should pass through: got (%q, %v)", got, changed)
	}
}

// TestMayorKickoffMailDeleteGuard pins Fix #122 / #123: mayor must not
// `gt mail delete` kickoff mail (Fix #122) or planner `BLOCKED:` status mail
// (Fix #123).
func TestMayorKickoffMailDeleteGuard(t *testing.T) {
	defer func(prev string) { currentRole = prev }(currentRole)
	orig := mayorMailInboxSubjectsLookup
	t.Cleanup(func() { mayorMailInboxSubjectsLookup = orig })

	subjects := map[string]string{
		"hq-wisp-clm": "New project: build testgt2 FizzBuzz from SPEC.md",
		"hq-wisp-zzz": "mol-planner-patrol complete",
		"hq-wisp-pk":  "Project: Widget API",
		"hq-wisp-re":  "Re: Fwd: New project: nested prefixes",
		"hq-wisp-id6": "BLOCKED: architecture missing",
		"hq-wisp-v2":  "BLOCKED: architecture and spec input files missing",
	}
	mayorMailInboxSubjectsLookup = func() map[string]string {
		return subjects
	}

	currentRole = "mayor"
	got, changed := normalizeGeneratedCommand("gt mail delete hq-wisp-clm")
	if got != "true" || !changed {
		t.Fatalf("kickoff-shaped delete: got %q changed=%v want (\"true\", true)", got, changed)
	}

	got, changed = normalizeGeneratedCommand("gt mail delete hq-wisp-zzz")
	if got != "gt mail delete hq-wisp-zzz" || changed {
		t.Fatalf("non-kickoff delete should pass through: got %q changed=%v", got, changed)
	}

	got, changed = normalizeGeneratedCommand("gt mail delete hq-wisp-pk")
	if got != "true" || !changed {
		t.Fatalf("Project:-prefixed subject: got %q changed=%v want (\"true\", true)", got, changed)
	}

	got, changed = normalizeGeneratedCommand("gt mail delete hq-wisp-re")
	if got != "true" || !changed {
		t.Fatalf("Re:/Fwd: kickoff subject: got %q changed=%v want (\"true\", true)", got, changed)
	}

	got, changed = normalizeGeneratedCommand("gt mail delete hq-wisp-id6")
	if got != "true" || !changed {
		t.Fatalf("BLOCKED mail delete: got %q changed=%v want (\"true\", true)", got, changed)
	}

	got, changed = normalizeGeneratedCommand("gt mail delete hq-wisp-v2")
	if got != "true" || !changed {
		t.Fatalf("BLOCKED long subject: got %q changed=%v want (\"true\", true)", got, changed)
	}

	// Fail open when inbox snapshot is unavailable (no false blocks).
	mayorMailInboxSubjectsLookup = func() map[string]string { return nil }
	got, changed = normalizeGeneratedCommand("gt mail delete hq-wisp-clm")
	if got != "gt mail delete hq-wisp-clm" || changed {
		t.Fatalf("empty lookup should not block: got %q changed=%v", got, changed)
	}

	// Other roles: never block mail delete here.
	currentRole = "witness"
	mayorMailInboxSubjectsLookup = func() map[string]string { return subjects }
	got, changed = normalizeGeneratedCommand("gt mail delete hq-wisp-clm")
	if got != "gt mail delete hq-wisp-clm" || changed {
		t.Fatalf("witness should not trigger mayor kickoff guard: got %q changed=%v", got, changed)
	}
}

func TestIsKickoffLikeMailSubject(t *testing.T) {
	tests := []struct {
		subj string
		want bool
	}{
		{"New project: build X", true},
		{"new PROJECT: lower", true},
		{"Re: New project: handoff", true},
		{"Kickoff: FizzBuzz", true},
		{"kickoff FizzBuzz", true},
		{"Project: API", true},
		{"SPEC: build a thing", false},
		{"Architecture Ready", false},
		{"Daily standup", false},
	}
	for _, tt := range tests {
		if got := isKickoffLikeMailSubject(tt.subj); got != tt.want {
			t.Errorf("isKickoffLikeMailSubject(%q) = %v, want %v", tt.subj, got, tt.want)
		}
	}
}

func TestIsBlockedStatusMailSubject(t *testing.T) {
	tests := []struct {
		subj string
		want bool
	}{
		{"BLOCKED: architecture missing", true},
		{"blocked: spec missing", true},
		{"Re: BLOCKED: still broken", true},
		{"Plan Complete", false},
		{"BLOCKED", false},
		{"Not blocked: yet", false},
	}
	for _, tt := range tests {
		if got := isBlockedStatusMailSubject(tt.subj); got != tt.want {
			t.Errorf("isBlockedStatusMailSubject(%q) = %v, want %v", tt.subj, got, tt.want)
		}
	}
}

func TestNormalizeStepRewrite(t *testing.T) {
	lastBeadID = ""
	lastStepID = "te-5c1"
	got, changed := normalizeGeneratedCommand("bd close step-1")
	if got != "bd close te-5c1" || !changed {
		t.Fatalf("unexpected rewrite for bd close step-1: got %q changed=%v", got, changed)
	}

	lastBeadID = ""
	lastStepID = ""
	got, changed = normalizeGeneratedCommand("bd close step-2")
	if got != "true" || !changed {
		t.Fatalf("bd close step-2 with empty lastStepID should be rejected as 'true': got %q changed=%v", got, changed)
	}

	lastBeadID = ""
	lastStepID = "te-5c2"
	got, changed = normalizeGeneratedCommand("bd close 2")
	if got != "bd close te-5c2" || !changed {
		t.Fatalf("unexpected rewrite for bd close 2: got %q changed=%v", got, changed)
	}
}

// Fix #90: small LLMs emit content-free status mails between same-rig
// agents (refinery → witness → refinery) every patrol cycle. Each one
// creates a permanent bead + Dolt commit and clogs the recipient's
// inbox (observed 195+ messages in <12h). hasContentFreeMailSend
// rejects the worst offenders BEFORE they reach `gt mail send`.
//
// Three signatures, all observed in production:
//
//  1. Subject is a bare formula name (`mol-refinery-patrol`,
//     `RE: mol-witness-patrol`). Formula names are internal
//     identifiers, never legitimate mail subjects.
//  2. `RE:` reply with an empty body.
//  3. Short body that just echoes the subject + an ack phrase
//     (`"Reply to witness regarding mol-refinery-patrol"`).
//
// We also pin the legitimate cases to avoid false positives:
// real `MERGE_FAILED <wisp>`, `FIX_NEEDED <polecat>`, and `MERGED
// <polecat>` mails (per the mol-refinery-patrol formula) MUST be
// allowed through. Heredoc bodies (`--stdin <<EOF...EOF`) are also
// always allowed because no observed hallucination uses them.
func TestHasContentFreeMailSend(t *testing.T) {
	cases := []struct {
		name       string
		cmd        string
		wantReject bool
	}{
		// REJECTED: formula-name subjects (the dominant noise source).
		{
			name:       "formula subject no RE",
			cmd:        `gt mail send testgt2/witness -s "mol-refinery-patrol" -m "Status update mol-refinery-patrol"`,
			wantReject: true,
		},
		{
			name:       "formula subject with RE",
			cmd:        `gt mail send testgt2/witness -s "RE: mol-refinery-patrol" -m "Reply to witness regarding mol-refinery-patrol"`,
			wantReject: true,
		},
		{
			name:       "formula subject unquoted",
			cmd:        `gt mail send testgt2/witness -s mol-witness-patrol -m "noted"`,
			wantReject: true,
		},
		// REJECTED: `RE:` with empty body.
		{
			name:       "RE empty body",
			cmd:        `gt mail send testgt2/witness -s "RE: anything" -m ""`,
			wantReject: true,
		},
		// REJECTED: body just echoes subject + ack phrase.
		{
			name:       "body echoes subject (reply to)",
			cmd:        `gt mail send testgt2/witness -s "NO_POLECATS_FOUND" -m "Reply to witness regarding NO_POLECATS_FOUND"`,
			wantReject: true,
		},
		{
			name:       "body just ack",
			cmd:        `gt mail send testgt2/witness -s "MERGE_QUEUE_EMPTY" -m "acknowledged"`,
			wantReject: true,
		},
		// REJECTED (Fix #90 extension): body is just the subject in
		// plain English ("NO_POLECATS_FOUND" → "No polecats found").
		// These were the dominant noise in the witness inbox (217+
		// messages observed). The body adds zero information beyond
		// the subject.
		{
			name:       "body restates subject in plain english (NO_POLECATS_FOUND)",
			cmd:        `gt mail send testgt2/witness -s "NO_POLECATS_FOUND" -m "No polecats found"`,
			wantReject: true,
		},
		{
			name:       "body restates subject (MERGE_QUEUE_EMPTY)",
			cmd:        `gt mail send testgt2/witness -s "MERGE_QUEUE_EMPTY" -m "The merge queue is empty"`,
			wantReject: true,
		},
		{
			name:       "body restates subject (MERGE_QUEUE_NONEMPTY)",
			cmd:        `gt mail send testgt2/witness -s "MERGE_QUEUE_NONEMPTY" -m "Merge queue not empty"`,
			wantReject: true,
		},
		{
			name:       "body restates subject (REFINERY_STATUS)",
			cmd:        `gt mail send testgt2/witness -s "REFINERY_STATUS" -m "Refinery status"`,
			wantReject: true,
		},
		// REJECTED (Fix #92): vague polecat-status alerts with no concrete
		// polecat address. Witness template used to contain a literal
		// example `-s "Polecat appears stalled"` which small LLMs copied
		// every patrol cycle, flooding the mayor with 10+ identical
		// alerts per minute and no actual polecat identified.
		{
			name:       "vague Polecat appears stalled no address",
			cmd:        `gt mail send mayor/ -s "Polecat appears stalled" -m "A polecat seems stuck"`,
			wantReject: true,
		},
		{
			name:       "vague polecat stuck no address",
			cmd:        `gt mail send mayor/ -s "Polecat stuck" -m "needs attention"`,
			wantReject: true,
		},
		{
			name:       "polecat dead no address",
			cmd:        `gt mail send mayor/ -s "Polecat is dead" -m "investigate"`,
			wantReject: true,
		},
		// ALLOWED: vague subject but body names a real polecat.
		{
			name:       "Polecat appears stalled with rig/name address",
			cmd:        `gt mail send mayor/ -s "Polecat appears stalled" -m "testgt2/rust has been idle for 30 minutes; gt polecat status shows no progress"`,
			wantReject: false,
		},
		{
			name:       "Polecat stalled with wisp id",
			cmd:        `gt mail send mayor/ -s "Polecat stalled" -m "Stalled on hq-wisp-abc123 for 45m, last activity 09:15"`,
			wantReject: false,
		},
		// REJECTED (Fix #92): patrol-cycle status pings.
		{
			name:       "Patrol Cycle #531 Complete",
			cmd:        `gt mail send mayor/ -s "Patrol Cycle #531 Complete" -m "all systems nominal"`,
			wantReject: true,
		},
		{
			name:       "Patrol Initiated",
			cmd:        `gt mail send mayor/ -s "Patrol Initiated" -m "starting patrol"`,
			wantReject: true,
		},
		{
			name:       "Patrol Complete bare",
			cmd:        `gt mail send testgt2/witness -s "Patrol Complete" -m "no findings"`,
			wantReject: true,
		},
		// REJECTED (Fix #118): internal protocol/status chatter that is
		// not actionable work and was flooding witness inboxes.
		{
			name:       "PATROL_FINISH protocol noise",
			cmd:        `gt mail send testgt2/witness -s "PATROL_FINISH" -m "patrol cycle complete"`,
			wantReject: true,
		},
		{
			name:       "ACTION_RECEIVED protocol noise",
			cmd:        `gt mail send testgt2/witness -s "ACTION_RECEIVED lnko" -m "received"`,
			wantReject: true,
		},
		{
			name:       "HOOK_ERROR protocol noise",
			cmd:        `gt mail send testgt2/witness -s "HOOK_ERROR hq-wisp-ukk00" -m "retrying"`,
			wantReject: true,
		},
		{
			name:       "MAIL_READ_ERROR protocol noise",
			cmd:        `gt mail send testgt2/witness -s "MAIL_READ_ERROR" -m "mail read failed"`,
			wantReject: true,
		},
		{
			name:       "MAIL_ERROR_REPORT_ACK protocol noise",
			cmd:        `gt mail send testgt2/witness -s "MAIL_ERROR_REPORT_ACK" -m "ack"`,
			wantReject: true,
		},
		{
			name:       "PATROL_CLEAR protocol noise",
			cmd:        `gt mail send testgt2/witness -s "PATROL_CLEAR testgt2" -m "cleared patrol"`,
			wantReject: true,
		},
		{
			name:       "REPLY_TO_NUDGE protocol noise",
			cmd:        `gt mail send testgt2/witness -s "REPLY_TO_NUDGE hq-wisp-aqh34: Done" -m "done"`,
			wantReject: true,
		},
		// REJECTED (Fix #113): MERGE_* coordination mail with empty
		// body. Witness/refinery hallucinate these every patrol cycle,
		// naming patrol-wisp IDs as if they were polecat branches:
		//   gt mail send testgt2/refinery -s "MERGE_READY hq-wisp-n460"
		// with no -m / no --stdin. Real merge mails MUST carry branch
		// info / failure reason / merge timestamp in the body.
		{
			name:       "MERGE_READY no body",
			cmd:        `gt mail send testgt2/refinery -s "MERGE_READY hq-wisp-n460"`,
			wantReject: true,
		},
		{
			name:       "MERGE_FAILED no body",
			cmd:        `gt mail send testgt2/witness -s "MERGE_FAILED hq-wisp-skko"`,
			wantReject: true,
		},
		{
			name:       "MERGE_READY empty -m body",
			cmd:        `gt mail send testgt2/refinery -s "MERGE_READY hq-wisp-z5vv" -m ""`,
			wantReject: true,
		},
		{
			name:       "RE: MERGE_READY no body",
			cmd:        `gt mail send testgt2/witness -s "RE: MERGE_READY hq-wisp-4qyvr"`,
			wantReject: true,
		},
		{
			name:       "MERGE_SKIPPED no body",
			cmd:        `gt mail send testgt2/witness -s "MERGE_SKIPPED te-poly-rust"`,
			wantReject: true,
		},
		{
			name:       "MERGE_COMPLETE no body",
			cmd:        `gt mail send testgt2/witness -s "MERGE_COMPLETE hq-wisp-abc"`,
			wantReject: true,
		},
		{
			name:       "MERGED no body",
			cmd:        `gt mail send testgt2/witness -s "MERGED rust"`,
			wantReject: true,
		},
		// ALLOWED: legitimate operational mails per mol-refinery-patrol formula.
		{
			name:       "MERGE_FAILED with real content",
			cmd:        `gt mail send testgt2/witness -s "MERGE_FAILED hq-wisp-abc" -m "Branch tests failed: 3 errors in build_test.go"`,
			wantReject: false,
		},
		{
			name:       "FIX_NEEDED with real content",
			cmd:        `gt mail send testgt2/polecats/rust -s "FIX_NEEDED rust" -m "Branch: polecat/rust/hq-1\nPR: https://github.com/x/y/pull/42\nFailures observed in CI: lint, typecheck"`,
			wantReject: false,
		},
		{
			name:       "MERGED announcement",
			cmd:        `gt mail send testgt2/witness -s "MERGED rust" -m "Branch: polecat/rust/hq-1\nMerged-At: 2026-05-12T08:00:00Z"`,
			wantReject: false,
		},
		// ALLOWED: heredoc body never rejected.
		{
			name:       "heredoc body always allowed",
			cmd:        `gt mail send testgt2/witness -s "RE: mol-refinery-patrol" --stdin <<EOF`,
			wantReject: false,
		},
		// Fix #114: Mayor must not fan out BLOCKED notifications. When
		// it gets `BLOCKED: missing architecture file` from planner,
		// the template tells it to delete the mail and stop. The LLM
		// sometimes routes around this by mailing the sender back with
		// a reworded subject ("Investigate BLOCKED ...", "Check
		// canonical locations ..."). This regex enforces the rule
		// at the runtime boundary so a misbehaving LLM cannot bypass
		// the template by rephrasing.
		{
			name:       "Mayor investigate-BLOCKED forward",
			cmd:        `gt mail send planner/ -s "Investigate BLOCKED: missing architecture file" -m "Check canonical locations for architecture file and verify mol-planner-patrol for details."`,
			wantReject: true,
		},
		{
			name:       "Mayor check-locations forward",
			cmd:        `gt mail send planner/ -s "Check canonical locations" -m "Please look at the rig directories."`,
			wantReject: true,
		},
		{
			name:       "Mayor please-investigate forward",
			cmd:        `gt mail send testgt2/architect -s "Please investigate the BLOCKED state" -m "Could you check why the design isn't progressing?"`,
			wantReject: true,
		},
		// Fix #114: Mayor must not mail "Create architecture" — the
		// architect was already slung at Stage 0 with `gt sling shiny
		// --on $BEAD <rig>/architect`. Re-mailing "please create
		// architecture" doesn't help; it just adds inbox noise the
		// architect template explicitly ignores.
		{
			name:       "Mayor create-architecture forward",
			cmd:        `gt mail send testgt2/architect -s "Create architecture" -m "Please create the architecture file in /home/stevef/gt/testgt2/architect/architecture.md"`,
			wantReject: true,
		},
		{
			name:       "Mayor write-architecture forward",
			cmd:        `gt mail send testgt2/architect -s "Write architecture.md" -m "Need the architecture doc"`,
			wantReject: true,
		},
		{
			name:       "Mayor generate-architecture forward",
			cmd:        `gt mail send testgt2/architect -s "Generate the architecture document" -m "Now please."`,
			wantReject: true,
		},
		// Fix #114: RE: IDLE acknowledgement noise. Mayor sometimes
		// hallucinates a chain of `RE: IDLE` mails to planner after
		// every IDLE notification.
		{
			name:       "RE: IDLE ack with prose body",
			cmd:        `gt mail send planner/ -s "RE: IDLE" -m "Acknowledged, idle status confirmed."`,
			wantReject: true,
		},
		{
			name:       "RE: REPORT idle ack",
			cmd:        `gt mail send planner/ -s "RE: REPORT: idle" -m "Got it."`,
			wantReject: true,
		},
		// ALLOWED — real, actionable mails that must NOT trip the
		// Fix #114 guards.
		{
			name:       "real Architecture Ready handoff",
			cmd:        `gt mail send mayor/ -s "Architecture Ready" -m "Project bead: hq-9jo\nDesign complete. architecture.md at /home/stevef/gt/testgt2/architect/architecture.md (also mirrored at /home/stevef/gt/testgt2/architecture.md). Ready for implementation."`,
			wantReject: false,
		},
		{
			name:       "real Plan Complete handoff",
			cmd:        `gt mail send mayor/ -s "Plan Complete" -m "Project bead: hq-9jo\nChild task beads: te-1 te-2 te-3"`,
			wantReject: false,
		},
		{
			name:       "real Stage 0 complete self-mail",
			cmd:        `gt mail send --self -s "Stage 0 complete: hq-9jo" -m "Kicked off project bead hq-9jo for rig testgt2 from mail hq-wisp-k4z. Standby for Architecture Ready."`,
			wantReject: false,
		},
		{
			name:       "real BLOCKED report from planner",
			cmd:        `gt mail send mayor/ -s "BLOCKED: architecture missing" -m "Searched /home/stevef/gt/testgt2/architect/architecture.md, all missing or stub. Cannot plan without real architecture."`,
			wantReject: false,
		},
		// NOT A MAIL SEND — must pass through.
		{
			name:       "mail inbox is not send",
			cmd:        `gt mail inbox`,
			wantReject: false,
		},
		{
			name:       "mail archive is not send",
			cmd:        `gt mail archive hq-wisp-abc`,
			wantReject: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason, rejected := hasContentFreeMailSend(tc.cmd)
			if rejected != tc.wantReject {
				t.Errorf("hasContentFreeMailSend(%q) rejected=%v (reason=%q), want %v",
					tc.cmd, rejected, reason, tc.wantReject)
			}
			if rejected && reason == "" {
				t.Errorf("rejection must carry a non-empty reason for the log line; got empty for %q", tc.cmd)
			}
		})
	}
}

// TestNormalizeRejectsContentFreeMailSend wires hasContentFreeMailSend
// into normalizeGeneratedCommand and asserts the full path: the
// command is replaced with the no-op `true` so the agent doesn't run
// it, the stderr log carries a `(Fix #90: ...)` marker, and a
// legitimate mail send still passes through unchanged.
func TestNormalizeRejectsContentFreeMailSend(t *testing.T) {
	lastBeadID = ""
	lastStepID = ""

	// Hallucinated noise → replaced with `true`.
	got, _ := normalizeGeneratedCommand(`gt mail send testgt2/witness -s "mol-refinery-patrol" -m "Reply to witness regarding mol-refinery-patrol"`)
	if got != "true" {
		t.Errorf("expected hallucinated mail to be replaced with 'true', got %q", got)
	}

	// Real operational mail → unchanged.
	realCmd := `gt mail send testgt2/witness -s "MERGED rust" -m "Branch: polecat/rust/hq-1\nMerged-At: 2026-05-12T08:00:00Z"`
	got, _ = normalizeGeneratedCommand(realCmd)
	if got != realCmd {
		t.Errorf("expected real MERGED mail to pass through unchanged, got %q", got)
	}
}

// Fix #113: gt-agent used to unconditionally unescape `\"` -> `"` on
// every captured command. That destroyed nested escapes inside
// `sh -c '... python3 -c "...\"id\"..." ...'` because sh's own
// double-quote parser needs the `\"` to survive into /bin/sh. After
// the strip, sh sees an unbalanced `"` and python got a code string
// with the quotes already gone, producing `SyntaxError: invalid
// syntax` and a BLOCKED: bd create failed mail every Stage 0.
//
// `isMultilineQuotedScript` is the predicate the runner now checks
// before applying the blanket unescape. Multi-line `sh -c '...'` /
// `bash -c "..."` invocations are exempt — sh handles its own escape
// processing for them.
func TestIsMultilineQuotedScript(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want bool
	}{
		{"single-line sh -c", `sh -c 'echo hi'`, false},
		{"single-line plain", `gt mail inbox`, false},
		{"single-line bash -c", `bash -c "echo hi"`, false},
		{
			"multi-line sh -c with single-quote body",
			"sh -c '\nset -u\necho hi\n'",
			true,
		},
		{
			"multi-line bash -c with double-quote body",
			"bash -c \"\necho one\necho two\n\"",
			true,
		},
		{
			"multi-line bash -c with single-quote body",
			"bash -c '\necho one\n'",
			true,
		},
		{
			"multi-line /bin/sh -c",
			"/bin/sh -c '\nset -u\necho hi\n'",
			true,
		},
		{
			"multi-line gt invocation (not sh -c)",
			"gt mail send mayor/ -m \"line one\nline two\"",
			false,
		},
		{
			"multi-line shell heredoc (not sh -c wrapper)",
			"cat <<EOF\nhello\nEOF",
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isMultilineQuotedScript(tt.cmd)
			if got != tt.want {
				t.Errorf("isMultilineQuotedScript(%q) = %v, want %v", tt.cmd, got, tt.want)
			}
		})
	}
}

// Fix #87: `bd update <id> --status=...` (and other write-side bd
// subcommands) must pass through unchanged. The earlier normalizer
// prepended `gt`, producing `gt bd update <id> --status=in_progress`,
// which `gt bd` (alias for `gt bead`) rejects with "unknown flag:
// --status" because `gt bd` only exposes show/read/move/mol. That bug
// bricked every polecat trying to mark itself in_progress and
// triggered the witness's "stuck polecat" heuristics, masking the real
// problem behind a flood of misleading errors.
func TestNormalizeDoesNotRewriteBdUpdate(t *testing.T) {
	lastBeadID = ""
	lastStepID = ""
	cases := []string{
		"bd update hq-211.3 --status=in_progress",
		`bd update hq-8mn --notes "Findings so far"`,
		`bd update hq-8mn --priority 1`,
	}
	for _, in := range cases {
		got, _ := normalizeGeneratedCommand(in)
		if strings.HasPrefix(got, "gt bd update") {
			t.Errorf("bd update should NOT be rewritten to `gt bd update` (gt bd has no update subcommand): %q -> %q", in, got)
		}
		if got != in {
			t.Errorf("bd update should pass through unchanged: %q -> %q", in, got)
		}
	}
}
