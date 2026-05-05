package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/workspace"
)

var plannerCmd = &cobra.Command{
	Use:     "planner",
	GroupID: GroupAgents,
	Short:   "Manage the Town Planner (global work planning agent)",
	RunE:    requireSubcommand,
	Long: `Manage the Town Planner - the global work planning agent.

The Planner is a town-level agent that:
  - Breaks down high-level objectives into specific tasks
  - Manages the roadmap and project timeline
  - Coordinates between the Mayor and Rig-level agents
  - Refines task descriptions and acceptance criteria

One Planner per town. The Deacon monitors the Planner's health.`,
}

var plannerStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the planner",
	Long:  `Start the global Town Planner agent.`,
	RunE:  runPlannerStart,
}

var plannerStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the planner",
	Long:  `Stop the running Town Planner agent.`,
	RunE:  runPlannerStop,
}

var plannerAttachCmd = &cobra.Command{
	Use:     "attach",
	Aliases: []string{"at"},
	Short:   "Attach to planner session",
	Long:    `Attach to the Town Planner tmux session. Detach with Ctrl-B D.`,
	RunE:    runPlannerAttach,
}

func init() {
	plannerCmd.AddCommand(plannerStartCmd)
	plannerCmd.AddCommand(plannerStopCmd)
	plannerCmd.AddCommand(plannerAttachCmd)
	rootCmd.AddCommand(plannerCmd)
}

func runPlannerStart(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	fmt.Println("Starting planner...")
	result := upStartPlanner(townRoot)
	if !result.ok {
		return fmt.Errorf("starting planner: %s", result.detail)
	}

	if result.detail == session.PlannerSessionName() {
		fmt.Printf("%s Planner is already running\n", style.Dim.Render("⚠"))
	} else {
		fmt.Printf("%s Planner started\n", style.Bold.Render("✓"))
	}
	fmt.Printf("  %s\n", style.Dim.Render("Use 'gt planner attach' to connect"))
	return nil
}

func runPlannerStop(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	sessionID := session.PlannerSessionName()
	sp := session.GetDefaultProvider(townRoot)
	ctx := context.Background()

	running, _ := sp.Exists(ctx, sessionID)
	if !running {
		fmt.Printf("%s Planner is not running\n", style.Dim.Render("⚠"))
		return nil
	}

	if err := sp.Stop(ctx, sessionID, true); err != nil {
		return fmt.Errorf("stopping planner: %w", err)
	}

	fmt.Printf("%s Planner stopped\n", style.Bold.Render("✓"))
	return nil
}

func runPlannerAttach(cmd *cobra.Command, args []string) error {
	townRoot, err := workspace.FindFromCwdOrError()
	if err != nil {
		return fmt.Errorf("not in a Gas Town workspace: %w", err)
	}

	sessionID := session.PlannerSessionName()
	sp := session.GetDefaultProvider(townRoot)
	ctx := context.Background()

	if running, _ := sp.Exists(ctx, sessionID); !running {
		if err := runPlannerStart(cmd, nil); err != nil {
			return err
		}
	}

	return attachToTmuxSession(sessionID)
}
