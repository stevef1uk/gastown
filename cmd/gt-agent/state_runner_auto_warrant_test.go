package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestIsCmdTimeoutError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "command timeout error",
			err:      errors.New("command exceeded 120s"),
			expected: true,
		},
		{
			name:     "script timeout error",
			err:      errors.New("script exceeded 60s"),
			expected: true,
		},
		{
			name:     "regular error",
			err:      errors.New("command failed: exit status 1"),
			expected: false,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "wrapped timeout error",
			err:      errors.New("some context: command exceeded 60s (command exceeded 120s)"),
			expected: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := isCmdTimeoutError(tc.err)
			if result != tc.expected {
				t.Fatalf("isCmdTimeoutError(%q) = %v, want %v", tc.err, result, tc.expected)
			}
		})
	}
}

func TestAutoWarrantCounter(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	townRoot := dir
	rig := "testrig"

	// Create state hooks with consecutive_cmd_timeout_threshold = 2
	hooks := orchestrator.StateHooks{
		ConsecutiveCmdTimeoutThreshold: 2,
		Track:                          "test",
	}

	// Create a simpleValidation := orchestrator.WorkflowValidation{}
	v := orchestrator.WorkflowValidation{}

	task := &orchestrator.Task{
		State:      "implementation",
		TemplateID: "rig-flow",
		Validation: v,
		Hooks:      hooks,
	}

	r := newStateRunner(task, townRoot, rig)

	// Initially counter should be 0
	if r.consecutiveCmdTimeouts != 0 {
		t.Fatal("initial counter should be 0")
	}

	// Simulate a timeout error
	timeoutErr := errors.New("command exceeded 120s")

	// First timeout - counter should be 1
	r.handleCmdError(timeoutErr)
	if r.consecutiveCmdTimeouts != 1 {
		t.Fatalf("after 1 timeout, counter should be 1, got %d", r.consecutiveCmdTimeouts)
	}

	// Second timeout - should trigger warrant filing
	// The warrant filing resets counter to 0
	r.handleCmdError(timeoutErr)
	if r.consecutiveCmdTimeouts != 0 {
		t.Fatalf("after 2 timeouts (threshold), counter should reset to 0 after warrant filing, got %d", r.consecutiveCmdTimeouts)
	}

	// Check that warrant file was created
	warrantDir := filepath.Join(dir, "warrants")
	files, err := os.ReadDir(warrantDir)
	if err != nil {
		t.Fatalf("warrant dir not created: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 warrant file, got %d", len(files))
	}

	// Verify warrant content
	warrantPath := filepath.Join(warrantDir, files[0].Name())
	data, err := os.ReadFile(warrantPath)
	if err != nil {
		t.Fatalf("read warrant: %v", err)
	}
	warrantContent := string(data)
	if !strings.Contains(warrantContent, "Auto-warrant") {
		t.Errorf("warrant should contain 'Auto-warrant', got: %s", warrantContent)
	}
	if !strings.Contains(warrantContent, "testrig") {
		t.Errorf("warrant should mention testrig, got: %s", warrantContent)
	}

	// Third error should reset counter after filing
	// (since the filing resets counter to 0 in our implementation)
	r.handleCmdError(timeoutErr)
	if r.consecutiveCmdTimeouts != 1 {
		t.Fatalf("after warrant filing + 1 timeout, counter should be 1, got %d", r.consecutiveCmdTimeouts)
	}
}

func TestNonTimeoutErrorResetsCounter(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	rig := "testrig"

	hooks := orchestrator.StateHooks{
		ConsecutiveCmdTimeoutThreshold: 3,
		Track:                          "test",
	}
	v := orchestrator.WorkflowValidation{}

	task := &orchestrator.Task{
		State:      "implementation",
		TemplateID: "rig-flow",
		Validation: v,
		Hooks:      hooks,
	}

	r := newStateRunner(task, dir, rig)

	// Simulate timeout
	timeoutErr := errors.New("command exceeded 120s")
	r.handleCmdError(timeoutErr)
	if r.consecutiveCmdTimeouts != 1 {
		t.Fatal("counter should be 1 after timeout")
	}

	// Non-timeout error should reset counter
	regularErr := errors.New("command failed: exit status 1")
	r.handleCmdError(regularErr)
	if r.consecutiveCmdTimeouts != 0 {
		t.Fatal("counter should reset to 0 after non-timeout error")
	}
}

func TestConsecutiveCmdTimeoutThresholdFromHooks(t *testing.T) {
	t.Parallel()

	hooks := orchestrator.StateHooks{
		ConsecutiveCmdTimeoutThreshold: 5,
	}

	// Test that the effective threshold returns the configured value
	threshold := hooks.EffectiveConsecutiveCmdTimeoutThreshold()
	if threshold != 5 {
		t.Fatalf("EffectiveConsecutiveCmdTimeoutThreshold() = %d, want 5", threshold)
	}

	// Test default threshold when not set
	hooks2 := orchestrator.StateHooks{}
	threshold2 := hooks2.EffectiveConsecutiveCmdTimeoutThreshold()
	if threshold2 != 3 {
		t.Fatalf("default threshold should be 3, got %d", threshold2)
	}
}