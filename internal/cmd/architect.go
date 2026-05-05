package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/workspace"
)

var architectCmd = &cobra.Command{
	Use:     "architect",
	GroupID: GroupAgents,
	Short:   "Manage the Rig Architect (design and structure agent)",
	RunE:    requireSubcommand,
	Long: `Manage the Rig Architect - the design and structure agent for a rig.

The Architect:
  - Defines technical design and project structure
  - Answers architectural questions from Polecats and QA
  - Ensures code follows established patterns and standards
  - Reviews complex changes for architectural integrity

One Architect per rig. The Witness monitors the Architect's health.`,
}

var architectStartCmd = &cobra.Command{
	Use:   "start <rig>",
	Short: "Start the architect",
	Long:  `Start the Architect agent for a specific rig.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runArchitectStart,
}

var architectStopCmd = &cobra.Command{
	Use:   "stop <rig>",
	Short: "Stop the architect",
	Long:  `Stop the running Architect agent for a rig.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runArchitectStop,
}

var architectAttachCmd = &cobra.Command{
	Use:     "attach [rig]",
	Aliases: []string{"at"},
	Short:   "Attach to architect session",
	Long:    `Attach to the Architect tmux session for a rig. Detach with Ctrl-B D.`,
	Args:    cobra.MaximumNArgs(1),
	RunE:    runArchitectAttach,
}

func init() {
	architectCmd.AddCommand(architectStartCmd)
	architectCmd.AddCommand(architectStopCmd)
	architectCmd.AddCommand(architectAttachCmd)
	rootCmd.AddCommand(architectCmd)
}

func runArchitectStart(cmd *cobra.Command, args []string) error {
	rigName := args[0]
	_, r, err := getRig(rigName)
	if err != nil {
		return err
	}

	fmt.Printf("Starting architect for %s...\n", rigName)
	result := upStartArchitect(rigName, r)
	if !result.ok {
		return fmt.Errorf("starting architect: %s", result.detail)
	}

	if result.detail == session.ArchitectSessionName(session.PrefixFor(rigName)) {
		fmt.Printf("%s Architect is already running for %s\n", style.Dim.Render("⚠"), rigName)
	} else {
		fmt.Printf("%s Architect started for %s\n", style.Bold.Render("✓"), rigName)
	}
	fmt.Printf("  %s\n", style.Dim.Render("Use 'gt architect attach " + rigName + "' to connect"))
	return nil
}

func runArchitectStop(cmd *cobra.Command, args []string) error {
	rigName := args[0]
	townRoot, _, err := getRig(rigName)
	if err != nil {
		return err
	}

	sessionID := session.ArchitectSessionName(session.PrefixFor(rigName))
	sp := session.GetDefaultProvider(townRoot)
	ctx := context.Background()

	running, _ := sp.Exists(ctx, sessionID)
	if !running {
		fmt.Printf("%s Architect is not running for %s\n", style.Dim.Render("⚠"), rigName)
		return nil
	}

	if err := sp.Stop(ctx, sessionID, true); err != nil {
		return fmt.Errorf("stopping architect: %w", err)
	}

	fmt.Printf("%s Architect stopped for %s\n", style.Bold.Render("✓"), rigName)
	return nil
}

func runArchitectAttach(cmd *cobra.Command, args []string) error {
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
			return fmt.Errorf("could not determine rig: %w\nUsage: gt architect attach <rig>", err)
		}
	}

	_, _, err = getRig(rigName)
	if err != nil {
		return err
	}

	sessionID := session.ArchitectSessionName(session.PrefixFor(rigName))
	sp := session.GetDefaultProvider(townRoot)
	ctx := context.Background()

	if running, _ := sp.Exists(ctx, sessionID); !running {
		if err := runArchitectStart(cmd, []string{rigName}); err != nil {
			return err
		}
	}

	return attachToTmuxSession(sessionID)
}
