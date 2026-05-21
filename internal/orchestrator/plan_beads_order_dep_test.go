package orchestrator

import "testing"

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

func TestAllowedEarlierImplementDependencyWrite(t *testing.T) {
	t.Parallel()
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
