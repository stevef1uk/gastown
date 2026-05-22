package orchestrator

import (
	"strings"
	"testing"
)

func TestEarlierRequiredFilesForBead(t *testing.T) {
	required := []string{
		"linkshelf/go.mod",
		"linkshelf/internal/store/store.go",
		"linkshelf/internal/api/handlers.go",
		"linkshelf/cmd/server/main.go",
	}
	got := EarlierRequiredFilesForBead("linkshelf/cmd/server/main.go", required)
	want := []string{
		"linkshelf/go.mod",
		"linkshelf/internal/store/store.go",
		"linkshelf/internal/api/handlers.go",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestFormatClosedDependencyCompileHints(t *testing.T) {
	v := WorkflowValidation{
		BeadTitleContains: "Implement linkshelf/",
		LayoutRoot:        "linkshelf",
		RequiredFiles: []string{
			"linkshelf/internal/api/handlers.go",
			"linkshelf/cmd/server/main.go",
		},
	}
	setListImplementBeadsByStatusHook(t, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		switch status {
		case "in_progress":
			return []PlanBead{{ID: "te-main", Title: "Implement linkshelf/cmd/server/main.go per architecture"}}, nil
		case "closed":
			return []PlanBead{{ID: "te-h", Title: "Implement linkshelf/internal/api/handlers.go per architecture"}}, nil
		default:
			return nil, nil
		}
	})

	got := FormatClosedDependencyCompileHints("", "", "linkshelf/cmd/server/main.go",
		[]string{"linkshelf/internal/api/handlers.go"}, v)
	if got == "" || !strings.Contains(got, "te-h") || !strings.Contains(got, "closed") {
		t.Fatalf("want closed-bead hint, got %q", got)
	}
	if !strings.Contains(got, "bd update te-h --status=open") {
		t.Fatalf("want explicit bd update step, got %q", got)
	}
	if !strings.Contains(got, "**First:**") || strings.Contains(got, "finish with JSON only") {
		t.Fatalf("want CMD-first reopen guidance, not failure-json default, got %q", got)
	}
}

func TestListImplementBeadsOpenOrInProgress_usesStatusHook(t *testing.T) {
	setListImplementBeadsByStatusHook(t, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "in_progress" {
			return []PlanBead{{ID: "te-x", Title: "Implement linkshelf/foo.go per architecture"}}, nil
		}
		return nil, nil
	})
	got, err := ListImplementBeadsOpenOrInProgress("", "", WorkflowValidation{BeadTitleContains: "Implement linkshelf/"})
	if err != nil || len(got) != 1 || got[0].ID != "te-x" {
		t.Fatalf("got %v err %v", got, err)
	}
}

func TestAllowedEarlierImplementDependencyWrite(t *testing.T) {
	t.Parallel()
	setListImplementBeadsByStatusHook(t, nil)
	v := WorkflowValidation{
		RequiredFiles: []string{
			"tasklist/go.mod",
			"tasklist/internal/store/store.go",
			"tasklist/internal/tasks/tasks.go",
			"tasklist/cmd/tasklist/main.go",
		},
	}
	active := "tasklist/cmd/tasklist/main.go"

	if !AllowedEarlierImplementDependencyWrite("", "", active, "tasklist/internal/store/store.go", v) {
		t.Fatal("store should be writable while cmd/main bead is active")
	}
	if !AllowedEarlierImplementDependencyWrite("", "", active, "tasklist/internal/tasks/tasks.go", v) {
		t.Fatal("tasks should be writable while cmd/main bead is active")
	}
	if AllowedEarlierImplementDependencyWrite("", "", active, "tasklist/cmd/tasklist/main.go", v) {
		t.Fatal("same path is not an earlier dependency")
	}
	if AllowedEarlierImplementDependencyWrite("", "", "tasklist/internal/store/store.go", active, v) {
		t.Fatal("must not allow writing cmd/main while store bead is active")
	}
	if AllowedEarlierImplementDependencyWrite("", "", active, "tasklist/other/hack.go", v) {
		t.Fatal("path not in required_files")
	}
}
