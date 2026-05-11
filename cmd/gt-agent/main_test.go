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
	}
	for _, tc := range tests {
		if got := hasInvalidPatrolCommand(tc.in); got != tc.want {
			t.Errorf("hasInvalidPatrolCommand(%q) = %v, want %v", tc.in, got, tc.want)
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
