package orchestrator

import (
	"os"
	"strings"
	"testing"
)

func TestExtractPathFromBeadTitle(t *testing.T) {
	prefix := "Implement finally/"
	got := ExtractPathFromBeadTitle("Implement finally/myapp/frontend/game/main.js per architecture", prefix)
	want := "finally/myapp/frontend/game/main.js"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestNormalizeBeadPathForLayout(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path, layout, want string
	}{
		{"internal/store/store.go", "linkshelf", "linkshelf/internal/store/store.go"},
		{"linkshelf/internal/store/store.go", "linkshelf", "linkshelf/internal/store/store.go"},
		{"go.mod", "linkshelf", "linkshelf/go.mod"},
		{"myapp/frontend/main.js", "finally", "myapp/frontend/main.js"},
		{"", "linkshelf", ""},
		{"internal/store/store.go", "", "internal/store/store.go"},
	}
	for _, tc := range tests {
		got := NormalizeBeadPathForLayout(tc.path, tc.layout)
		if got != tc.want {
			t.Fatalf("NormalizeBeadPathForLayout(%q, %q) = %q, want %q", tc.path, tc.layout, got, tc.want)
		}
	}
}

func TestNormalizeBeadPathForLayout_testgt3BeadTitle(t *testing.T) {
	t.Parallel()
	title := "Implement linkshelf/internal/store/store.go per architecture"
	raw := ExtractPathFromBeadTitle(title, "Implement linkshelf/")
	got := NormalizeBeadPathForLayout(raw, "linkshelf")
	want := "linkshelf/internal/store/store.go"
	if raw != "linkshelf/internal/store/store.go" {
		t.Fatalf("ExtractPathFromBeadTitle = %q, want linkshelf/internal/store/store.go", raw)
	}
	if got != want {
		t.Fatalf("normalized = %q, want %q", got, want)
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
	err := ValidatePlanBeads(beads, "", v, "finally")
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
	if err := ValidatePlanBeads(beads, archPath, v, ""); err != nil {
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
	if err := ValidatePlanBeads(beads, "", v, "finally"); err != nil {
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
