package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestAppendImplementationCodeindexReminder(t *testing.T) {
	if !orchestrator.CodeindexEnabled() {
		t.Skip("codeindex not on PATH")
	}
	dir := t.TempDir()
	rig := "testrig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	layout := filepath.Join(rigDir, "linkshelf", "internal", "store")
	if err := os.MkdirAll(layout, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "schema.go"), []byte("package store\nfunc InitSchema() error { return nil }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := orchestrator.WorkflowValidation{
		LayoutRoot:        "linkshelf",
		QAVerifyCommand:   "cd linkshelf && go test ./...",
		BeadTitleContains: "Implement ",
		RequiredFiles:     []string{"linkshelf/internal/store/schema.go", "linkshelf/internal/api/handlers.go"},
	}
	if _, err := orchestrator.RefreshCodeindexIndex(rigDir, v); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	task := &orchestrator.Task{Hooks: orchestrator.StateHooks{Track: "implementation", Artifacts: "implementation"}}
	runner := newStateRunner(task, dir, rig)
	runner.v = v
	runner.track.activeBeadPath = "linkshelf/internal/api/handlers.go"
	var b strings.Builder
	b.WriteString("EDIT ok")
	runner.appendImplementationCodeindexReminder(&b)
	got := b.String()
	if !strings.Contains(got, "Codeindex symbols") || !strings.Contains(got, "InitSchema") {
		t.Fatalf("got %q", got)
	}
}
