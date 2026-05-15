package main

import (
	"path/filepath"
	"testing"
)

func TestStatePath_townAgents(t *testing.T) {
	got := statePath("/gt", "planner", "", "")
	want := filepath.Join("/gt", "planner", stateFileName)
	if got != want {
		t.Fatalf("planner: got %q want %q", got, want)
	}
}

func TestStatePath_rigSingletons(t *testing.T) {
	for _, role := range []string{"architect", "qa", "witness", "refinery"} {
		got := statePath("/gt", role, "testgt2", "")
		want := filepath.Join("/gt", "testgt2", role, stateFileName)
		if got != want {
			t.Fatalf("%s: got %q want %q", role, got, want)
		}
	}
}

func TestStatePath_namedPolecat(t *testing.T) {
	got := statePath("/gt", "polecat", "testgt2", "toast")
	want := filepath.Join("/gt", "testgt2", "polecats", "toast", stateFileName)
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
