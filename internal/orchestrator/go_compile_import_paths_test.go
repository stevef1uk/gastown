package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProductionPathsFromImportedPackages(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	layout := "app"
	apiDir := filepath.Join(dir, layout, "internal", "api")
	storeDir := filepath.Join(dir, layout, "internal", "store")
	for _, d := range []string{apiDir, storeDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(apiDir, "handlers.go"), []byte(`package api
import "app/internal/store"
func F() { _ = store.X }
`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "store.go"), []byte("package store\nvar X int\n"), 0644); err != nil {
		t.Fatal(err)
	}
	got := ProductionPathsFromImportedPackages(dir, layout, []string{"app/internal/api/handlers.go"})
	if len(got) != 1 || got[0] != "app/internal/store/store.go" {
		t.Fatalf("got %v", got)
	}
}
