package main

import (
	"path/filepath"
	"testing"
)

func TestOrchestratedRoleLogPath(t *testing.T) {
	t.Parallel()
	got := orchestratedRoleLogPath("/gt", "architect", "mockrigb", "")
	want := filepath.Join("/gt", "mockrigb", "architect", "typescript")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	// planner is rig-level — empty rig returns "" (no town-level fallback)
	got = orchestratedRoleLogPath("/gt", "planner", "", "")
	if got != "" {
		t.Fatalf("planner with empty rig: got %q want empty", got)
	}
	got = orchestratedRoleLogPath("/gt", "planner", "myrig", "")
	want = filepath.Join("/gt", "myrig", "planner", "typescript")
	if got != want {
		t.Fatalf("planner with rig: got %q want %q", got, want)
	}
}
