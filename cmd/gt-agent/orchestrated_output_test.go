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
	got = orchestratedRoleLogPath("/gt", "planner", "", "")
	want = filepath.Join("/gt", "planner", "typescript")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
