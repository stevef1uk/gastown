package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"errors"
	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/doltserver"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/workspace"
)

// RigConfigSyncCheck verifies that all registered rigs have a config.json file,
// Dolt database, and rig identity bead. This prevents issues where the daemon
// can't find the beads prefix to check docked/parked status.
type RigConfigSyncCheck struct {
	FixableCheck
	missingConfig    []string          // Rig names missing config.json
	prefixMismatches []prefixMismatch  // Prefix mismatches between config.json and registry
	missingRigBeads  []rigBeadInfo     // Rigs missing identity beads
	missingDoltDB    []rigCheckInfo   // Rigs missing Dolt database
	missingPrefixCfg []rigCheckInfo   // Rigs missing issue-prefix in config.yaml
	dbNameMismatches []dbMismatch      // Dolt database name doesn't match prefix
}

type rigCheckInfo struct {
	name       string
	path       string
	isTownRoot bool
}

type prefixMismatch struct {
	rigName        string
	configPrefix   string
	registryPrefix string
}

type rigBeadInfo struct {
	rigName string
	prefix  string
	gitURL  string
}

type dbMismatch struct {
	rigName     string
	prefix      string
	currentDB   string
	expectedDB  string
}

// NewRigConfigSyncCheck creates a new rig config sync check.
func NewRigConfigSyncCheck() *RigConfigSyncCheck {
	return &RigConfigSyncCheck{
		FixableCheck: FixableCheck{
			BaseCheck: BaseCheck{
				CheckName:        "rig-config-sync",
				CheckDescription: "Verify registered rigs have config.json, Dolt DB, and identity beads",
				CheckCategory:    CategoryConfig,
			},
		},
	}
}

