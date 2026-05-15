package beads

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureBeadsDirMode0700_RepairsLoosePerms(t *testing.T) {
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.MkdirAll(beadsDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := EnsureBeadsDirMode0700(beadsDir); err != nil {
		t.Fatalf("EnsureBeadsDirMode0700: %v", err)
	}
	info, err := os.Stat(beadsDir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0700 {
		t.Fatalf("mode = %o, want 0700", got)
	}
}

func TestEnsureBeadsDirMode0700_NoOpWhenMissing(t *testing.T) {
	if err := EnsureBeadsDirMode0700(filepath.Join(t.TempDir(), ".beads")); err != nil {
		t.Fatalf("EnsureBeadsDirMode0700: %v", err)
	}
}
