package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/townconfig"
	"github.com/steveyegge/gastown/internal/workspace"
)

var configSyncTownRoot string

var configSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Sync embedded orchestrator and formula config into the town",
	Long: `Refresh runtime config under a Gas Town HQ from the installed gt binary.

Copies embedded orchestrator templates and prompts to {town}/orchestrator/
(overwriting files that differ from source). Updates .beads/formulas/ using
the same rules as gt upgrade (skips formulas you modified).

Used automatically by make install. Run manually after pulling template or
prompt changes without rebuilding:

  gt config sync
  gt config sync --town-root ~/gt`,
	RunE: runConfigSync,
}

func runConfigSync(cmd *cobra.Command, args []string) error {
	var townRoot string
	var err error
	if configSyncTownRoot != "" {
		townRoot = configSyncTownRoot
	} else {
		townRoot, err = workspace.FindFromCwdOrError()
		if err != nil {
			return err
		}
	}

	res, err := townconfig.Sync(townRoot)
	if err != nil {
		return err
	}

	fmt.Printf("%s Town config synced → %s\n", style.SuccessPrefix, townRoot)
	fmt.Printf("  orchestrator: %d added, %d updated\n",
		res.Orchestrator.Added, res.Orchestrator.Updated)
	f := res.Formulas
	if f.Updated+f.Reinstalled+f.Skipped > 0 {
		fmt.Printf("  formulas: %d updated, %d reinstalled, %d skipped (user-modified)\n",
			f.Updated, f.Reinstalled, f.Skipped)
	} else {
		fmt.Printf("  formulas: up to date\n")
	}
	return nil
}

func init() {
	configSyncCmd.Flags().StringVar(&configSyncTownRoot, "town-root", "", "Town root (default: workspace from cwd)")
	configCmd.AddCommand(configSyncCmd)
}
