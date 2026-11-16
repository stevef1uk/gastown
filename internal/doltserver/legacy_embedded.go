package doltserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EmbeddedDoltDir describes a legacy per-rig .beads/dolt directory discovered
// while the town runs the shared Dolt server from .dolt-data.
type EmbeddedDoltDir struct {
	// Path is the absolute path to the .beads/dolt directory.
	Path string
	// Databases are the Dolt database names found inside the directory.
	Databases []string
	// SafeToRemove is true when every database inside duplicates a canonical
	// database in .dolt-data (or the directory holds no databases at all), so
	// deleting the directory cannot lose data.
	SafeToRemove bool
}

// FindLegacyEmbeddedDoltDirs finds per-rig .beads/dolt directories that exist
// alongside the centralized .dolt-data Dolt server.
//
// Gas Town runs a single shared Dolt server whose data directory is
// <townRoot>/.dolt-data. A legacy per-rig .beads/dolt directory is an embedded
// data directory that bd may auto-spawn a server against when the shared server
// is unreachable — serving empty/stale databases and producing errors such as
// "database not initialized: issue_prefix config is missing". This is how a
// healthy rig appears to lose its beads after the shared server stops.
//
// Returns nil when the town is not using a local shared server.
func FindLegacyEmbeddedDoltDirs(townRoot string) []EmbeddedDoltDir {
	config := DefaultConfig(townRoot)
	if config.IsRemote() {
		return nil
	}
	// Only relevant when the centralized data directory is actually in use.
	if _, err := os.Stat(filepath.Join(config.DataDir, "config.yaml")); err != nil {
		return nil
	}

	canonical := map[string]bool{}
	if dbs, err := listDatabasesLocal(config); err == nil {
		for _, db := range dbs {
			canonical[db] = true
		}
	}

	var result []EmbeddedDoltDir
	for _, dir := range embeddedDoltDirCandidates(townRoot) {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		// dir is <beads>/.beads/dolt, so its parent is the beads directory whose
		// metadata.json names the embedded database.
		dbs, unresolved := doltDatabasesIn(dir, filepath.Dir(dir))
		// A directory that holds a database we cannot name (a top-level ".dolt"
		// with no metadata.json) cannot be matched against a canonical name, so
		// treat it as unsafe.
		safe := !unresolved
		for _, db := range dbs {
			if !canonical[db] {
				safe = false
				break
			}
		}
		result = append(result, EmbeddedDoltDir{Path: dir, Databases: dbs, SafeToRemove: safe})
	}
	return result
}

// RemoveLegacyEmbeddedDoltDirs removes legacy embedded Dolt directories that are
// safe to delete (stale duplicates of canonical .dolt-data databases). It
// returns the number removed and the paths that were skipped because they may
// hold unmigrated data.
func RemoveLegacyEmbeddedDoltDirs(townRoot string) (removed int, skipped []string, err error) {
	for _, d := range FindLegacyEmbeddedDoltDirs(townRoot) {
		if !d.SafeToRemove {
			skipped = append(skipped, d.Path)
			continue
		}
		if rmErr := os.RemoveAll(d.Path); rmErr != nil {
			return removed, skipped, fmt.Errorf("removing legacy embedded Dolt dir %s: %w", d.Path, rmErr)
		}
		removed++
	}
	return removed, skipped, nil
}

// embeddedDoltDirCandidates lists the .beads/dolt locations that could hold a
// legacy embedded Dolt database.
func embeddedDoltDirCandidates(townRoot string) []string {
	dirs := []string{filepath.Join(townRoot, ".beads", "dolt")}
	for name := range embeddedDoltRigNames(townRoot) {
		dirs = append(dirs,
			filepath.Join(townRoot, name, ".beads", "dolt"),
			filepath.Join(townRoot, name, "mayor", "rig", ".beads", "dolt"),
		)
	}
	return dirs
}

// embeddedDoltRigNames collects rig names from mayor/rigs.json and top-level
// directories (handles rigs not yet registered in rigs.json).
func embeddedDoltRigNames(townRoot string) map[string]struct{} {
	names := make(map[string]struct{})

	if data, err := os.ReadFile(filepath.Join(townRoot, "mayor", "rigs.json")); err == nil {
		var rigs struct {
			Rigs map[string]struct{} `json:"rigs"`
		}
		if json.Unmarshal(data, &rigs) == nil {
			for name := range rigs.Rigs {
				names[name] = struct{}{}
			}
		}
	}

	if entries, err := os.ReadDir(townRoot); err == nil {
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				names[e.Name()] = struct{}{}
			}
		}
	}

	return names
}

// doltDatabasesIn returns the Dolt database names found inside dir, plus
// whether the directory itself is a Dolt database (".dolt" directly inside)
// whose name could not be resolved from the beads metadata.
func doltDatabasesIn(dir, beadsDir string) (databases []string, unresolved bool) {
	if _, err := os.Stat(filepath.Join(dir, ".dolt")); err == nil {
		if name := readDoltDatabaseName(beadsDir); name != "" {
			databases = append(databases, name)
		} else {
			unresolved = true
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return databases, unresolved
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, e.Name(), ".dolt")); err == nil {
			databases = append(databases, e.Name())
		}
	}
	return databases, unresolved
}

// readDoltDatabaseName reads dolt_database from <beadsDir>/metadata.json.
func readDoltDatabaseName(beadsDir string) string {
	data, err := os.ReadFile(filepath.Join(beadsDir, "metadata.json"))
	if err != nil {
		return ""
	}
	var meta struct {
		DoltDatabase string `json:"dolt_database"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return ""
	}
	return meta.DoltDatabase
}
