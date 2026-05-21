package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateImplementWritePath_incrementalEditAllowed(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	rel := "linkshelf/internal/store/store.go"
	abs := filepath.Join(dir, rig, "mayor", "rig", rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		t.Fatal(err)
	}
	body := strings.TrimSpace(`
package store

import "errors"

type Store struct{}

func (s *Store) AddLink(url string) error {
	if url == "" {
		return errors.New("empty url")
	}
	return nil
}
`) + "\n"
	if err := os.WriteFile(abs, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	v := DefaultWorkflowValidation()
	v.LayoutRoot = "linkshelf"
	v.RequiredFiles = []string{rel}
	if !PreferIncrementalEdit(dir, rig, rel, v) {
		t.Fatal("fixture should prefer incremental edit")
	}
	if err := ValidateImplementWritePath(dir, rig, "te-store", rel, v, false); err != nil {
		t.Fatalf("partial: %v", err)
	}
	if err := ValidateImplementWritePath(dir, rig, "te-store", rel, v, true); err == nil {
		t.Fatal("expected full WRITE rejection on substantive file")
	}
}
