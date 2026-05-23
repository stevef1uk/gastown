package main

import "testing"

func TestNormalizeGoCommandTypos(t *testing.T) {
	t.Parallel()
	cmd := "cd linkshelf && go mod tidy && go build./..."
	fixed, ok := normalizeGoCommandTypos(cmd)
	if !ok || fixed != "cd linkshelf && go mod tidy && go build ./..." {
		t.Fatalf("got ok=%v cmd=%q", ok, fixed)
	}
}

func TestNormalizeGoCommandTypos_goTestCountFlag(t *testing.T) {
	t.Parallel()
	cmd := "go test -count=1./internal/api/..."
	fixed, ok := normalizeGoCommandTypos(cmd)
	if !ok || fixed != "go test -count=1 ./internal/api/..." {
		t.Fatalf("got ok=%v cmd=%q", ok, fixed)
	}
}
