package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractGoSourcePathsFromOutput(t *testing.T) {
	t.Parallel()
	out := `go: linkshelf/cmd/server imports
	github.com/modernc.org/sqlite: cannot find module
internal/api/handlers.go:8:2: /home/u/store.go:9:2: invalid import path: modernc.org/sqlite driver`
	got := extractGoSourcePathsFromOutput(out, "linkshelf")
	found := false
	for _, p := range got {
		if strings.HasSuffix(p, "handlers.go") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected handlers.go in %v", got)
	}
}

func TestGoToolOutputLooksFailed(t *testing.T) {
	t.Parallel()
	if !goToolOutputLooksFailed("go mod tidy", "cannot find module providing package github.com/foo") {
		t.Fatal("expected failure detection")
	}
	if goToolOutputLooksFailed("go mod tidy", "go: downloading example.com v1.0.0\n") {
		t.Fatal("expected success output not failed")
	}
}

func TestExtractGoSourcePathsFromOutput_cmdServerImports(t *testing.T) {
	t.Parallel()
	out := `go: linkshelf/cmd/server imports
	github.com/modernc.org/sqlite: cannot find module`
	got := extractGoSourcePathsFromOutput(out, "linkshelf")
	foundMain := false
	for _, p := range got {
		if strings.HasSuffix(p, "cmd/server/main.go") {
			foundMain = true
		}
	}
	if !foundMain {
		t.Fatalf("expected cmd/server/main.go from imports line, got %v", got)
	}
	foundStore := false
	for _, p := range got {
		if strings.HasSuffix(p, "internal/store/store.go") {
			foundStore = true
		}
	}
	if !foundStore {
		t.Fatalf("expected store.go when module resolution fails, got %v", got)
	}
}

func TestAppendGoCompileSourceContext_writesSnippet(t *testing.T) {
	dir := t.TempDir()
	storeDir := filepath.Join(dir, "linkshelf", "internal", "store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "store.go"), []byte("package store\nimport \"broken\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	appendGoCompileSourceContext(&b, dir, "linkshelf", "go build ./...", "linkshelf/internal/store/store.go:3:8: syntax error")
	if !strings.Contains(b.String(), "package store") {
		t.Fatalf("want snippet in feedback: %q", b.String())
	}
}
