package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkflowUsesGo(t *testing.T) {
	t.Parallel()
	if !WorkflowUsesGo(WorkflowValidation{QAVerifyCommand: "cd linkshelf && go test ./..."}) {
		t.Fatal("expected Go from qa_verify_command")
	}
	if WorkflowUsesGo(WorkflowValidation{QAVerifyCommand: "python3 -m pytest -q"}) {
		t.Fatal("pytest profile should not be Go")
	}
	if !WorkflowUsesGo(WorkflowValidation{TestRunner: "go"}) {
		t.Fatal("test_runner go")
	}
	if !WorkflowUsesGo(WorkflowValidation{QAVerifyCommand: "cd linkshelf && go run ./cmd/server"}) {
		t.Fatal("expected Go from go run in qa_verify_command")
	}
}

func TestGoProjectSetupVerifyCommand(t *testing.T) {
	t.Parallel()
	got := GoProjectSetupVerifyCommand(WorkflowValidation{LayoutRoot: "linkshelf"}, "")
	if got != "cd linkshelf && go mod download" {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "go build") || strings.Contains(got, "curl") {
		t.Fatalf("setup verify must not build or curl: %q", got)
	}
}

func TestGoCompileOnlyVerifyCommand(t *testing.T) {
	t.Parallel()
	got := GoCompileOnlyVerifyCommand(WorkflowValidation{LayoutRoot: "linkshelf"}, "")
	if !strings.Contains(got, "go build") || strings.Contains(got, "curl") {
		t.Fatalf("got %q", got)
	}
}

func TestImplementationVerifyHint_neverCurl(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	main := filepath.Join(dir, "linkshelf", "cmd", "server", "main.go")
	if err := os.MkdirAll(filepath.Dir(main), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(main, []byte("package main\nfunc main() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go run ./cmd/server & curl -s http://localhost:8080/",
	}
	got := v.ImplementationVerifyHint(dir)
	if strings.Contains(got, "curl") || strings.Contains(got, "go run") {
		t.Fatalf("prompt hint must be compile-only: %q", got)
	}
}

func TestGoImplementationVerifyCommand_beforeMain(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	v := WorkflowValidation{LayoutRoot: "linkshelf"}
	got := GoImplementationVerifyCommand(v, dir)
	if !strings.Contains(got, "go build") || strings.Contains(got, "curl") {
		t.Fatalf("want compile-only before main: %q", got)
	}
}

func TestGoImplementationVerifyCommandForBead_frontendReturnsEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	v := WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go run ./cmd/server & curl -s http://localhost:8080/",
	}
	for _, path := range []string{
		"linkshelf/web/index.html",
		"linkshelf/web/style.css",
		"linkshelf/web/app.js",
	} {
		got := GoImplementationVerifyCommandForBead(v, dir, path)
		if got != "" {
			t.Errorf("frontend path %q should return empty verify cmd, got %q", path, got)
		}
	}
}

func TestGoImplementationVerifyCommandForBead_goMod(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	main := filepath.Join(dir, "linkshelf", "cmd", "server", "main.go")
	if err := os.MkdirAll(filepath.Dir(main), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(main, []byte("package main\nfunc main() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go run ./cmd/server & curl -s http://localhost:8080/",
	}
	got := GoImplementationVerifyCommandForBead(v, dir, "linkshelf/go.mod")
	if strings.Contains(got, "curl") || strings.Contains(got, "go run") || strings.Contains(got, "go build") {
		t.Fatalf("go.mod bead wants scaffold verify only: %q", got)
	}
	if !strings.Contains(got, "go mod download") {
		t.Fatalf("want go mod download: %q", got)
	}
}

func TestGoImplementationVerifyCommandForBead_storeCompileOnly(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	main := filepath.Join(dir, "linkshelf", "cmd", "server", "main.go")
	if err := os.MkdirAll(filepath.Dir(main), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(main, []byte("package main\nfunc main() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go run ./cmd/server & curl -s http://localhost:8080/",
	}
	got := GoImplementationVerifyCommandForBead(v, dir, "linkshelf/internal/store/store.go")
	if strings.Contains(got, "curl") || strings.Contains(got, "go run") {
		t.Fatalf("store bead must not curl: %q", got)
	}
	if !strings.Contains(got, "go build ./internal/store/...") && !strings.Contains(got, "go test -timeout 30s -count=1 ./internal/store/...") {
		t.Fatalf("store bead wants package-scoped verify: %q", got)
	}
}

func TestGoToolOutputMissingDeps(t *testing.T) {
	t.Parallel()
	cases := []struct {
		output string
		want   bool
	}{
		{"go: no required module provides github.com/foo/bar", true},
		{"missing go.sum entry for module github.com/foo/bar", true},
		{"go.sum is out of date", true},
		{"cannot find module providing package foo/bar", true},
		{"# github.com/foo/bar\n./foo.go:5:2: undefined: Something", false},
		{"matched no packages", false},
		{"ok   github.com/foo/bar  0.001s", false},
	}
	for _, c := range cases {
		got := GoToolOutputMissingDeps(c.output)
		if got != c.want {
			t.Fatalf("GoToolOutputMissingDeps(%q) = %v, want %v", c.output, got, c.want)
		}
	}
}

func TestGoToolOutputMatchedNoPackages(t *testing.T) {
	t.Parallel()
	cases := []struct {
		output string
		want   bool
	}{
		{"matched no packages", true},
		{"no packages to test", true},
		{"no modules specified", true},
		{"no module dependencies to download", true},
		{"ok   github.com/foo/bar  0.001s", false},
		{"# github.com/foo/bar\n./foo.go:5:2: undefined: Something", false},
	}
	for _, c := range cases {
		got := GoToolOutputMatchedNoPackages(c.output)
		if got != c.want {
			t.Fatalf("GoToolOutputMatchedNoPackages(%q) = %v, want %v", c.output, got, c.want)
		}
	}
}

func TestGoVerifyCommandWithTidy(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go test ./...",
	}
	got := GoVerifyCommandWithTidy(v, "")
	if !strings.Contains(got, "go mod tidy") || !strings.Contains(got, "go test") {
		t.Fatalf("got %q", got)
	}
}
