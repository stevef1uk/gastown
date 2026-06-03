package doctor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
	missingDoltDB    []rigCheckInfo    // Rigs missing Dolt database
	missingPrefixCfg []rigCheckInfo    // Rigs missing issue-prefix in config.yaml
	missingPrefixDB  []rigCheckInfo    // Rigs missing issue-prefix in Dolt database
	dbNameMismatches []dbMismatch      // Dolt database name doesn't match prefix
}

type rigCheckInfo struct {
	name       string
	path       string
	prefix     string
	isTownRoot bool
}

type prefixMismatch struct {
	rigName        string
	configPrefix   string
	registryPrefix string
}

type rigBeadInfo struct {
	rigName    string
	path       string
	prefix     string
	gitURL     string
	isTownRoot bool
}

type dbMismatch struct {
	rigName     string
	prefix      string
	currentDB   string
	expectedDB  string
}

// resolvePrefixStringsForRigFix returns bd init --prefix value and the value
// stored in config.issue_prefix (no trailing hyphen). Town root uses hq.
// For rigs with an empty rigs.json beads.prefix, the prefix is taken from
// town .beads/routes.jsonl so --fix matches DatabasePrefixCheck / bd routing.
func resolvePrefixStringsForRigFix(townRoot string, info rigCheckInfo) (forInit string, forDB string, err error) {
	if info.isTownRoot {
		return "hq", "hq", nil
	}
	raw := strings.TrimSpace(info.prefix)
	if raw != "" {
		if !strings.HasSuffix(raw, "-") {
			raw = raw + "-"
		}
		return raw, strings.TrimSuffix(raw, "-"), nil
	}
	routes, err := beads.LoadRoutes(filepath.Join(townRoot, ".beads"))
	if err != nil {
		return "", "", fmt.Errorf("load routes for rig %s: %w", info.name, err)
	}
	rigRoot := filepath.Clean(info.path)
	mayorWorktree := filepath.Join(rigRoot, "mayor", "rig")
	for _, r := range routes {
		routeAbs := filepath.Clean(filepath.Join(townRoot, r.Path))
		if routeAbs == mayorWorktree || routeAbs == rigRoot {
			p := strings.TrimSpace(r.Prefix)
			if p == "" {
				continue
			}
			if !strings.HasSuffix(p, "-") {
				p = p + "-"
			}
			return p, strings.TrimSuffix(p, "-"), nil
		}
	}
	return "", "", fmt.Errorf("no beads prefix in rigs.json or routes.jsonl for rig %q", info.name)
}

// rigBeadsDir returns the canonical on-disk beads directory (mayor/rig/.beads or
// rig-root .beads per FindRigBeadsDir). Town root always uses <town>/.beads.
func rigBeadsDir(townRoot string, info rigCheckInfo) string {
	if info.isTownRoot {
		return filepath.Join(info.path, ".beads")
	}
	return doltserver.FindRigBeadsDir(townRoot, info.name)
}

// expectedBeadsPrefixForRig returns the beads issue prefix (no trailing hyphen) used
// for identity bead IDs and yaml/db expectations. Falls back to routes.jsonl when
// rigs.json omits beads.prefix.
func expectedBeadsPrefixForRig(townRoot string, info rigCheckInfo) string {
	if info.isTownRoot {
		return "hq"
	}
	p := strings.TrimSpace(info.prefix)
	if p != "" {
		return strings.TrimSuffix(p, "-")
	}
	routes, err := beads.LoadRoutes(filepath.Join(townRoot, ".beads"))
	if err != nil {
		return ""
	}
	rigRoot := filepath.Clean(info.path)
	mayorWT := filepath.Join(rigRoot, "mayor", "rig")
	for _, r := range routes {
		routeAbs := filepath.Clean(filepath.Join(townRoot, r.Path))
		if routeAbs == mayorWT || routeAbs == rigRoot {
			return strings.TrimSuffix(strings.TrimSpace(r.Prefix), "-")
		}
	}
	return ""
}

