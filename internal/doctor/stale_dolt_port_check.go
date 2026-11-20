package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/steveyegge/gastown/internal/doltserver"
)

// StaleDoltPortCheck detects stale Dolt port files that point to wrong ports.
// This can cause bd commands to fail with "database not found" errors when
// they connect to the wrong Dolt server.
// It also checks metadata.json files for port consistency with the running server.
// Finally, it detects legacy per-rig .beads/dolt directories that let bd
// auto-spawn an embedded server serving empty databases when the shared server
// is unreachable (surfacing as "issue_prefix config is missing").
type StaleDoltPortCheck struct {
	FixableCheck
	stalePorts        []stalePortInfo
	staleMetadata     []staleMetadataInfo
	staleEmbeddedDirs []doltserver.EmbeddedDoltDir
}

type stalePortInfo struct {
	path        string
	port        int
	correctPort int
}

type staleMetadataInfo struct {
	path        string
	port        int
	correctPort int
}

// NewStaleDoltPortCheck creates a new stale Dolt port check.
func NewStaleDoltPortCheck() *StaleDoltPortCheck {
	return &StaleDoltPortCheck{
		FixableCheck: FixableCheck{
			BaseCheck: BaseCheck{
				CheckName:        "stale-dolt-port",
				CheckDescription: "Detect stale Dolt port files pointing to wrong ports",
				CheckCategory:    CategoryConfig,
			},
		},
	}
}

// Run checks for stale Dolt port files and metadata.json port consistency.
func (c *StaleDoltPortCheck) Run(ctx *CheckContext) *CheckResult {
	c.stalePorts = nil
	c.staleMetadata = nil
	c.staleEmbeddedDirs = nil

	// Get the correct port from the main Dolt config
	correctPort := c.getCorrectPort(ctx)
	if correctPort == 0 {
		correctPort = 3307 // default
	}

	// Find all dolt-server.port files
	portFiles := c.findPortFiles(ctx.TownRoot)

	var details []string
	for _, portFile := range portFiles {
		data, err := os.ReadFile(portFile)
		if err != nil {
			continue
		}

		portStr := strings.TrimSpace(string(data))
		port, err := strconv.Atoi(portStr)
		if err != nil {
			continue
		}

		// Check if port matches the correct port
		if port != correctPort {
			c.stalePorts = append(c.stalePorts, stalePortInfo{
				path:        portFile,
				port:        port,
				correctPort: correctPort,
			})
			relPath, _ := filepath.Rel(ctx.TownRoot, portFile)
			details = append(details, fmt.Sprintf("Stale port file %s has port %d (should be %d)", relPath, port, correctPort))
		}
	}

	// Check metadata.json files for port consistency
	metadataFiles := c.findMetadataFiles(ctx.TownRoot)
	for _, metaFile := range metadataFiles {
		port := c.getPortFromMetadata(metaFile)
		if port > 0 && port != correctPort {
			c.staleMetadata = append(c.staleMetadata, staleMetadataInfo{
				path:        metaFile,
				port:        port,
				correctPort: correctPort,
			})
			relPath, _ := filepath.Rel(ctx.TownRoot, metaFile)
			details = append(details, fmt.Sprintf("metadata.json %s has port %d (should be %d)", relPath, port, correctPort))
		}
	}

	// Also check for legacy per-rig .beads/dolt directories that let bd spawn an
	// embedded server serving empty/stale databases when the shared server is down.
	c.staleEmbeddedDirs = doltserver.FindLegacyEmbeddedDoltDirs(ctx.TownRoot)
	for _, d := range c.staleEmbeddedDirs {
		relPath, _ := filepath.Rel(ctx.TownRoot, d.Path)
		note := ""
		if !d.SafeToRemove {
			note = " — contains databases not present in .dolt-data (run 'gt dolt migrate')"
		}
		details = append(details, fmt.Sprintf("Legacy embedded Dolt dir: %s (databases: %s)%s", relPath, strings.Join(d.Databases, ", "), note))
	}

	if len(c.stalePorts) == 0 && len(c.staleMetadata) == 0 && len(c.staleEmbeddedDirs) == 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: "All Dolt port files are consistent",
		}
	}

	return &CheckResult{
		Name:    c.Name(),
		Status:  StatusWarning,
		Message: fmt.Sprintf("%d stale port file(s), %d stale metadata.json(s), %d legacy embedded Dolt dir(s)", len(c.stalePorts), len(c.staleMetadata), len(c.staleEmbeddedDirs)),
		Details: details,
		FixHint: "Run 'gt doctor --fix' to fix port inconsistencies and remove stale embedded Dolt directories",
	}
}

