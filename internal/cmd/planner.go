package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/style"
)

var plannerCmd = &cobra.Command{
	Use:     "planner",
	GroupID: GroupAgents,
	Short:   "Manage the Planner (rig-level work planning agent)",
	RunE:    requireSubcommand,
	Long: `Manage the Planner - the rig-level work planning agent.

The Planner is a rig-level agent that:
  - Breaks down high-level objectives into specific tasks
  - Manages the roadmap and project timeline
  - Coordinates between the Mayor and other Rig-level agents
  - Refines task descriptions and acceptance criteria

One Planner per rig. The Deacon monitors the Planner's health.`,
}

var plannerStartCmd = &cobra.Command{
	Use:   "start <rig>",
	Short: "Start the planner for a rig",
	Long:  `Start the Planner agent for a specific rig.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runPlannerStart,
}

var plannerStopCmd = &cobra.Command{
	Use:   "stop <rig>",
	Short: "Stop the planner for a rig",
	Long:  `Stop the running Planner agent for a specific rig.`,
	Args:  cobra.ExactArgs(1),
	RunE:  runPlannerStop,
}

var plannerAttachCmd = &cobra.Command{
	Use:     "attach <rig>",
	Aliases: []string{"at"},
	Short:   "Attach to planner session for a rig",
	Long:    `Attach to the Planner tmux session for a specific rig. Detach with Ctrl-B D.`,
	Args:    cobra.ExactArgs(1),
	RunE:    runPlannerAttach,
}

func init() {
	plannerCmd.AddCommand(plannerStartCmd)
	plannerCmd.AddCommand(plannerStopCmd)
	plannerCmd.AddCommand(plannerAttachCmd)
	rootCmd.AddCommand(plannerCmd)
}

func runPlannerStart(cmd *cobra.Command, args []string) error {
	rigName := args[0]
	_, r, err := getRig(rigName)
	if err != nil {
		return err
	}

	fmt.Printf("Starting planner for %s...\n", rigName)
	result := upStartPlanner(rigName, r)
	if !result.ok {
		return fmt.Errorf("starting planner: %s", result.detail)
	}

	if result.detail == session.PlannerSessionName(session.PrefixFor(rigName), rigName) {
		fmt.Printf("%s Planner is already running for %s\n", style.Dim.Render("⚠"), rigName)
	} else {
		fmt.Printf("%s Planner started for %s\n", style.Bold.Render("✓"), rigName)
	}
	fmt.Printf("  %s\n", style.Dim.Render("Use 'gt planner attach "+rigName+"' to connect"))
	return nil
}

func runPlannerStop(cmd *cobra.Command, args []string) error {
	rigName := args[0]
	townRoot, _, err := getRig(rigName)
	if err != nil {
		return err
	}

	sessionID := session.PlannerSessionName(session.PrefixFor(rigName), rigName)
	sp := session.GetDefaultProvider(townRoot)
	ctx := context.Background()

	running, _ := sp.Exists(ctx, sessionID)
	if !running {
		fmt.Printf("%s Planner is not running for %s\n", style.Dim.Render("⚠"), rigName)
		return nil
	}

	if err := sp.Stop(ctx, sessionID, true); err != nil {
		return fmt.Errorf("stopping planner: %w", err)
	}

	fmt.Printf("%s Planner stopped for %s\n", style.Bold.Render("✓"), rigName)
	return nil
}

func runPlannerAttach(cmd *cobra.Command, args []string) error {
	rigName := args[0]
	townRoot, _, err := getRig(rigName)
	if err != nil {
		return err
	}

	sessionID := session.PlannerSessionName(session.PrefixFor(rigName), rigName)
	sp := session.GetDefaultProvider(townRoot)
	ctx := context.Background()

	if running, _ := sp.Exists(ctx, sessionID); !running {
		if err := runPlannerStart(cmd, args); err != nil {
			return err
		}
	}

	return attachToTmuxSession(sessionID)
}