// Run checks if all registered rigs have proper configuration.
func (c *RigConfigSyncCheck) Run(ctx *CheckContext) *CheckResult {
	rigsConfigPath := filepath.Join(ctx.TownRoot, "mayor", "rigs.json")
	rigsConfig, err := config.LoadRigsConfig(rigsConfigPath)
	if err != nil {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusWarning,
			Message: "Could not load rigs registry",
			Details: []string{err.Error()},
		}
	}

	c.missingConfig = nil
	c.prefixMismatches = nil
	c.missingRigBeads = nil
	c.missingDoltDB = nil
	c.missingPrefixCfg = nil
	c.dbNameMismatches = nil
	var details []string

	var rigsToCheck []rigCheckInfo
	for name := range rigsConfig.Rigs {
		rigsToCheck = append(rigsToCheck, rigCheckInfo{
			name: name,
			path: filepath.Join(ctx.TownRoot, name),
		})
	}
	// Add town root rig
	townName, _ := workspace.GetTownName(ctx.TownRoot)
	if townName == "" {
		townName = filepath.Base(ctx.TownRoot)
	}
	rigsToCheck = append(rigsToCheck, rigCheckInfo{
		name:       townName,
		path:       ctx.TownRoot,
		isTownRoot: true,
	})

	for _, info := range rigsToCheck {
		rigName := info.name
		rigPath := info.path

		// Get expected prefix
		expectedPrefix := ""
		if info.isTownRoot {
			expectedPrefix = "hq"
		} else if entry, ok := rigsConfig.Rigs[rigName]; ok && entry.BeadsConfig != nil {
			expectedPrefix = entry.BeadsConfig.Prefix
		}

		// Check if rig directory exists
		if _, err := os.Stat(rigPath); os.IsNotExist(err) {
			details = append(details, fmt.Sprintf("Registered rig %s directory does not exist", rigName))
			continue
		}

		// Check if config.json exists
		configPath := filepath.Join(rigPath, "config.json")
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			if !info.isTownRoot {
				c.missingConfig = append(c.missingConfig, rigName)
				details = append(details, fmt.Sprintf("Rig %s is registered but missing config.json", rigName))
			}
		} else {
			// Check if config.json has correct prefix
			rigCfg, err := rig.LoadRigConfig(rigPath)
			if err != nil {
				details = append(details, fmt.Sprintf("Rig %s has unreadable config.json: %v", rigName, err))
			} else {
				configPrefix := ""
				if rigCfg.Beads != nil {
					configPrefix = rigCfg.Beads.Prefix
				}

				// Compare prefixes
				if expectedPrefix != "" && configPrefix != "" && expectedPrefix != configPrefix {
					c.prefixMismatches = append(c.prefixMismatches, prefixMismatch{
						rigName:        rigName,
						configPrefix:   configPrefix,
						registryPrefix: expectedPrefix,
					})
					details = append(details, fmt.Sprintf(
						"Rig %s prefix mismatch: config.json has %q, registry has %q",
						rigName, configPrefix, expectedPrefix))
				}
			}
		}

		// Check beads configuration
		beadsDir := filepath.Join(rigPath, "mayor", "rig", ".beads")
		if info.isTownRoot {
			beadsDir = filepath.Join(rigPath, ".beads")
		}

		if _, err := os.Stat(beadsDir); os.IsNotExist(err) {
			details = append(details, fmt.Sprintf("Rig %s is missing beads directory at %s", rigName, beadsDir))
			// missingPrefixCfg logic below will handle creating the directory if it's missing
		}

		// Check issue-prefix in config.yaml
		configYamlPath := filepath.Join(beadsDir, "config.yaml")
		if data, err := os.ReadFile(configYamlPath); err != nil {
			if os.IsNotExist(err) && expectedPrefix != "" {
				c.missingPrefixCfg = append(c.missingPrefixCfg, info)
				details = append(details, fmt.Sprintf("Rig %s is missing .beads/config.yaml", rigName))
			}
		} else {
			if !hasUncommentedPrefix(string(data)) && expectedPrefix != "" {
				c.missingPrefixCfg = append(c.missingPrefixCfg, info)
				details = append(details, fmt.Sprintf("Rig %s .beads/config.yaml missing issue-prefix", rigName))
			}
		}

		// Check metadata.json for Dolt database
		metadataPath := filepath.Join(beadsDir, "metadata.json")
		if _, err := os.Stat(metadataPath); os.IsNotExist(err) {
			details = append(details, fmt.Sprintf("Rig %s is missing .beads/metadata.json", rigName))
			c.missingDoltDB = append(c.missingDoltDB, info)
		} else {
			// Read database name from metadata.json
			metadataBytes, err := os.ReadFile(metadataPath)
			if err != nil {
				details = append(details, fmt.Sprintf("Rig %s could not read metadata.json: %v", rigName, err))
			} else {
				var meta struct {
					DoltDatabase string `json:"dolt_database"`
				}
				if err := json.Unmarshal(metadataBytes, &meta); err != nil {
					details = append(details, fmt.Sprintf("Rig %s has invalid metadata.json: %v", rigName, err))
				} else {
					// Compare prefix with database name (convention: DB name should be rig name)
					// Special case: town root uses "hq" database
					expectedDB := rigName
					if info.isTownRoot {
						expectedDB = "hq"
					}

					if meta.DoltDatabase != expectedDB {
						c.dbNameMismatches = append(c.dbNameMismatches, dbMismatch{
							rigName:    rigName,
							prefix:     expectedPrefix,
							currentDB:  meta.DoltDatabase,
							expectedDB: expectedDB,
						})
						details = append(details, fmt.Sprintf("Rig %s database name mismatch: metadata says %q, expected %q",
							rigName, meta.DoltDatabase, expectedDB))
					}
				}
			}
		}

		// 2. Check for rig identity bead
		rigBeadID := beads.RigBeadIDWithPrefix(expectedPrefix, rigName)
		bd := beads.NewWithBeadsDir(rigPath, beadsDir)
		if _, err := bd.Show(rigBeadID); err != nil {
			if errors.Is(err, beads.ErrNotFound) {
				details = append(details, fmt.Sprintf("Rig %s is missing identity bead %s", rigName, rigBeadID))
				c.missingRigBeads = append(c.missingRigBeads, rigBeadInfo{
					rigName: rigName,
					prefix:  expectedPrefix,
				})
			}
		}
	}


	// Check for summary
	issueCount := len(c.missingConfig) + len(c.prefixMismatches) + len(c.missingRigBeads) + len(c.missingDoltDB) + len(c.missingPrefixCfg) + len(c.dbNameMismatches)
	if issueCount == 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: "All registered rigs have valid configuration",
		}
	}

	var parts []string
	if len(c.missingConfig) > 0 {
		parts = append(parts, fmt.Sprintf("%d missing config.json", len(c.missingConfig)))
	}
	if len(c.prefixMismatches) > 0 {
		parts = append(parts, fmt.Sprintf("%d prefix mismatch(es)", len(c.prefixMismatches)))
	}
	if len(c.missingRigBeads) > 0 {
		parts = append(parts, fmt.Sprintf("%d missing identity bead(s)", len(c.missingRigBeads)))
	}
	if len(c.missingDoltDB) > 0 {
		parts = append(parts, fmt.Sprintf("%d missing Dolt DB(s)", len(c.missingDoltDB)))
	}
	if len(c.missingPrefixCfg) > 0 {
		parts = append(parts, fmt.Sprintf("%d missing issue-prefix", len(c.missingPrefixCfg)))
	}
	if len(c.dbNameMismatches) > 0 {
		parts = append(parts, fmt.Sprintf("%d DB name mismatch(es)", len(c.dbNameMismatches)))
	}

	return &CheckResult{
		Name:    c.Name(),
		Status:  StatusWarning,
		Message: strings.Join(parts, ", "),
		Details: details,
		FixHint: "Run 'gt doctor --fix' to create missing config files and databases",
	}
}

