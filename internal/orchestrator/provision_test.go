package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSyncTownAssets_freshTown(t *testing.T) {
	dir := t.TempDir()
	res, err := SyncTownAssets(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Added == 0 {
		t.Fatal("expected files added")
	}
	rigFlow := filepath.Join(dir, TownAssetsSubdir, "templates", "rig-flow.yaml")
	if _, err := os.Stat(rigFlow); err != nil {
		t.Fatalf("rig-flow.yaml: %v", err)
	}
	design := filepath.Join(dir, TownAssetsSubdir, "prompts", "rig-flow", "design.md")
	if _, err := os.Stat(design); err != nil {
		t.Fatalf("design.md: %v", err)
	}
}

func TestSyncTownAssets_noOverwriteWithoutFlag(t *testing.T) {
	dir := t.TempDir()
	if _, err := SyncTownAssets(dir, true); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, TownAssetsSubdir, "templates", "rig-flow.yaml")
	if err := os.WriteFile(path, []byte("custom"), 0644); err != nil {
		t.Fatal(err)
	}
	res, err := SyncTownAssets(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Updated != 0 {
		t.Fatalf("expected no updates, got %d", res.Updated)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "custom" {
		t.Fatal("custom content should be preserved")
	}
}

func TestSyncTownAssets_updateChanged(t *testing.T) {
	dir := t.TempDir()
	if _, err := SyncTownAssets(dir, true); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, TownAssetsSubdir, "templates", "rig-flow.yaml")
	if err := os.WriteFile(path, []byte("custom"), 0644); err != nil {
		t.Fatal(err)
	}
	res, err := SyncTownAssets(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Updated == 0 {
		t.Fatal("expected rig-flow.yaml to be updated from embed")
	}
	data, _ := os.ReadFile(path)
	if string(data) == "custom" {
		t.Fatal("expected embedded content to replace custom")
	}
}
