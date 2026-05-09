package doctor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/doltserver"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/workspace"
)

// RigBeadsCheck verifies that rig identity beads exist for all rigs.
// Rig identity beads track rig metadata like git URL, prefix, and operational state.
// They are created by gt rig add (see gt-zmznh) but may be missing for legacy rigs.
type RigBeadsCheck struct {
	FixableCheck
}

// NewRigBeadsCheck creates a new rig identity beads check.
func NewRigBeadsCheck() *RigBeadsCheck {
	return &RigBeadsCheck{
		FixableCheck: FixableCheck{
			BaseCheck: BaseCheck{
				CheckName:        "rig-beads-exist",
				CheckDescription: "Verify rig identity beads exist for all rigs",
				CheckCategory:    CategoryRig,
			},
		},
	}
}

// Run checks if rig identity beads exist for all rigs.
func (c *RigBeadsCheck) Run(ctx *CheckContext) *CheckResult {
	// Load routes to get rig info
	townBeadsDir := filepath.Join(ctx.TownRoot, ".beads")
	routes, err := beads.LoadRoutes(townBeadsDir)
	if err != nil {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusWarning,
			Message: "Could not load routes.jsonl",
		}
	}

	// Build unique rig list from routes
	// Routes have format: prefix "gt-" -> path "gastown/mayor/rig"
	rigSet := make(map[string]struct {
		prefix    string
		beadsPath string
	})
	for _, r := range routes {
		// Extract rig name from path (first component)
		rigName := r.Path
		if rigName == "." {
			// For town root, try to get the actual town name from town.json
			if name, err := workspace.GetTownName(ctx.TownRoot); err == nil {
				rigName = name
			} else {
				rigName = "town" // Fallback
			}
		} else {
			parts := strings.Split(r.Path, "/")
			if len(parts) >= 1 && parts[0] != "." && parts[0] != ".." {
				rigName = parts[0]
			} else {
				continue // Skip ".." or malformed paths
			}
		}

		// Validate that the rig directory actually exists (except for town root "." which we already resolved)
		if r.Path != "." {
			rigPath := filepath.Join(ctx.TownRoot, rigName)
			if info, err := os.Stat(rigPath); err != nil || !info.IsDir() {
				continue
			}
		}

		if rigName != "" {
			prefix := strings.TrimSuffix(r.Prefix, "-")
			// Exclude town-level prefix "hq" from rig identity checks.
			if prefix == "hq" {
				continue
			}
			if _, exists := rigSet[rigName]; !exists {
				rigSet[rigName] = struct {
					prefix    string
					beadsPath string
				}{
					prefix:    prefix,
					beadsPath: r.Path,
				}
			}
		}
	}

	if len(rigSet) == 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: "No rigs to check",
		}
	}

	var missing []string
	var checked int

	// Check each rig for its identity bead
	for rigName, info := range rigSet {
		rigBeadsPath := filepath.Join(ctx.TownRoot, info.beadsPath)
		bd := beads.New(rigBeadsPath)

		rigBeadID := beads.RigBeadIDWithPrefix(info.prefix, rigName)
		if _, err := bd.Show(rigBeadID); err != nil {
			missing = append(missing, rigBeadID)
		}
		checked++
	}

	if len(missing) == 0 {
		return &CheckResult{
			Name:    c.Name(),
			Status:  StatusOK,
			Message: fmt.Sprintf("All %d rig identity beads exist", checked),
		}
	}

	return &CheckResult{
		Name:    c.Name(),
		Status:  StatusError,
		Message: fmt.Sprintf("%d rig identity bead(s) missing", len(missing)),
		Details: missing,
		FixHint: "Run 'gt doctor --fix' to create missing rig identity beads",
	}
}

// Fix creates missing rig identity beads.
// Fix registers the missing rig identity beads.
func (c *RigBeadsCheck) Fix(ctx *CheckContext) error {
	// Load routes to get rig info
	townBeadsDir := filepath.Join(ctx.TownRoot, ".beads")
	routes, err := beads.LoadRoutes(townBeadsDir)
	if err != nil {
		return fmt.Errorf("loading routes.jsonl: %w", err)
	}

	// Build unique rig list from routes
	rigSet := make(map[string]struct {
		prefix    string
		beadsPath string
	})
	for _, r := range routes {
		rigName := r.Path
		if rigName == "." {
			if name, err := workspace.GetTownName(ctx.TownRoot); err == nil {
				rigName = name
			} else {
				rigName = "hq"
			}
		} else {
			parts := strings.Split(r.Path, "/")
			if len(parts) >= 1 && parts[0] != "." && parts[0] != ".." {
				rigName = parts[0]
			} else {
				continue
			}
		}

		// Validate rig directory
		if r.Path != "." {
			rigPath := filepath.Join(ctx.TownRoot, rigName)
			if info, err := os.Stat(rigPath); err != nil || !info.IsDir() {
				continue
			}
		}

		if rigName != "" {
			prefix := strings.TrimSuffix(r.Prefix, "-")
			// Exclude town-level prefix "hq"
			if prefix == "hq" {
				continue
			}
			if _, exists := rigSet[rigName]; !exists {
				rigSet[rigName] = struct {
					prefix    string
					beadsPath string
				}{
					prefix:    prefix,
					beadsPath: r.Path,
				}
			}
		}
	}

	if len(rigSet) == 0 {
		return nil
	}

	var errs []error
	for rigName, info := range rigSet {
		// Ensure metadata is correct before running ANY bd commands (gt-zmy)
		metadataRigName := rigName
		if info.beadsPath == "." {
			metadataRigName = "hq"
		}
		_ = doltserver.EnsureMetadata(ctx.TownRoot, metadataRigName)

		rigBeadsPath := filepath.Join(ctx.TownRoot, info.beadsPath)
		bd := beads.New(rigBeadsPath)
		bd.Verbose = ctx.Verbose

		// Try to get git URL from rig config
		rigPath := filepath.Join(ctx.TownRoot, rigName)
		if info.beadsPath == "." {
			rigPath = ctx.TownRoot
		}
		gitURL := ""
		if cfg, err := rig.LoadRigConfig(rigPath); err == nil {
			gitURL = cfg.GitURL
		}

		fields := &beads.RigFields{
			Repo:   gitURL,
			Prefix: info.prefix,
			State:  beads.RigStateActive,
		}

		rigBeadID := beads.RigBeadIDWithPrefix(info.prefix, rigName)
		if _, err := bd.EnsureRigBead(rigName, fields); err != nil {
			errs = append(errs, fmt.Errorf("ensuring %s: %w", rigBeadID, err))
		}
	}

	return errors.Join(errs...)
}
