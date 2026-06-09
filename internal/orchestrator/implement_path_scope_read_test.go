package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateImplementReadPath_allowsClosedDependency(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	for _, rel := range []string{
		"linkshelf/internal/api/handlers.go",
		"linkshelf/cmd/server/main.go",
	} {
		abs := filepath.Join(dir, rig, "mayor", "rig", rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte("package x\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	v := DefaultWorkflowValidation()
	v.LayoutRoot = "linkshelf"
	v.RequiredFiles = []string{
		"linkshelf/internal/api/handlers.go",
		"linkshelf/cmd/server/main.go",
	}
	// Active main bead may READ closed handlers dependency.
	if err := ValidateImplementReadPath(dir, rig, "te-main", "linkshelf/internal/api/handlers.go", v, ""); err != nil {
		t.Fatalf("read closed dependency: %v", err)
	}
	if err := ValidateImplementReadPath(dir, rig, "te-main", "linkshelf/not-in-plan.go", v, ""); err == nil {
		t.Fatal("expected reject read outside required_files")
	}
}
