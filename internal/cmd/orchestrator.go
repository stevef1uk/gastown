package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/orchestrator"
	"github.com/steveyegge/gastown/internal/polecat"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/workspace"
	"io"
	"path/filepath"
)
var orchestratorCmd = &cobra.Command{
	Use:     "orchestrator",
	Aliases: []string{"orch"},
	Short:   "Manage the MCP orchestrator service",
}

var orchestratorRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the orchestrator service (foreground)",
	RunE: func(cmd *cobra.Command, args []string) error {
		townRoot, err := workspace.FindFromCwdOrError()
		if err != nil {
			return err
		}

		mgr := orchestrator.NewManager(townRoot)
		// Load templates from townRoot/orchestrator/templates
		tmplDir := filepath.Join(townRoot, "orchestrator", "templates")
		if err := mgr.LoadTemplatesFromDir(tmplDir); err != nil {
			fmt.Printf("Warning: failed to load templates: %v\n", err)
		}

		server := orchestrator.NewServer(mgr)
		
		// [TODO] Get NATS URL from config
		natsURL := "nats://localhost:4222"
		
		fmt.Printf("Orchestrator listening on NATS: %s\n", natsURL)
		if err := server.ListenNATS(natsURL); err != nil {
			return fmt.Errorf("starting NATS listener: %w", err)
		}

		// Also serve on stdio for MCP clients (this is blocking)
		// If stdio is not a TTY and we are in background, we still want to keep the process alive for NATS.
		err = server.Serve()
		if err != nil && err != io.EOF {
			return err
		}
		
		// If Serve returned (e.g. EOF on stdin), keep alive for NATS if it was started
		select {}
	},
}

var orchestratorStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the orchestrator service",
	RunE: func(cmd *cobra.Command, args []string) error {
		townRoot, err := workspace.FindFromCwdOrError()
		if err != nil {
			return err
		}
		if err := orchestrator.Start(townRoot); err != nil {
			return err
		}
		fmt.Printf("%s Orchestrator started\n", style.SuccessPrefix)
		return nil
	},
}

var orchestratorStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the orchestrator service",
	RunE: func(cmd *cobra.Command, args []string) error {
		townRoot, err := workspace.FindFromCwdOrError()
		if err != nil {
			return err
		}
		if err := orchestrator.Stop(townRoot); err != nil {
			return err
		}
		fmt.Printf("%s Orchestrator stopped\n", style.SuccessPrefix)
		return nil
	},
}

var orchestratorSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Install orchestrator templates and prompts into the town",
	Long: `Copy workflow FSM templates and per-state prompts from the gastown binary
into {townRoot}/orchestrator/. Source of truth lives in the gastown repo under
internal/orchestrator/town/ and is embedded at build time.

Use after editing templates in gastown (make install runs this automatically).`,
	RunE: runOrchestratorSync,
}

var orchestratorSyncUpdate bool
var orchestratorSyncTownRoot string

var orchestratorStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Check orchestrator status",
	RunE: func(cmd *cobra.Command, args []string) error {
		townRoot, err := workspace.FindFromCwdOrError()
		if err != nil {
			return err
		}
		running, pid, _ := orchestrator.IsRunning(townRoot)
		if running {
			fmt.Printf("%s Orchestrator is running (PID %d)\n", style.Bold.Render("●"), pid)
		} else {
			fmt.Printf("%s Orchestrator is not running\n", style.Dim.Render("○"))
		}
		return nil
	},
}

func runOrchestratorSync(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return err
	}
	if orchestratorSyncTownRoot != "" {
		townRoot = orchestratorSyncTownRoot
	}
	res, err := orchestrator.SyncTownAssets(townRoot, orchestratorSyncUpdate)
	if err != nil {
		return err
	}
	fmt.Printf("%s Orchestrator assets → %s/orchestrator (%d added, %d updated)\n",
		style.SuccessPrefix, townRoot, res.Added, res.Updated)

	for _, rigName := range discoverRigs(townRoot) {
		rigPath := filepath.Join(townRoot, rigName)
		if n, err := polecat.RepairIdentityFiles(rigPath, rigName); err != nil {
			fmt.Printf("%s polecat identity repair (%s): %v\n", style.Warning.Render("!"), rigName, err)
		} else if n > 0 {
			fmt.Printf("  Repaired .gt-agent for %d polecat(s) on %s\n", n, rigName)
		}
	}
	return nil
}

func init() {
	orchestratorSyncCmd.Flags().BoolVar(&orchestratorSyncUpdate, "update-changed", false, "Overwrite town files that differ from embedded source")
	orchestratorSyncCmd.Flags().StringVar(&orchestratorSyncTownRoot, "town-root", "", "Town root (default: workspace from cwd)")
	orchestratorCmd.AddCommand(orchestratorStartCmd)
	orchestratorCmd.AddCommand(orchestratorStopCmd)
	orchestratorCmd.AddCommand(orchestratorStatusCmd)
	orchestratorCmd.AddCommand(orchestratorRunCmd)
	orchestratorCmd.AddCommand(orchestratorSyncCmd)
	rootCmd.AddCommand(orchestratorCmd)
}
