package main

import "testing"

func TestFormatSuccessCommandOutput(t *testing.T) {
	t.Parallel()
	if got := formatSuccessCommandOutput([]byte("go: added foo v1\n")); got != "go: added foo v1\n" {
		t.Fatalf("got %q", got)
	}
	if got := formatSuccessCommandOutput(nil); got != "(exit 0, no output)\n" {
		t.Fatalf("got %q", got)
	}
	if got := formatSuccessCommandOutput([]byte("  \n")); got != "(exit 0, no output)\n" {
		t.Fatalf("got %q", got)
	}
}
