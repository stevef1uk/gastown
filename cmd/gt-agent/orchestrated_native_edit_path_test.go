package main

import (
	"strings"
	"testing"
)

func TestResolveNativeEditAbsPath_rejectsProsePath(t *testing.T) {
	_, _, err := resolveNativeEditAbsPath(t.TempDir(), "` command to create the file.", "linkshelf")
	if err == nil || !strings.Contains(err.Error(), "invalid path") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseOrchestratedNativeEdits_proseWriteRejectedAtResolve(t *testing.T) {
	in := "WRITE: ` command to create the file.\npackage x\n---END WRITE---\n"
	ops := parseOrchestratedNativeEdits(in)
	if len(ops) != 1 {
		t.Fatalf("ops = %+v", ops)
	}
	_, _, err := resolveNativeEditAbsPath(t.TempDir(), ops[0].path, "linkshelf")
	if err == nil || !strings.Contains(err.Error(), "invalid path") {
		t.Fatalf("err = %v", err)
	}
}
