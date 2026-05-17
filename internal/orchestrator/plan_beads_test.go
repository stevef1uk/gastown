package orchestrator

import (
	"os"
	"strings"
	"testing"
)

func TestExtractPathFromBeadTitle(t *testing.T) {
	prefix := "Implement finally/"
	got := ExtractPathFromBeadTitle("Implement finally/myapp/frontend/game/main.js per architecture", prefix)
	want := "myapp/frontend/game/main.js"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestValidatePlanBeads_duplicates(t *testing.T) {
	v := WorkflowValidation{
		BeadTitleContains: "Implement finally/",
		RequiredFiles: []string{
			"myapp/frontend/game/main.js",
			"finally/backend/main.py",
		},
	}
	beads := []PlanBead{
		{ID: "xx-a", Title: "Implement finally/myapp/frontend/game/main.js per architecture"},
		{ID: "xx-b", Title: "Implement finally/myapp/frontend/game/main.js per architecture"},
		{ID: "xx-c", Title: "Implement finally/finally/backend/main.py per architecture"},
	}
	err := ValidatePlanBeads(beads, "", v)
	if err == nil || !containsAll(err.Error(), "duplicate", "main.js") {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestValidatePlanBeads_archBacktickBasenameMatch(t *testing.T) {
	v := WorkflowValidation{
		BeadTitleContains: "Implement ",
		RequiredFiles: []string{
			"backend/widget.py",
			"backend/main.py",
			"backend/test_widget.py",
		},
	}
	beads := []PlanBead{
		{ID: "xx-xli", Title: "Implement backend/widget.py per architecture"},
		{ID: "xx-7oy", Title: "Implement backend/main.py per architecture"},
		{ID: "xx-569", Title: "Implement backend/test_widget.py per architecture"},
	}
	arch := "# Arch\nRun `python3 backend/main.py` and `backend/main.py`.\n"
	dir := t.TempDir()
	archPath := dir + "/architecture.md"
	if err := os.WriteFile(archPath, []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePlanBeads(beads, archPath, v); err != nil {
		t.Fatalf("expected ok with basename + command backtick noise: %v", err)
	}
}

func TestValidatePlanBeads_ok(t *testing.T) {
	v := WorkflowValidation{
		BeadTitleContains: "Implement finally/",
		RequiredFiles: []string{
			"myapp/frontend/game/main.js",
			"finally/backend/main.py",
		},
	}
	beads := []PlanBead{
		{ID: "xx-a", Title: "Implement finally/myapp/frontend/game/main.js per architecture"},
		{ID: "xx-c", Title: "Implement finally/finally/backend/main.py per architecture"},
	}
	v.RequiredFiles[1] = "finally/backend/main.py"
	if err := ValidatePlanBeads(beads, "", v); err != nil {
		t.Fatal(err)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !stringsContainsFold(s, p) {
			return false
		}
	}
	return true
}

func stringsContainsFold(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
