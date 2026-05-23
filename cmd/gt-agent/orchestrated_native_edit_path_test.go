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
	if len(ops) != 0 {
		t.Fatalf("prose WRITE must not parse, ops=%+v", ops)
	}
}
