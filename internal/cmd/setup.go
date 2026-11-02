package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/workspace"
)

var setupCmd = &cobra.Command{
	Use:     "setup",
	GroupID: GroupAgents,
	Short:   "Manage the town project-setup agent",
	RunE:    requireSubcommand,
}

var setupStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the project-setup agent",
	Args:  cobra.NoArgs,
	RunE:  runSetupStart,
}

var setupStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the project-setup agent",
	Args:  cobra.NoArgs,
	RunE:  runSetupStop,
}

var setupRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart project setup with the current agent configuration",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := runSetupStop(cmd, args); err != nil {
			return err
		}
		return runSetupStart(cmd, args)
	},
}

func init() {
	setupCmd.AddCommand(setupStartCmd, setupStopCmd, setupRestartCmd)
	rootCmd.AddCommand(setupCmd)
}

func runSetupStart(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return err
	}
	result := upStartSetup(townRoot)
	if !result.ok {
		return fmt.Errorf("starting setup: %s", result.detail)
	}
	fmt.Printf("Setup running: %s\n", result.detail)
	return nil
}

func runSetupStop(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return err
	}
	sp := session.GetDefaultProvider(townRoot)
	if err := sp.Stop(cmd.Context(), session.SetupSessionName(), true); err != nil {
		return fmt.Errorf("stopping setup: %w", err)
	}
	fmt.Println("Setup stopped")
	return nil
}
