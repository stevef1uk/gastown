package deps

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestParseClaudeCodeVersion(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"2.1.101 (Claude Code)", "2.1.101"},
		{"2.1.101 (Claude Code)\n", "2.1.101"},
		{"2.1.101", "2.1.101"},
		{"2.0.20", "2.0.20"},
		{"1.0.128", "1.0.128"},
		{"10.20.30 (Claude Code)", "10.20.30"},
		{"some other output", ""},
		{"", ""},
	}

	for _, tt := range tests {
		result := parseClaudeCodeVersion(tt.input)
		if result != tt.expected {
			t.Errorf("parseClaudeCodeVersion(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestCheckClaudeCode(t *testing.T) {
	ResetClaudeCodeCheckCache()
	status, version := CheckClaudeCode()

	if status == ClaudeCodeNotFound {
		t.Skip("claude not installed, skipping integration test")
	}

	if status == ClaudeCodeOK && version == "" {
		t.Error("CheckClaudeCode returned ClaudeCodeOK but empty version")
	}

	t.Logf("CheckClaudeCode: status=%d, version=%s", status, version)
}

func TestCheckClaudeCode_cachesPerProcess(t *testing.T) {
	ResetClaudeCodeCheckCache()
	dir := t.TempDir()
	counter := filepath.Join(dir, "count")
	script := filepath.Join(dir, "claude")
	if err := os.WriteFile(script, []byte(fmt.Sprintf(`#!/bin/sh
n=0
[ -f %q ] && n=$(cat %q)
n=$((n+1))
echo "$n" > %q
echo '%s (Claude Code)'
`, counter, counter, counter, RecommendedClaudeCodeVersion)), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	CheckClaudeCode()
	CheckClaudeCode()
	data, err := os.ReadFile(counter)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "1\n" && string(data) != "1" {
		t.Fatalf("claude --version ran %q times, want 1", string(data))
	}
}
