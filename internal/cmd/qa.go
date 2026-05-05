package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/workspace"
)

var qaCmd = &cobra.Command{
	Use:     "qa",
	GroupID: GroupAgents,
	Short:   "Manage the Rig QA (quality assurance agent)",
	RunE:    requireSubcommand,
	Long: `Manage the Rig QA - the quality assurance agent for a rig.

The QA Agent:
  - Validates changes against requirements and design
  - Runs and manages the test suite
  - Performs regression testing and sanity checks
  - Blocks merges that don't meet quality bars

One QA Agent per rig. The Witness monitors the QA Agent's health.`,
}

var qaStartCmd = &cobra.Command{
	Use:   "start <rig>",
	Short: "Start the QA agent",
	Long:  `Start the QA agent for a specific rig.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runQAStart,
}

var qaStopCmd = &cobra.Command{
	Use:   "stop <rig>",
	Short: "Stop the QA agent",
	Long:  `Stop the running QA agent for a rig.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runQAStop,
}

var qaAttachCmd = &cobra.Command{
	Use:     "attach [rig]",
	Aliases: []string{"at"},
	Short:   "Attach to QA session",
	Long:    `Attach to the QA tmux session for a rig. Detach with Ctrl-B D.`,
	Args:    cobra.MaximumNArgs(1),
	RunE:    runQAAttach,
}

func init() {
	qaCmd.AddCommand(qaStartCmd)
	qaCmd.AddCommand(qaStopCmd)
	qaCmd.AddCommand(qaAttachCmd)
	rootCmd.AddCommand(qaCmd)
}

func runQAStart(cmd *cobra.Command, args []string) error {
	rigName := args[0]
	_, r, err := getRig(rigName)
	if err != nil {
		return err
	}

	fmt.Printf("Starting QA for %s...\n", rigName)
	result := upStartQA(rigName, r)
	if !result.ok {
		return fmt.Errorf("starting QA: %s", result.detail)
	}

	if result.detail == session.QASessionName(session.PrefixFor(rigName)) {
		fmt.Printf("%s QA is already running for %s\n", style.Dim.Render("⚠"), rigName)
	} else {
		fmt.Printf("%s QA started for %s\n", style.Bold.Render("✓"), rigName)
	}
	fmt.Printf("  %s\n", style.Dim.Render("Use 'gt qa attach " + rigName + "' to connect"))
	return nil
}

func runQAStop(cmd *cobra.Command, args []string) error {
	rigName := args[0]
	townRoot, _, err := getRig(rigName)
	if err != nil {
		return err
	}

	sessionID := session.QASessionName(session.PrefixFor(rigName))
	sp := session.GetDefaultProvider(townRoot)
	ctx := context.Background()

	running, _ := sp.Exists(ctx, sessionID)
	if !running {
		fmt.Printf("%s QA is not running for %s\n", style.Dim.Render("⚠"), rigName)
		return nil
	}

	if err := sp.Stop(ctx, sessionID, true); err != nil {
		return fmt.Errorf("stopping QA: %w", err)
	}

	fmt.Printf("%s QA stopped for %s\n", style.Bold.Render("✓"), rigName)
	return nil
}

func runQAAttach(cmd *cobra.Command, args []string) error {
	rigName := ""
	if len(args) > 0 {
		rigName = args[0]
	}

	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	if rigName == "" {
		rigName, err = inferRigFromCwd(townRoot)
		if err != nil {
			return fmt.Errorf("could not determine rig: %w\nUsage: gt qa attach <rig>", err)
		}
	}

	_, _, err = getRig(rigName)
	if err != nil {
		return err
	}

	sessionID := session.QASessionName(session.PrefixFor(rigName))
	sp := session.GetDefaultProvider(townRoot)
	ctx := context.Background()

	if running, _ := sp.Exists(ctx, sessionID); !running {
		if err := runQAStart(cmd, []string{rigName}); err != nil {
			return err
		}
	}

	return attachToTmuxSession(sessionID)
}
