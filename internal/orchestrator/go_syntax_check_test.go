package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoSourceBytesValid_rejectsBrokenMerge(t *testing.T) {
	src := []byte("package store\n\nfunc f() {}\n}n err\n")
	if err := GoSourceBytesValid(src); err == nil {
		t.Fatal("expected syntax error")
	}
}

func TestGoSourceBytesValid_acceptsMinimalPackage(t *testing.T) {
	src := []byte("package store\n\nfunc List() error { return nil }\n")
	if err := GoSourceBytesValid(src); err != nil {
		t.Fatal(err)
	}
}

func TestImplementGoBytesCorrupted_markerOnly(t *testing.T) {
	if !ImplementGoBytesCorrupted([]byte(">>>>>>> REPLACE")) {
		t.Fatal("marker-only file should be corrupted")
	}
	if ImplementGoBytesCorrupted([]byte("package api\n\nfunc f() {}\n")) {
		t.Fatal("valid minimal Go should not be corrupted")
	}
}

func TestGoFileAtMayorRigParses_corruptReturnsFalse(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	rel := "linkshelf/internal/store/store.go"
	abs := filepath.Join(dir, rig, "mayor", "rig", rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("package store\n}n err\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if GoFileAtMayorRigParses(dir, rig, rel) {
		t.Fatal("corrupt file should not parse")
	}
}

func TestFormatCorruptedGoFileRecoveryHint(t *testing.T) {
	out := "./store.go:121:2: syntax error: unexpected name n\n"
	hint := FormatCorruptedGoFileRecoveryHint(out, []string{"linkshelf/internal/store/store.go"})
	if hint == "" || !strings.Contains(hint, "WRITE:") {
		t.Fatalf("hint=%q", hint)
	}
}
