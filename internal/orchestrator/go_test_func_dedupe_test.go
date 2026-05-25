package orchestrator

import (
	"strings"
	"testing"
)

func TestDedupeGoTestFuncs_keepsFirst(t *testing.T) {
	t.Parallel()
	src := []byte(`package pkg

import "testing"

func TestFoo(t *testing.T) {}

func TestFoo(t *testing.T) { t.Fatal("dup") }

func TestBar(t *testing.T) {}
`)
	out, removed, err := DedupeGoTestFuncs(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != "TestFoo" {
		t.Fatalf("removed = %v", removed)
	}
	if len(DuplicateGoTestFuncNames(out)) != 0 {
		t.Fatalf("still dupes: %v", DuplicateGoTestFuncNames(out))
	}
	if !strings.Contains(string(out), "TestBar") {
		t.Fatal("missing TestBar")
	}
	if strings.Count(string(out), "func TestFoo") != 1 {
		t.Fatalf("want one TestFoo, got:\n%s", out)
	}
	if err := GoSourceBytesValid(out); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeGoTestFileContent_noOpOnProd(t *testing.T) {
	t.Parallel()
	src := []byte("package pkg\nfunc X() {}\n")
	out, removed, err := NormalizeGoTestFileContent("internal/pkg/widget.go", src)
	if err != nil || len(removed) != 0 || string(out) != string(src) {
		t.Fatalf("out=%q removed=%v err=%v", out, removed, err)
	}
}
