package orchestrator

import "testing"

func TestAllowedEarlierImplementDependencyWrite(t *testing.T) {
	t.Parallel()
	required := []string{
		"tasklist/go.mod",
		"tasklist/internal/store/store.go",
		"tasklist/internal/tasks/tasks.go",
		"tasklist/cmd/tasklist/main.go",
	}
	active := "tasklist/cmd/tasklist/main.go"

	if !AllowedEarlierImplementDependencyWrite(active, "tasklist/internal/store/store.go", required) {
		t.Fatal("store should be writable while cmd/main bead is active")
	}
	if !AllowedEarlierImplementDependencyWrite(active, "tasklist/internal/tasks/tasks.go", required) {
		t.Fatal("tasks should be writable while cmd/main bead is active")
	}
	if AllowedEarlierImplementDependencyWrite(active, "tasklist/cmd/tasklist/main.go", required) {
		t.Fatal("same path is not an earlier dependency")
	}
	if AllowedEarlierImplementDependencyWrite("tasklist/internal/store/store.go", active, required) {
		t.Fatal("must not allow writing cmd/main while store bead is active")
	}
	if AllowedEarlierImplementDependencyWrite(active, "tasklist/other/hack.go", required) {
		t.Fatal("path not in required_files")
	}
}
