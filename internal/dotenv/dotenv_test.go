package dotenv

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("GT_ROOT=~/gt-town\nEXISTING=from-file\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EXISTING", "already-set")
	t.Setenv("GT_ROOT", "")

	if err := LoadFile(path); err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	wantRoot := filepath.Join(home, "gt-town")
	if got := os.Getenv("GT_ROOT"); got != wantRoot {
		t.Fatalf("GT_ROOT=%q want %q", got, wantRoot)
	}
	if got := os.Getenv("EXISTING"); got != "already-set" {
		t.Fatalf("EXISTING=%q want already-set", got)
	}
}

func TestLoadFromCwd(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "pkg")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("GT_ROOT="+root+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(sub); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GT_ROOT", "")
	t.Setenv("GT_TOWN_ROOT", "")
	loaded, err := LoadFromCwd()
	if err != nil {
		t.Fatal(err)
	}
	if loaded == "" {
		t.Fatal("expected .env to load")
	}
	if os.Getenv("GT_ROOT") != root {
		t.Fatalf("GT_ROOT=%q want %q", os.Getenv("GT_ROOT"), root)
	}
}
