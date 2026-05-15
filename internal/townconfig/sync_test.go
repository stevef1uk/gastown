package townconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSync_freshTown(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}

	res, err := Sync(dir)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if res.Orchestrator.Added == 0 {
		t.Fatalf("expected orchestrator files added, got %+v", res.Orchestrator)
	}
	if _, err := os.Stat(filepath.Join(dir, "orchestrator", "templates", "rig-flow.yaml")); err != nil {
		t.Fatalf("rig-flow template: %v", err)
	}
}

func TestSync_requiresTownRoot(t *testing.T) {
	_, err := Sync(t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing config.json")
	}
}