// Fix creates missing config.json files, Dolt databases, and rig identity beads.
func (c *RigConfigSyncCheck) Fix(ctx *CheckContext) error {
	rigsConfigPath := filepath.Join(ctx.TownRoot, "mayor", "rigs.json")
	rigsConfig, err := config.LoadRigsConfig(rigsConfigPath)
	if err != nil {
		return fmt.Errorf("could not load rigs registry: %w", err)
	}

	// Fix missing config.json files
	for _, rigName := range c.missingConfig {
		entry, ok := rigsConfig.Rigs[rigName]
		if !ok {
			continue
		}

		rigPath := filepath.Join(ctx.TownRoot, rigName)
		configPath := filepath.Join(rigPath, "config.json")

		prefix := ""
		if entry.BeadsConfig != nil {
			prefix = entry.BeadsConfig.Prefix
		}

		rigCfg := &rig.RigConfig{
			Type:      "rig",
			Version:   1,
			Name:      rigName,
			GitURL:    entry.GitURL,
			CreatedAt: entry.AddedAt,
		}
		if prefix != "" {
			rigCfg.Beads = &rig.BeadsConfig{Prefix: prefix}
		}

		data, err := json.MarshalIndent(rigCfg, "", "  ")
		if err != nil {
			return fmt.Errorf("could not serialize config for %s: %w", rigName, err)
		}

		if err := os.WriteFile(configPath, data, 0644); err != nil {
			return fmt.Errorf("could not write config.json for %s: %w", rigName, err)
		}
	}

	// Fix missing issue-prefix in config.yaml
	for _, info := range c.missingPrefixCfg {
		beadsDir := filepath.Join(info.path, "mayor", "rig", ".beads")
		if info.isTownRoot {
			beadsDir = filepath.Join(info.path, ".beads")
		}

		if err := os.MkdirAll(beadsDir, 0755); err != nil {
			return fmt.Errorf("could not create beads directory for %s: %w", info.name, err)
		}

		configYamlPath := filepath.Join(beadsDir, "config.yaml")
		prefix := "hq"
		if !info.isTownRoot {
			if rigsConfig != nil {
				if entry, ok := rigsConfig.Rigs[info.name]; ok && entry.BeadsConfig != nil {
					prefix = entry.BeadsConfig.Prefix
				}
			}
		}

		newLine := fmt.Sprintf("\nissue-prefix: %q\n", prefix)
		if _, err := os.Stat(configYamlPath); os.IsNotExist(err) {
			if err := os.WriteFile(configYamlPath, []byte(newLine), 0644); err != nil {
				return fmt.Errorf("could not create config.yaml for %s: %w", info.name, err)
			}
		} else {
			data, err := os.ReadFile(configYamlPath)
			if err != nil {
				continue
			}
			content := string(data)
			if !hasUncommentedPrefix(content) {
				if strings.Contains(content, "# issue-prefix:") {
					content = strings.Replace(content, "# issue-prefix: \"\"", fmt.Sprintf("issue-prefix: %q", prefix), 1)
					content = strings.Replace(content, "# issue-prefix: \"", fmt.Sprintf("issue-prefix: %q", prefix), 1)
				} else {
					content = content + newLine
				}
				if err := os.WriteFile(configYamlPath, []byte(content), 0644); err != nil {
					return fmt.Errorf("could not update config.yaml for %s: %w", info.name, err)
				}
			}
		}

		// Ensure metadata is correct before initializing or updating config,
		// so bd connects to the right centralized database. (gt-zmy)
		rigNameForMetadata := info.name
		if info.isTownRoot {
			rigNameForMetadata = "hq"
		}
		_ = doltserver.EnsureMetadata(ctx.TownRoot, rigNameForMetadata)

		// Initialize beads database if missing
		if _, err := os.Stat(filepath.Join(beadsDir, "metadata.json")); os.IsNotExist(err) {
			destroyToken := "DESTROY-" + info.name
			bdPath, err := exec.LookPath("bd")
			if err != nil {
				return fmt.Errorf("beads (bd) binary not found in PATH")
			}
			cmd := exec.Command(bdPath, "init", "--prefix", prefix, "--force", "--destroy-token="+destroyToken)
			cmd.Dir = info.path
			cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
			if out, err := cmd.CombinedOutput(); err != nil {
				return fmt.Errorf("could not init beads for %s: %v: %s", info.name, err, string(out))
			}
		}
	}

	// Fix missing Dolt database
	for _, info := range c.missingDoltDB {
		beadsDir := filepath.Join(info.path, "mayor", "rig", ".beads")
		if info.isTownRoot {
			beadsDir = filepath.Join(info.path, ".beads")
		}

		if err := os.MkdirAll(beadsDir, 0755); err != nil {
			return fmt.Errorf("could not create beads directory for %s: %w", info.name, err)
		}

		// Ensure metadata is correct before initializing, so bd connects to the
		// right centralized database. (gt-zmy)
		rigNameForMetadata := info.name
		if info.isTownRoot {
			rigNameForMetadata = "hq"
		}
		_ = doltserver.EnsureMetadata(ctx.TownRoot, rigNameForMetadata)

		prefix := "hq"
		if !info.isTownRoot {
			if rigsConfig != nil {
				if entry, ok := rigsConfig.Rigs[info.name]; ok && entry.BeadsConfig != nil {
					prefix = entry.BeadsConfig.Prefix
				}
			}
		}

		destroyToken := "DESTROY-" + info.name
		bdPath, err := exec.LookPath("bd")
		if err != nil {
			return fmt.Errorf("beads (bd) binary not found in PATH")
		}
		cmd := exec.Command(bdPath, "init", "--prefix", prefix, "--force", "--destroy-token="+destroyToken)
		cmd.Dir = info.path
		cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("could not init beads for %s: %v: %s", info.name, err, string(out))
		}
	}

	// Fix database name mismatches - rename database to match rig directory name
	renamedDBs := false
	for _, mismatch := range c.dbNameMismatches {
		rigPath := filepath.Join(ctx.TownRoot, mismatch.rigName)
		// Correct path for town-root rig (it is the town root itself, not a subdirectory)
		if mismatch.prefix == "hq" {
			rigPath = ctx.TownRoot
		}
		beadsDir := filepath.Join(rigPath, "mayor", "rig", ".beads")
		// Detect town root - it has no mayor/rig subdirectory
		if _, err := os.Stat(filepath.Join(rigPath, "mayor", "rig")); os.IsNotExist(err) {
			beadsDir = filepath.Join(rigPath, ".beads")
		}
		metadataPath := filepath.Join(beadsDir, "metadata.json")

		// Read current metadata
		metadataBytes, err := os.ReadFile(metadataPath)
		if err != nil {
			return fmt.Errorf("could not read metadata.json for %s: %w", mismatch.rigName, err)
		}

		var metadata map[string]interface{}
		if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
			return fmt.Errorf("could not parse metadata.json for %s: %w", mismatch.rigName, err)
		}

		// Update database name to match rig directory name
		metadata["dolt_database"] = mismatch.expectedDB

		// Write updated metadata
		newMetadata, err := json.MarshalIndent(metadata, "", "  ")
		if err != nil {
			return fmt.Errorf("could not serialize metadata.json for %s: %w", mismatch.rigName, err)
		}

		if err := os.WriteFile(metadataPath, newMetadata, 0644); err != nil {
			return fmt.Errorf("could not write metadata.json for %s: %w", mismatch.rigName, err)
		}

		// Rename the Dolt database directory
		dataDir := filepath.Join(ctx.TownRoot, ".dolt-data")
		oldDBPath := filepath.Join(dataDir, mismatch.currentDB)
		newDBPath := filepath.Join(dataDir, mismatch.expectedDB)

		if _, err := os.Stat(oldDBPath); err == nil {
			// Check if new path already exists
			if _, err := os.Stat(newDBPath); err == nil {
				// New path exists - this is a conflict, skip rename
				// The database with the correct name already exists
			} else {
				// Rename the database directory
				if err := os.Rename(oldDBPath, newDBPath); err != nil {
					return fmt.Errorf("could not rename database %s to %s: %w", mismatch.currentDB, mismatch.expectedDB, err)
				}
				renamedDBs = true
			}
		}
	}

	// If we renamed databases, restart the Dolt server to pick up the changes.
	// Guard: skip restart if the server has been running less than 60s — restarting
	// during startup churn is a known crash trigger (gt-9bxzs: Dolt NomsBlockStore
	// panic when SIGTERM arrives mid-write). The server will pick up renamed databases
	// on its next natural restart or on the next doctor --fix run once stable.
	if renamedDBs {
		if running, pid, _ := doltserver.IsRunning(ctx.TownRoot); running && pid > 0 {
			const minStableAge = 60 * time.Second
			state, _ := doltserver.LoadState(ctx.TownRoot)
			if state != nil && !state.StartedAt.IsZero() && time.Since(state.StartedAt) < minStableAge {
				// Server started less than 60s ago — skip restart to avoid crash
				// during Dolt startup churn. Databases will be picked up on next restart.
			} else {
				// Stop the server
				if err := doltserver.Stop(ctx.TownRoot); err != nil {
					return fmt.Errorf("could not stop Dolt server for restart: %w", err)
				}
				// Start the server again
				if err := doltserver.Start(ctx.TownRoot); err != nil {
					return fmt.Errorf("could not restart Dolt server: %w", err)
				}
			}
		}
	}

	// Fix missing rig identity beads
	for _, info := range c.missingRigBeads {
		rigPath := filepath.Join(ctx.TownRoot, info.rigName)
		// Correct path for town-root rig (it is the town root itself, not a subdirectory)
		if info.prefix == "hq" {
			rigPath = ctx.TownRoot
		}
		mayorRigPath := filepath.Join(rigPath, "mayor", "rig")
		// Detect town root - it has no mayor/rig subdirectory
		if _, err := os.Stat(mayorRigPath); os.IsNotExist(err) {
			mayorRigPath = rigPath
		}

		// Ensure metadata is correct so bd connects to the right server (gt-zmy)
		rigName := info.rigName
		if info.prefix == "hq" {
			rigName = "hq"
		}
		_ = doltserver.EnsureMetadata(ctx.TownRoot, rigName)

		bd := beads.New(mayorRigPath)
		fields := &beads.RigFields{
			Repo:   info.gitURL,
			Prefix: info.prefix,
			State:  beads.RigStateActive,
		}

		if _, err := bd.CreateRigBead(info.rigName, fields); err != nil {
			return fmt.Errorf("could not create rig bead for %s: %w", info.rigName, err)
		}

		// Add status:docked label if the rig should be docked
		rigBeadID := fmt.Sprintf("%s-rig-%s", info.prefix, info.rigName)
		cmd := exec.Command("bd", "label", rigBeadID, "--add", "status:docked")
		cmd.Dir = mayorRigPath
		_ = cmd.Run() // Best effort - ignore errors
	}

	return nil
}

// doltDatabaseExists checks if a Dolt database exists on the server.
func (c *RigConfigSyncCheck) doltDatabaseExists(ctx *CheckContext, dbName string) bool {
	// Use the doltserver package to list databases
	databases, err := doltserver.ListDatabases(ctx.TownRoot)
	if err != nil {
		return false
	}

	for _, db := range databases {
		if db == dbName {
			return true
		}
	}
	return false
}

// rigBeadExists checks if a rig identity bead exists.
func (c *RigConfigSyncCheck) rigBeadExists(rigBeadID, rigPath string) bool {
	mayorRigPath := filepath.Join(rigPath, "mayor", "rig")

	// Try to show the bead using bd
	cmd := exec.Command("bd", "show", rigBeadID, "--json")
	cmd.Dir = mayorRigPath
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}

	// Check if the output contains the bead ID
	return strings.Contains(string(output), rigBeadID)
}

func hasUncommentedPrefix(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "issue-prefix:") {
			return true
		}
	}
	return false
}