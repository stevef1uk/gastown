package main

import (
	"path/filepath"
	"testing"
)

func TestStatePath_townAgents(t *testing.T) {
	// planner is rig-level — empty rig falls through to default path
	got := statePath("/gt", "planner", "", "")
	want := filepath.Join("/gt", stateFileName)
	if got != want {
		t.Fatalf("planner: got %q want %q", got, want)
	}
	// planner with rig goes to rig-level path
	got = statePath("/gt", "planner", "myrig", "")
	want = filepath.Join("/gt", "myrig", "planner", stateFileName)
	if got != want {
		t.Fatalf("planner with rig: got %q want %q", got, want)
	}
}

func TestStatePath_rigSingletons(t *testing.T) {
	for _, role := range []string{"architect", "qa", "witness", "refinery"} {
		got := statePath("/gt", role, "mockrig", "")
		want := filepath.Join("/gt", "mockrig", role, stateFileName)
		if got != want {
			t.Fatalf("%s: got %q want %q", role, got, want)
		}
	}
}

func TestStatePath_namedPolecat(t *testing.T) {
	got := statePath("/gt", "polecat", "mockrig", "toast")
	want := filepath.Join("/gt", "mockrig", "polecats", "toast", stateFileName)
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
