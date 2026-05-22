package orchestrator

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requireGoToolchain(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not in PATH")
	}
}

func TestImplementationModuleCompileOK_passesCleanModule(t *testing.T) {
	requireGoToolchain(t)
	dir := t.TempDir()
	layout := filepath.Join(dir, "linkshelf")
	if err := os.MkdirAll(filepath.Join(layout, "internal", "pkg"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "go.mod"), []byte("module linkshelf\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "internal", "pkg", "pkg.go"), []byte("package pkg\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := DefaultWorkflowValidation()
	v.LayoutRoot = "linkshelf"
	v.QAVerifyCommand = "cd linkshelf && go test ./..."
	if err := ImplementationModuleCompileOK(dir, v); err != nil {
		if strings.Contains(err.Error(), "command not found") || strings.Contains(err.Error(), "exit status 127") {
			t.Skip("go not available in test shell")
		}
		t.Fatal(err)
	}
}

func TestImplementationModuleCompileOK_failsBrokenModule(t *testing.T) {
	requireGoToolchain(t)
	dir := t.TempDir()
	layout := filepath.Join(dir, "linkshelf")
	if err := os.MkdirAll(filepath.Join(layout, "internal", "pkg"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "go.mod"), []byte("module linkshelf\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "internal", "pkg", "pkg.go"), []byte("package pkg\n\nfoo bar\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := DefaultWorkflowValidation()
	v.LayoutRoot = "linkshelf"
	v.QAVerifyCommand = "cd linkshelf && go test ./..."
	err := ImplementationModuleCompileOK(dir, v)
	if err == nil {
		t.Fatal("expected compile failure")
	}
	if strings.Contains(err.Error(), "command not found") || strings.Contains(err.Error(), "exit status 127") {
		t.Skip("go not available in test shell")
	}
}
