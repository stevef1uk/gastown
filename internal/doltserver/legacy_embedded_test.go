package doltserver

import (
	"os"
	"path/filepath"
	"testing"
)

// writeDoltDB creates a minimal valid Dolt database directory at dir/.dolt.
func writeDoltDB(t *testing.T, dir string) {
	t.Helper()
	noms := filepath.Join(dir, ".dolt", "noms")
	if err := os.MkdirAll(noms, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(noms, "manifest"), []byte("manifest"), 0644); err != nil {
		t.Fatal(err)
	}
}

func setupSharedTown(t *testing.T) string {
	t.Helper()
	town := t.TempDir()
	if err := os.MkdirAll(filepath.Join(town, ".dolt-data"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(town, ".dolt-data", "config.yaml"), []byte("listener:\n  port: 3307\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Canonical databases.
	writeDoltDB(t, filepath.Join(town, ".dolt-data", "hq"))
	writeDoltDB(t, filepath.Join(town, ".dolt-data", "testgt4"))
	// Rig registration.
	if err := os.MkdirAll(filepath.Join(town, "mayor"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(town, "mayor", "rigs.json"), []byte(`{"rigs":{"testgt4":{},"other":{}}}`), 0644); err != nil {
		t.Fatal(err)
	}
	return town
}

func TestFindLegacyEmbeddedDoltDirs_DuplicateIsSafe(t *testing.T) {
	town := setupSharedTown(t)

	// Stale duplicate: databases match canonical names, including bd's embedded
	// top-level ".dolt" whose name is resolved from metadata.json.
	dup := filepath.Join(town, "testgt4", ".beads", "dolt")
	writeDoltDB(t, filepath.Join(dup, "hq"))
	writeDoltDB(t, dup) // top-level .dolt = bd's embedded database
	if err := os.WriteFile(filepath.Join(town, "testgt4", ".beads", "metadata.json"),
		[]byte(`{"dolt_database":"testgt4"}`), 0644); err != nil {
		t.Fatal(err)
	}

	// Unmigrated data: database not present in .dolt-data.
	unmigrated := filepath.Join(town, "other", ".beads", "dolt")
	writeDoltDB(t, filepath.Join(unmigrated, "brandnew"))

	found := FindLegacyEmbeddedDoltDirs(town)
	byPath := map[string]EmbeddedDoltDir{}
	for _, d := range found {
		byPath[d.Path] = d
	}

	dupInfo, ok := byPath[dup]
	if !ok {
		t.Fatalf("expected duplicate dir %s to be reported; got %+v", dup, found)
	}
	if !dupInfo.SafeToRemove {
		t.Errorf("expected duplicate dir to be safe to remove, got %+v", dupInfo)
	}

	unmigratedInfo, ok := byPath[unmigrated]
	if !ok {
		t.Fatalf("expected unmigrated dir %s to be reported", unmigrated)
	}
	if unmigratedInfo.SafeToRemove {
		t.Errorf("expected unmigrated dir to be unsafe, got %+v", unmigratedInfo)
	}
}

func TestFindLegacyEmbeddedDoltDirs_UnresolvedEmbeddedIsUnsafe(t *testing.T) {
	town := setupSharedTown(t)

	// A top-level ".dolt" with no metadata.json to name it → unsafe.
	dir := filepath.Join(town, "other", ".beads", "dolt")
	writeDoltDB(t, dir)

	found := FindLegacyEmbeddedDoltDirs(town)
	if len(found) != 1 {
		t.Fatalf("expected 1 dir, got %+v", found)
	}
	if found[0].SafeToRemove {
		t.Errorf("expected unresolved embedded dir to be unsafe, got %+v", found[0])
	}
}

func TestRemoveLegacyEmbeddedDoltDirs_RemovesOnlySafe(t *testing.T) {
	town := setupSharedTown(t)

	dup := filepath.Join(town, "testgt4", ".beads", "dolt")
	writeDoltDB(t, filepath.Join(dup, "testgt4"))

	unmigrated := filepath.Join(town, "other", ".beads", "dolt")
	writeDoltDB(t, filepath.Join(unmigrated, "brandnew"))

	removed, skipped, err := RemoveLegacyEmbeddedDoltDirs(town)
	if err != nil {
		t.Fatalf("RemoveLegacyEmbeddedDoltDirs: %v", err)
	}
	if removed != 1 {
		t.Errorf("expected 1 removed, got %d", removed)
	}
	if len(skipped) != 1 || skipped[0] != unmigrated {
		t.Errorf("expected skipped=[%s], got %v", unmigrated, skipped)
	}
	if _, err := os.Stat(dup); !os.IsNotExist(err) {
		t.Errorf("expected duplicate dir %s to be removed", dup)
	}
	if _, err := os.Stat(unmigrated); err != nil {
		t.Errorf("expected unmigrated dir %s to be kept: %v", unmigrated, err)
	}
}

func TestPathWithinDirectory(t *testing.T) {
	tests := []struct {
		path      string
		directory string
		want      bool
	}{
		{"/town/rig/.beads/dolt", "/town/rig/.beads/dolt", true},
		{"/town/rig/.beads/dolt/.doltcfg (deleted)", "/town/rig/.beads/dolt", true},
		{"/town/rig/.beads/dolt-old", "/town/rig/.beads/dolt", false},
		{"/town/.dolt-data/testgt4", "/town/testgt4/.beads/dolt", false},
	}
	for _, tt := range tests {
		if got := pathWithinDirectory(tt.path, tt.directory); got != tt.want {
			t.Errorf("pathWithinDirectory(%q, %q) = %t, want %t", tt.path, tt.directory, got, tt.want)
		}
	}
}

func TestProcessUsesDirectory(t *testing.T) {
	processes := []doltServerProcess{
		{pid: 12, paths: []string{"/town/testgt4/.beads/dolt/.doltcfg"}},
		{pid: 13, paths: []string{"/town/.dolt-data/testgt4"}},
	}
	if !processUsesDirectory(processes, "/town/testgt4/.beads/dolt") {
		t.Fatal("expected legacy Dolt process path to be detected")
	}
	if processUsesDirectory(processes[1:], "/town/testgt4/.beads/dolt") {
		t.Fatal("canonical shared Dolt path must not match a legacy directory")
	}
}

func TestFindLegacyEmbeddedDoltDirs_NoSharedServer(t *testing.T) {
	town := t.TempDir()
	// No .dolt-data/config.yaml → embedded dirs are not considered stale.
	writeDoltDB(t, filepath.Join(town, "testgt4", ".beads", "dolt", "testgt4"))

	if found := FindLegacyEmbeddedDoltDirs(town); len(found) != 0 {
		t.Errorf("expected no legacy dirs without a shared server, got %+v", found)
	}
}
