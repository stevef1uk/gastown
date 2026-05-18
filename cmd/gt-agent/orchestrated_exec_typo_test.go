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
