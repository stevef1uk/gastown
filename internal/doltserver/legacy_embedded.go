package doltserver

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
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
// hold unmigrated data or are in use by a Dolt server.
func RemoveLegacyEmbeddedDoltDirs(townRoot string) (removed int, skipped []string, err error) {
	dirs := FindLegacyEmbeddedDoltDirs(townRoot)
	if len(dirs) == 0 {
		return 0, nil, nil
	}
	processes, err := doltServerProcesses()
	if err != nil {
		return 0, nil, fmt.Errorf("checking Dolt processes before legacy cleanup: %w", err)
	}
	for _, d := range dirs {
		if !d.SafeToRemove {
			skipped = append(skipped, d.Path)
			continue
		}
		if processUsesDirectory(processes, d.Path) {
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

// FindDoltProcessesUsingDirectory returns sql-server process IDs whose working
// directory or configured data path is inside directory.
func FindDoltProcessesUsingDirectory(directory string) ([]int, error) {
	processes, err := doltServerProcesses()
	if err != nil {
		return nil, fmt.Errorf("listing Dolt server processes: %w", err)
	}
	var pids []int
	for _, process := range processes {
		if processUsesDirectory([]doltServerProcess{process}, directory) {
			pids = append(pids, process.pid)
		}
	}
	return pids, nil
}

type doltServerProcess struct {
	pid   int
	paths []string
}

func doltServerProcesses() ([]doltServerProcess, error) {
	out, err := exec.Command("ps", "-eo", "pid=,args=").Output()
	if err != nil {
		return nil, err
	}
	var processes []doltServerProcess
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || !strings.EqualFold(filepath.Base(fields[1]), "dolt") {
			continue
		}
		isServer := false
		for _, field := range fields[2:] {
			if field == "sql-server" {
				isServer = true
				break
			}
		}
		if !isServer {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		process := doltServerProcess{pid: pid}
		if cwd := ProcessWorkingDirectory(pid); cwd != "" {
			process.paths = append(process.paths, cwd)
		}
		if dataDir := GetDoltDataDirFromProcess(pid); dataDir != "" {
			process.paths = append(process.paths, dataDir)
		}
		if configPath := getDoltConfigPathFromProcess(pid); configPath != "" {
			process.paths = append(process.paths, configPath)
			if configData, readErr := os.ReadFile(configPath); readErr == nil {
				var config struct {
					DataDir   string `yaml:"data_dir"`
					ConfigDir string `yaml:"cfg_dir"`
				}
				if yaml.Unmarshal(configData, &config) == nil {
					process.paths = append(process.paths, config.DataDir, config.ConfigDir)
				}
			}
		}
		processes = append(processes, process)
	}
	return processes, nil
}

func processUsesDirectory(processes []doltServerProcess, directory string) bool {
	for _, process := range processes {
		for _, path := range process.paths {
			if pathWithinDirectory(path, directory) {
				return true
			}
		}
	}
	return false
}

func pathWithinDirectory(path, directory string) bool {
	path = strings.TrimSuffix(strings.TrimSpace(path), " (deleted)")
	directory = strings.TrimSuffix(strings.TrimSpace(directory), " (deleted)")
	if path == "" || directory == "" {
		return false
	}
	pathAbs, pathErr := filepath.Abs(path)
	directoryAbs, directoryErr := filepath.Abs(directory)
	if pathErr != nil || directoryErr != nil {
		return false
	}
	pathAbs = filepath.Clean(pathAbs)
	directoryAbs = filepath.Clean(directoryAbs)
	return pathAbs == directoryAbs || strings.HasPrefix(pathAbs, directoryAbs+string(filepath.Separator))
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
