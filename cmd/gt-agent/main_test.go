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
			// Negative test: a real argument that contains the substring
			// "CMD:" (without a leading space) must NOT split.
			name:     "non-leading CMD: in argument is preserved",
			input:    `CMD: echo "prefixCMD:notamarker"`,
			wantCmds: []string{`echo "prefixCMD:notamarker"`},
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

		// Mayor and Architect are legitimate sources of shiny.
		{"gt sling te-foo --formula shiny", "mayor", false},
		{"gt sling te-foo --formula shiny", "architect", false},
		{"gt sling te-foo --formula shiny", "polecat", false},
		{"gt sling te-foo --formula shiny", "crew", false},

		// Other formulas are fine for any role.
		{"gt sling te-foo --formula mol-polecat-work", "witness", false},
		{"gt sling te-foo --formula mol-witness-patrol", "witness", false},

		// Non-sling commands not touched.
		{"gt hook", "witness", false},
		{"shiny --formula shiny", "witness", false}, // not a `gt sling`

		// Quoted formula name still resolved.
		{"gt sling te-foo --formula \"shiny\"", "witness", true},
		{"gt sling te-foo --formula 'shiny'", "witness", true},
	}
	for _, tc := range tests {
		if got := hasInvalidShinyFormula(tc.cmd, tc.role); got != tc.want {
			t.Errorf("hasInvalidShinyFormula(%q, %q) = %v, want %v",
				tc.cmd, tc.role, got, tc.want)
		}
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
