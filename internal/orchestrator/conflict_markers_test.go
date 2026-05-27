package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScrubConflictMarkers_removesReplaceMarkers(t *testing.T) {
	input := `>>>>>>> REPLACE
package api

import "net/http"

func Handler(w http.ResponseWriter, r *http.Request) {
}
`
	want := `package api

import "net/http"

func Handler(w http.ResponseWriter, r *http.Request) {
}
`
	got, removed := scrubConflictMarkers(input)
	if removed == 0 {
		t.Fatal("expected markers to be removed")
	}
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestScrubConflictMarkers_removesSearchReplaceBlock(t *testing.T) {
	input := `package api
<<<<<<< SEARCH
func oldHandler() {}
=======
func newHandler() {}
>>>>>>> REPLACE

func another() {}
`
	want := `package api
func oldHandler() {}
func newHandler() {}

func another() {}
`
	got, removed := scrubConflictMarkers(input)
	if removed == 0 {
		t.Fatal("expected markers to be removed")
	}
	if removed != 3 {
		t.Fatalf("expected 3 marker lines removed, got %d", removed)
	}
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestScrubConflictMarkers_noMarkers(t *testing.T) {
	input := `package api

func Handler() {}
`
	got, removed := scrubConflictMarkers(input)
	if removed != 0 {
		t.Fatal("expected no markers to be removed")
	}
	if got != input {
		t.Errorf("got:\n%s\nwant:\n%s", got, input)
	}
}

func TestScrubConflictMarkers_emptyFile(t *testing.T) {
	got, removed := scrubConflictMarkers("")
	if removed != 0 {
		t.Fatal("expected no markers in empty file")
	}
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestScrubConflictMarkers_goCodeWithEqualsIsNotMarker(t *testing.T) {
	input := `package api

const x = "test"
if a == b {
	// not a marker
}
`
	got, removed := scrubConflictMarkers(input)
	if removed != 0 {
		t.Fatal("expected no markers removed — valid Go code")
	}
	if got != input {
		t.Errorf("got:\n%s\nwant:\n%s", got, input)
	}
}

func TestScrubFileConflictMarkers_roundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "handlers.go")
	corrupted := `>>>>>>> REPLACE
package api

func Handler() {}
`
	if err := os.WriteFile(path, []byte(corrupted), 0644); err != nil {
		t.Fatal(err)
	}
	changed, removed, err := ScrubFileConflictMarkers(path)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected file to be changed")
	}
	if removed != 1 {
		t.Fatalf("expected 1 line removed, got %d", removed)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	want := `package api

func Handler() {}
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestIsConflictMarkerLine(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{">>>>>>> REPLACE", true},
		{"<<<<<<< SEARCH", true},
		{"=======", true},
		{">>>>>>> main", true},
		{"=======\n", true},
		{"package main", false},
		{"func main() {}", false},
		{"a == b", false},
		{"x = 5", false},
	}
	for _, tt := range tests {
		got := IsConflictMarkerLine(tt.line)
		if got != tt.want {
			t.Errorf("IsConflictMarkerLine(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}
