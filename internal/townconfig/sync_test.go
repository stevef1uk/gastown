package townconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSync_freshTown(t *testing.T) {
	for _, marker := range []string{"config.json", "mayor/town.json"} {
		t.Run(marker, func(t *testing.T) {
			testSyncFreshTown(t, marker)
		})
	}
}

func testSyncFreshTown(t *testing.T, marker string) {
	t.Helper()
	dir := t.TempDir()
	markerPath := filepath.Join(dir, filepath.FromSlash(marker))
	if err := os.MkdirAll(filepath.Dir(markerPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(markerPath, []byte(`{}`), 0644); err != nil {
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
		t.Fatal("expected error for missing town markers")
	}
}
