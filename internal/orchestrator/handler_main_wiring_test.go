package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendGoBuildCmdServerToVerify_handlersBead(t *testing.T) {
	dir := t.TempDir()
	layout := "linkshelf"
	apiDir := filepath.Join(dir, layout, "internal", "api")
	cmdDir := filepath.Join(dir, layout, "cmd", "server")
	for _, d := range []string{apiDir, cmdDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, layout, "go.mod"), []byte("module linkshelf\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(apiDir, "handlers.go"), []byte("package api\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:    layout,
		TestRunner:    "go",
		RequiredFiles: []string{layout + "/internal/api/handlers.go", layout + "/cmd/server/main.go"},
	}
	bead := layout + "/internal/api/handlers.go"
	base := GoTestVerifyCommandForPackage(v, dir, bead)
	got := AppendGoBuildCmdServerToVerify(base, dir, bead, v)
	if !strings.Contains(got, "go test") || !strings.Contains(got, "go build ./cmd/server/...") {
		t.Fatalf("got %q", got)
	}
}

func TestAppendGoBuildCmdServerToVerify_skipsStoreBead(t *testing.T) {
	dir := t.TempDir()
	v := WorkflowValidation{LayoutRoot: "linkshelf", TestRunner: "go"}
	bead := "linkshelf/internal/store/store.go"
	base := "go test ./internal/store/..."
	got := AppendGoBuildCmdServerToVerify(base, dir, bead, v)
	if got != base {
		t.Fatalf("got %q want %q", got, base)
	}
}

func TestFormatHandlerExportsForMainBlock_noExports(t *testing.T) {
	dir := t.TempDir()
	layout := "linkshelf"
	apiDir := filepath.Join(dir, layout, "internal", "api")
	cmdDir := filepath.Join(dir, layout, "cmd", "server")
	for _, d := range []string{apiDir, cmdDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(apiDir, "handlers.go"), []byte("package api\nfunc serveIndex() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cmdDir, "main.go"), []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{LayoutRoot: layout, TestRunner: "go"}
	block := FormatHandlerExportsForMainBlock(dir, layout+"/internal/api/handlers.go", v)
	if !strings.Contains(block, "None on disk") || !strings.Contains(block, "RegisterHandlers") {
		t.Fatalf("missing export guidance:\n%s", block)
	}
	if !strings.Contains(block, "api.serveIndex") {
		t.Fatalf("missing unexported warning:\n%s", block)
	}
}

func TestFormatMainDependencyExportsBlock_noHandlerExports(t *testing.T) {
	dir := t.TempDir()
	layout := "linkshelf"
	handlers := filepath.Join(dir, layout, "internal", "api", "handlers.go")
	if err := os.MkdirAll(filepath.Dir(handlers), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(handlers, []byte("package api\nfunc serveIndex() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:    layout,
		TestRunner:    "go",
		RequiredFiles: []string{layout + "/internal/api/handlers.go", layout + "/cmd/server/main.go"},
	}
	block := FormatMainDependencyExportsBlock(dir, layout+"/cmd/server/main.go", v)
	if !strings.Contains(block, "export nothing yet") || !strings.Contains(block, "go build ./cmd/server/...") {
		t.Fatalf("missing main-bead warning:\n%s", block)
	}
}