// sqlIssuePrefixLookupState interprets bd sql --json for `select value ... issue_prefix`.
// When determined is false (empty stdout, non-JSON, or unparseable), callers must not
// flag missing DB prefix — this matches legacy behavior and bd test stubs that exit 0
// with no output. When determined is true, missing reflects an empty/absent value.
func sqlIssuePrefixLookupState(out []byte) (missing, determined bool) {
	out = bytes.TrimSpace(out)
	if len(out) == 0 || (out[0] != '[' && out[0] != '{') {
		return false, false
	}
	if bytes.Equal(out, []byte("[]")) {
		return true, true
	}
	var rows []map[string]interface{}
	if err := json.Unmarshal(out, &rows); err != nil {
		var wrapped struct {
			Rows []map[string]interface{} `json:"rows"`
		}
		if err2 := json.Unmarshal(out, &wrapped); err2 != nil {
			return false, false
		}
		if len(wrapped.Rows) == 0 {
			return true, true
		}
		rows = wrapped.Rows
	} else if len(rows) == 0 {
		return true, true
	}
	row := rows[0]
	for _, key := range []string{"value", "Value", "VALUE"} {
		if v, ok := row[key].(string); ok {
			return v == "", true
		}
	}
	for _, v := range row {
		if s, ok := v.(string); ok {
			return s == "", true
		}
	}
	// Row parsed but no string column — treat as missing value.
	return true, true
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
	c.missingPrefixDB = nil
	c.dbNameMismatches = nil
	var details []string
	var rigsToCheck []rigCheckInfo
	for name, entry := range rigsConfig.Rigs {
		prefix := ""
		if entry.BeadsConfig != nil {
			prefix = entry.BeadsConfig.Prefix
		}
		rigsToCheck = append(rigsToCheck, rigCheckInfo{
			name:   name,
			path:   filepath.Join(ctx.TownRoot, name),
			prefix: prefix,
		})
	}
	// Add town root rig
	townName, err := workspace.GetTownName(ctx.TownRoot)
	if err != nil || townName == "" {
		townName = "hq"
	}
	rigsToCheck = append(rigsToCheck, rigCheckInfo{
		name:       townName,
		path:       ctx.TownRoot,
		isTownRoot: true,
	})

	for _, info := range rigsToCheck {
		rigName := info.name
		rigPath := info.path
		configPath := filepath.Join(rigPath, "config.json")
		if info.isTownRoot {
			configPath = filepath.Join(rigPath, "settings", "config.json")
		}

		// Skip existence check for town root directory (we know it exists)
		if !info.isTownRoot {
			if _, err := os.Stat(rigPath); os.IsNotExist(err) {
				details = append(details, fmt.Sprintf("Registered rig %s directory does not exist", rigName))
				continue
			}
		}

		// Registry prefix from rigs.json (for config.json mismatch vs registry)
		registryPrefix := ""
		if info.isTownRoot {
			registryPrefix = "hq"
		} else {
			registryPrefix = info.prefix
		}
		beadsExpectedPrefix := expectedBeadsPrefixForRig(ctx.TownRoot, info)

		// Check if config.json exists
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			c.missingConfig = append(c.missingConfig, rigName)
			details = append(details, fmt.Sprintf("Rig %s is registered but missing config.json", rigName))
			// Continue to check beads/dolt even if config.json missing
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
				if registryPrefix != "" && configPrefix != "" && registryPrefix != configPrefix {
					c.prefixMismatches = append(c.prefixMismatches, prefixMismatch{
						rigName:        rigName,
						configPrefix:   configPrefix,
						registryPrefix: registryPrefix,
					})
					details = append(details, fmt.Sprintf(
						"Rig %s prefix mismatch: config.json has %q, registry has %q",
						rigName, configPrefix, registryPrefix))
				}
			}
		}

		// Check beads configuration at canonical .beads (mayor/rig or rig-root)
		beadsDir := rigBeadsDir(ctx.TownRoot, info)

		metadataPath := filepath.Join(beadsDir, "metadata.json")
		if _, err := os.Stat(beadsDir); os.IsNotExist(err) {
			details = append(details, fmt.Sprintf("Rig %s is missing .beads directory at %s", rigName, beadsDir))
			c.missingDoltDB = append(c.missingDoltDB, info)
			c.missingPrefixCfg = append(c.missingPrefixCfg, info)
			// Continue to check identity bead (it might be in a different beadsDir)
		} else {
			// Check issue-prefix in config.yaml
			configYamlPath := filepath.Join(beadsDir, "config.yaml")
			if data, err := os.ReadFile(configYamlPath); err == nil {
				// Check for both issue-prefix: and issue_prefix: (bd supports both)
				hasPrefix := strings.Contains(string(data), "issue-prefix:") || strings.Contains(string(data), "issue_prefix:")
				if !hasPrefix && beadsExpectedPrefix != "" {
					c.missingPrefixCfg = append(c.missingPrefixCfg, info)
					details = append(details, fmt.Sprintf("Rig %s .beads/config.yaml missing issue-prefix", rigName))
				}
			} else if os.IsNotExist(err) {
				c.missingPrefixCfg = append(c.missingPrefixCfg, info)
				details = append(details, fmt.Sprintf("Rig %s .beads/config.yaml not found", rigName))
			}

			// Check metadata.json for Dolt database
			if _, err := os.Stat(metadataPath); os.IsNotExist(err) {
				details = append(details, fmt.Sprintf("Rig %s is missing .beads/metadata.json", rigName))
				c.missingDoltDB = append(c.missingDoltDB, info)
			} else {
				// Read database name from metadata.json
				metadataBytes, err := os.ReadFile(metadataPath)
				if err == nil {
					var meta struct {
						DoltDatabase string `json:"dolt_database"`
						DoltMode     string `json:"dolt_mode"`
					}
					if err := json.Unmarshal(metadataBytes, &meta); err == nil {
						expectedDB := rigName
						if info.isTownRoot {
							expectedDB = "hq"
						}
						if meta.DoltDatabase != expectedDB {
							c.dbNameMismatches = append(c.dbNameMismatches, dbMismatch{
								rigName:    rigName,
								prefix:     beadsExpectedPrefix,
								currentDB:  meta.DoltDatabase,
								expectedDB: expectedDB,
							})
							details = append(details, fmt.Sprintf("Rig %s database name mismatch: metadata says %q, expected %q",
								rigName, meta.DoltDatabase, expectedDB))
						}
					}
				}
			}
		}

		// Check issue_prefix in Dolt database if metadata.json exists
		if _, err := os.Stat(metadataPath); err == nil {
			bd := beads.NewWithBeadsDir(rigPath, beadsDir)
			// Query the config table directly. If missing or empty, it's an issue.
			out, err := bd.SQL("select value from config where `key` = 'issue_prefix'")
			if err == nil {
				missing, determined := sqlIssuePrefixLookupState(out)
				if determined && missing {
					c.missingPrefixDB = append(c.missingPrefixDB, info)
					details = append(details, fmt.Sprintf("Rig %s database is missing issue_prefix in config table", rigName))
				}
			} else {
				// If table doesn't exist or SQL fails, we treat it as missing prefix config
				// unless it's a connection error which is handled by other checks.
				errStr := err.Error()
				if strings.Contains(errStr, "not found") || strings.Contains(errStr, "config") {
					c.missingPrefixDB = append(c.missingPrefixDB, info)
					details = append(details, fmt.Sprintf("Rig %s database is missing config table or issue_prefix", rigName))
				}
			}
		}

		// Check for rig identity bead (skip when we cannot derive a prefix)
		if beadsExpectedPrefix != "" {
			rigBeadID := beads.RigBeadIDWithPrefix(beadsExpectedPrefix, rigName)
			bd := beads.NewWithBeadsDir(rigPath, beadsDir)
			if _, err := bd.Show(rigBeadID); err != nil {
				if errors.Is(err, beads.ErrNotFound) {
					details = append(details, fmt.Sprintf("Rig %s is missing identity bead %s", rigName, rigBeadID))
					c.missingRigBeads = append(c.missingRigBeads, rigBeadInfo{
						rigName:    rigName,
						path:       rigPath,
						prefix:     beadsExpectedPrefix,
						gitURL:     rigsConfig.Rigs[rigName].GitURL,
						isTownRoot: info.isTownRoot,
					})
				}
			}
		}
	}

	// Check for summary
	issueCount := len(c.missingConfig) + len(c.prefixMismatches) + len(c.missingRigBeads) + len(c.missingDoltDB) + len(c.missingPrefixCfg) + len(c.missingPrefixDB) + len(c.dbNameMismatches)
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
		parts = append(parts, fmt.Sprintf("%d missing issue-prefix (yaml)", len(c.missingPrefixCfg)))
	}
	if len(c.missingPrefixDB) > 0 {
		parts = append(parts, fmt.Sprintf("%d missing issue-prefix (db)", len(c.missingPrefixDB)))
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
	townName, _ := workspace.GetTownName(ctx.TownRoot)
	if townName == "" {
		townName = "hq"
	}
	for _, rigName := range c.missingConfig {
		if rigName == townName {
			if _, err := config.EnsureTownSettingsFile(ctx.TownRoot); err != nil {
				return fmt.Errorf("could not create town settings for %s: %w", rigName, err)
			}
			continue
		}

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

	// Fix missing Dolt database first so bd init --force cannot clobber issue_prefix
	// written by the missingPrefixDB / missingPrefixCfg loops (GH#doctor-order).
	for _, info := range c.missingDoltDB {
		forInit, _, err := resolvePrefixStringsForRigFix(ctx.TownRoot, info)
		if err != nil {
			return err
		}

		beadsDir := rigBeadsDir(ctx.TownRoot, info)

		// If the beads directory already exists, never run bd init --force: it wipes
		// the Dolt database and destroys agent beads, rig identity beads, and issues.
		if _, err := os.Stat(beadsDir); err == nil {
			rigName := info.name
			if rigName == "" {
				if info.isTownRoot {
					rigName = "hq"
				} else {
					rigName = filepath.Base(info.path)
				}
			}
			if err := doltserver.EnsureMetadata(ctx.TownRoot, rigName); err != nil {
				return fmt.Errorf("could not repair metadata for %s: %w", info.name, err)
			}
			continue
		}

		// Run bd init --prefix <prefix> --force --destroy-token to create the database
		destroyToken := "DESTROY-" + strings.TrimSuffix(forInit, "-")
		bdPath, err := exec.LookPath("bd")
		if err != nil {
			return fmt.Errorf("beads (bd) binary not found in PATH")
		}
		cmd := exec.Command(bdPath, "init", "--prefix", forInit, "--force", "--destroy-token="+destroyToken)
		cmd.Dir = info.path
		cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("could not init beads for %s: %v: %s", info.name, err, string(out))
		}

		rigName := info.name
		if rigName == "" {
			if info.isTownRoot {
				rigName = "hq"
			} else {
				rigName = filepath.Base(info.path)
			}
		}
		if err := doltserver.EnsureMetadata(ctx.TownRoot, rigName, rigName); err != nil {
			return fmt.Errorf("could not ensure metadata for %s after init: %w", rigName, err)
		}
	}

	// Fix missing issue-prefix in config.yaml
	for _, info := range c.missingPrefixCfg {
		_, forDB, err := resolvePrefixStringsForRigFix(ctx.TownRoot, info)
		if err != nil {
			return err
		}

		beadsDir := rigBeadsDir(ctx.TownRoot, info)
		configYamlPath := filepath.Join(beadsDir, "config.yaml")

		// Create .beads dir if missing
		if err := os.MkdirAll(beadsDir, 0755); err != nil {
			return fmt.Errorf("could not create beads directory: %w", err)
		}

		// Initialize beads database if missing
		// Note: bd init handles creating metadata.json and config.yaml
		// Initialization is handled by the missingDoltDB loop if metadata.json is missing.
		// If metadata.json exists but config.yaml is missing, we already created config.yaml above.
		if _, err := os.Stat(configYamlPath); os.IsNotExist(err) {
			_ = os.WriteFile(configYamlPath, []byte("issue-prefix: "+forDB+"\n"), 0644)
		}
	}

	// Fix missing issue-prefix in Dolt database
	for _, info := range c.missingPrefixDB {
		_, forDB, err := resolvePrefixStringsForRigFix(ctx.TownRoot, info)
		if err != nil {
			return err
		}

		beadsDir := rigBeadsDir(ctx.TownRoot, info)

		bd := beads.NewWithBeadsDir(info.path, beadsDir)

		// 1. Ensure config table exists
		_, _ = bd.SQL("create table if not exists config (`key` varchar(255) primary key, `value` varchar(255))")

		// 2. Insert or update issue_prefix
		esc := strings.ReplaceAll(forDB, "'", "''")
		query := fmt.Sprintf("replace into config (`key`, `value`) values ('issue_prefix', '%s')", esc)
		if _, err := bd.SQL(query); err != nil {
			return fmt.Errorf("could not set issue_prefix in database for %s: %w", info.name, err)
		}
		// Also set via bd so config.yaml / cached layers match (belt-and-suspenders).
		if _, err := bd.Run("config", "set", "issue_prefix", forDB); err != nil {
			return fmt.Errorf("could not bd config set issue_prefix for %s: %w", info.name, err)
		}
	}

	// Fix database name mismatches
	for _, mismatch := range c.dbNameMismatches {
		// Just ensure metadata is correct, Dolt will handle it on next restart
		rigName := mismatch.rigName
		if mismatch.prefix == "hq" {
			rigName = "hq"
		}
		if err := doltserver.EnsureMetadata(ctx.TownRoot, rigName, rigName); err != nil {
			return fmt.Errorf("could not repair db metadata for %s: %w", rigName, err)
		}
	}

	// Fix missing rig identity beads
	for _, info := range c.missingRigBeads {
		// Resolve beads from rig (or town) root so rig-root .beads + redirect layouts work.
		bd := beads.New(info.path)
		fields := &beads.RigFields{
			Repo:   info.gitURL,
			Prefix: info.prefix,
			State:  beads.RigStateActive,
		}
		if _, err := bd.EnsureRigBead(info.rigName, fields); err != nil {
			return fmt.Errorf("could not create rig bead for %s: %w", info.rigName, err)
		}
	}

	// Final metadata reconciliation across all known rigs. This is safe/idempotent
	// and prevents a stale dolt_database value from surviving partial fixes.
	for rigName := range rigsConfig.Rigs {
		if err := doltserver.EnsureMetadata(ctx.TownRoot, rigName, rigName); err != nil {
			return fmt.Errorf("could not reconcile metadata for rig %s: %w", rigName, err)
		}
	}
	if err := doltserver.EnsureMetadata(ctx.TownRoot, "hq", "hq"); err != nil {
		return fmt.Errorf("could not reconcile metadata for hq: %w", err)
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
	bd := beads.New(rigPath)
	_, err := bd.Show(rigBeadID)
	return err == nil
}