// Fix removes stale Dolt port files, fixes metadata.json port mismatches, and
// removes legacy embedded .beads/dolt directories that duplicate .dolt-data.
func (c *StaleDoltPortCheck) Fix(ctx *CheckContext) error {
	// Remove stale port files
	for _, info := range c.stalePorts {
		if err := os.Remove(info.path); err != nil {
			return fmt.Errorf("could not remove stale port file %s: %w", info.path, err)
		}
	}

	// Fix metadata.json files with wrong ports
	for _, info := range c.staleMetadata {
		if err := c.fixMetadataPort(info.path, info.correctPort); err != nil {
			return fmt.Errorf("could not fix metadata.json %s: %w", info.path, err)
		}
	}

	// Remove legacy embedded Dolt directories that duplicate canonical databases.
	_, skipped, err := doltserver.RemoveLegacyEmbeddedDoltDirs(ctx.TownRoot)
	if err != nil {
		return err
	}
	if len(skipped) > 0 {
		return fmt.Errorf("could not remove legacy embedded Dolt directories (unmigrated data or active server): %s", strings.Join(skipped, ", "))
	}

	return nil
}

// getCorrectPort returns the port from the main Dolt server config.
func (c *StaleDoltPortCheck) getCorrectPort(ctx *CheckContext) int {
	// Check the main Dolt server config
	configPath := filepath.Join(ctx.TownRoot, ".dolt-data", "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return 0
	}

	// Parse port from config.yaml
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "port:" && i+1 < len(lines) {
			portStr := strings.TrimSpace(strings.TrimPrefix(lines[i+1], "port:"))
			port, err := strconv.Atoi(portStr)
			if err == nil {
				return port
			}
		}
		if strings.HasPrefix(line, "  port:") {
			portStr := strings.TrimSpace(strings.TrimPrefix(line, "  port:"))
			port, err := strconv.Atoi(portStr)
			if err == nil {
				return port
			}
		}
	}

	return 0
}

// findPortFiles finds all dolt-server.port files in known locations.
// Avoids filepath.Walk over the entire town root, which is extremely slow
// on Docker bind mounts (macOS VirtioFS).
func (c *StaleDoltPortCheck) findPortFiles(townRoot string) []string {
	var files []string

	// Known locations for port files
	locations := []string{
		filepath.Join(townRoot, ".beads", "dolt-server.port"),
		filepath.Join(townRoot, ".dolt-data", ".beads", "dolt-server.port"),
		filepath.Join(townRoot, "daemon", "dolt.port"),
	}

	// Rig .beads directories (same discovery pattern as findMetadataFiles)
	rigsConfig := filepath.Join(townRoot, "mayor", "rigs.json")
	if data, err := os.ReadFile(rigsConfig); err == nil {
		var rigs struct {
			Rigs map[string]struct{} `json:"rigs"`
		}
		if json.Unmarshal(data, &rigs) == nil {
			for rigName := range rigs.Rigs {
				locations = append(locations,
					filepath.Join(townRoot, rigName, "mayor", "rig", ".beads", "dolt-server.port"),
					filepath.Join(townRoot, rigName, ".beads", "dolt-server.port"),
				)
			}
		}
	}

	for _, loc := range locations {
		if _, err := os.Stat(loc); err == nil {
			files = append(files, loc)
		}
	}

	return files
}

// findMetadataFiles finds all metadata.json files that might contain Dolt port config.
func (c *StaleDoltPortCheck) findMetadataFiles(townRoot string) []string {
	var files []string

	// Town root metadata
	townMeta := filepath.Join(townRoot, ".beads", "metadata.json")
	if _, err := os.Stat(townMeta); err == nil {
		files = append(files, townMeta)
	}

	// Rig metadata files
	rigsConfig := filepath.Join(townRoot, "mayor", "rigs.json")
	if data, err := os.ReadFile(rigsConfig); err == nil {
		var rigs struct {
			Rigs map[string]struct{} `json:"rigs"`
		}
		if json.Unmarshal(data, &rigs) == nil {
			for rigName := range rigs.Rigs {
				rigMeta := filepath.Join(townRoot, rigName, "mayor", "rig", ".beads", "metadata.json")
				if _, err := os.Stat(rigMeta); err == nil {
					files = append(files, rigMeta)
				}
			}
		}
	}

	return files
}

// getPortFromMetadata reads the dolt_server_port from a metadata.json file.
func (c *StaleDoltPortCheck) getPortFromMetadata(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}

	var metadata struct {
		DoltServerPort int `json:"dolt_server_port"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return 0
	}

	return metadata.DoltServerPort
}

// fixMetadataPort updates the dolt_server_port in a metadata.json file.
func (c *StaleDoltPortCheck) fixMetadataPort(path string, correctPort int) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	// Parse as generic map to preserve all fields
	var metadata map[string]interface{}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return err
	}

	// Update the port
	metadata["dolt_server_port"] = correctPort

	// Write back
	newData, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, newData, 0644)
}
