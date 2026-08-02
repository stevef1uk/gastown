package specprofile

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeterministicIndexRig_basicLayoutTree(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "testrig", "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	spec := "# Hello World API\n\n## Layout\n\n```\nhelloapi/\n├── go.mod\n├── main.go\n├── handler/\n│   └── hello.go\n└── handler/\n    └── hello_test.go\n```\n\n## Phases\n\n1. **setup** — Initialize go.mod\n2. **handler** — Implement handler\n3. **integration** — Tests\n"
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}

	f, err := DeterministicIndexRig(context.Background(), dir, "testrig")
	if err != nil {
		t.Fatalf("DeterministicIndexRig failed: %v", err)
	}
	if f.Source != "deterministic" {
		t.Fatalf("expected source 'deterministic', got %q", f.Source)
	}
	wantFiles := map[string]bool{
		"helloapi/go.mod":              true,
		"helloapi/main.go":             true,
		"helloapi/handler/hello.go":    true,
		"helloapi/handler/hello_test.go": true,
	}
	if len(f.Validation.RequiredFiles) != 4 {
		t.Fatalf("expected 4 files, got %d: %v", len(f.Validation.RequiredFiles), f.Validation.RequiredFiles)
	}
	for _, p := range f.Validation.RequiredFiles {
		if !wantFiles[p] {
			t.Fatalf("unexpected file %q in %v", p, f.Validation.RequiredFiles)
		}
	}
	if len(f.Validation.DeliveryPhases) == 0 {
		t.Fatal("expected delivery phases")
	}
}

func TestDeterministicIndexRig_fallbackWhenNoTree(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "testrig", "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	// SPEC with no layout tree
	spec := "# Hello World API\n\n## Overview\nA simple API.\n\n## Phases\n\n1. **setup** — Initialize\n"
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := DeterministicIndexRig(context.Background(), dir, "testrig")
	if err == nil {
		t.Fatal("expected error when no layout tree")
	}
	if !strings.Contains(err.Error(), "no parseable layout tree") {
		t.Fatalf("expected 'no parseable layout tree' error, got: %v", err)
	}
}

func TestDeterministicIndexRig_proseBacktickRefsIgnored(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "testrig", "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	spec := "# Hello World API\n\n## Layout\n\n```\nhelloapi/\n├── go.mod\n├── main.go\n└── handler/\n    └── hello.go\n```\n\n## File Layout\n- `go.mod` – module\n- `main.go` – entry\n- `handler/hello.go` – handler\n- `package.json` – NOT used\n- `tsconfig.json` – NOT used\n"
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}

	f, err := DeterministicIndexRig(context.Background(), dir, "testrig")
	if err != nil {
		t.Fatalf("DeterministicIndexRig failed: %v", err)
	}
	// Only tree files should be present; prose backtick refs (including negative mentions) ignored
	wantFiles := map[string]bool{
		"helloapi/go.mod":         true,
		"helloapi/main.go":        true,
		"helloapi/handler/hello.go": true,
	}
	if len(f.Validation.RequiredFiles) != 3 {
		t.Fatalf("expected 3 files (tree only), got %d: %v", len(f.Validation.RequiredFiles), f.Validation.RequiredFiles)
	}
	for _, p := range f.Validation.RequiredFiles {
		if !wantFiles[p] {
			t.Fatalf("unexpected file %q (prose ref leaked?) in %v", p, f.Validation.RequiredFiles)
		}
	}
}

func TestDeterministicIndexRig_verifyCommandInference(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "testrig", "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	spec := "# Hello World API\n\n## Layout\n\n```\nhelloapi/\n├── go.mod\n├── main.go\n└── handler.go\n```\n\n## Testing\nRun `cd helloapi && go test ./...` to verify.\n"
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}

	f, err := DeterministicIndexRig(context.Background(), dir, "testrig")
	if err != nil {
		t.Fatalf("DeterministicIndexRig failed: %v", err)
	}
	if !strings.Contains(f.Validation.QAVerifyCommand, "go test") {
		t.Fatalf("expected go test verify command, got: %q", f.Validation.QAVerifyCommand)
	}
}
