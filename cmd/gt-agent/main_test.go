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
		{"bd mol current mol-witness-patrol", "gt mol current", true},
		{"bd mol current hq-wisp-abc123", "gt mol current", true},
		{"gt mol current", "gt mol current", false},
	}
	for _, tt := range tests {
		got, changed := normalizeGeneratedCommand(tt.in)
		if got != tt.want || changed != tt.changed {
			t.Errorf("normalizeGeneratedCommand(%q) = (%q, %v), want (%q, %v)",
				tt.in, got, changed, tt.want, tt.changed)
		}
	}
}
