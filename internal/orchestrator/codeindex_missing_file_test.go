package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatCodeindexContextForBead_warnsStaleIndexWhenFileMissing(t *testing.T) {
	dir := t.TempDir()
	index := filepath.Join(dir, codeindexIndexName)
	payload := `{
		"nodes": [{
			"id": "internal/api",
			"symbols": [
				{"name": "RegisterHandlers", "kind": "function", "exported": true, "file": "handlers.go", "line": 1}
			]
		}]
	}`
	if err := os.WriteFile(index, []byte(payload), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{LayoutRoot: "linkshelf", QAVerifyCommand: "cd linkshelf && go test ./..."}
	got := FormatCodeindexContextForBead(dir, "linkshelf/internal/api/handlers.go", v)
	if got == "" || !strings.Contains(got, "not on disk") {
		t.Fatalf("got %q", got)
	}
	if strings.Contains(got, "RegisterHandlers") {
		t.Fatal("must not inject stale symbols when file is missing")
	}
}

func TestFormatCodeindexContextForBead_noStaleWarningWhenIndexEmpty(t *testing.T) {
	if !CodeindexEnabled() {
		t.Setenv("GT_CODEINDEX", "1")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, codeindexIndexName), []byte(`{"nodes":[]}`), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{LayoutRoot: "linkshelf", QAVerifyCommand: "cd linkshelf && go test ./..."}
	got := FormatCodeindexContextForBead(dir, "linkshelf/internal/api/handlers.go", v)
	if strings.Contains(got, "not on disk") {
		t.Fatalf("should not warn when index has no stale symbols: %q", got)
	}
}
