package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/orchestrator"
	"github.com/steveyegge/gastown/internal/rtm"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/workspace"
)

var (
	rigRTMJSON     bool
	rigRTMMarkdown bool
	rigRTMOutput   string
	rigRTMAudit    bool
	rigRTMTown     string
)

var rigRTMCmd = &cobra.Command{
	Use:   "rtm <rig>",
	Short: "Show requirements traceability matrix (SPEC → Beads → Tests)",
	Long: `Correlates requirements from SPEC.md and TEST_PLAN.md with implement beads
and planned test files on disk to produce a Requirements Traceability Matrix report.

Flags:
  --json           Output raw JSON
  --markdown, -m   Output GitHub-flavored Markdown to stdout
  --output, -o     Write Markdown report to file (defaults to <rig>/mayor/rig/TRACEABILITY.md)
  --audit          Audit mode: exit code 1 if any requirement has missing tests or gaps

Examples:
  gt rig rtm testgt4
  gt rig rtm testgt4 --markdown
  gt rig rtm testgt4 -o TRACEABILITY.md
  gt rig rtm testgt4 --audit`,
	Args: cobra.ExactArgs(1),
	RunE: runRigRTM,
}

var mayorWorkflowRTMCmd = &cobra.Command{
	Use:   "rtm <workflow-id>",
	Short: "Show requirements traceability matrix for a workflow's rig",
	Args:  cobra.ExactArgs(1),
	RunE:  runMayorWorkflowRTM,
}

func init() {
	rigRTMCmd.Flags().StringVar(&rigRTMTown, "town", "", "Town root (default: cwd walk-up or GT_TOWN_ROOT)")
	rigRTMCmd.Flags().BoolVar(&rigRTMJSON, "json", false, "Output as JSON")
	rigRTMCmd.Flags().BoolVarP(&rigRTMMarkdown, "markdown", "m", false, "Output as Markdown")
	rigRTMCmd.Flags().StringVarP(&rigRTMOutput, "output", "o", "", "Write Markdown report to file")
	rigRTMCmd.Flags().BoolVar(&rigRTMAudit, "audit", false, "Exit non-zero if any requirement has gaps or test failures")

	rigCmd.AddCommand(rigRTMCmd)

	mayorWorkflowRTMCmd.Flags().BoolVar(&rigRTMJSON, "json", false, "Output as JSON")
	mayorWorkflowRTMCmd.Flags().BoolVarP(&rigRTMMarkdown, "markdown", "m", false, "Output as Markdown")
	mayorWorkflowRTMCmd.Flags().StringVarP(&rigRTMOutput, "output", "o", "", "Write Markdown report to file")
	mayorWorkflowRTMCmd.Flags().BoolVar(&rigRTMAudit, "audit", false, "Exit non-zero if any requirement has gaps or test failures")

	mayorWorkflowCmd.AddCommand(mayorWorkflowRTMCmd)
}

func runRigRTM(_ *cobra.Command, args []string) error {
	rigName := args[0]
	townRoot, err := resolveTownRootForRTM()
	if err != nil {
		return err
	}

	return generateAndOutputRTM(townRoot, rigName)
}

func runMayorWorkflowRTM(_ *cobra.Command, args []string) error {
	workflowID := args[0]
	townRoot, err := resolveTownRootForRTM()
	if err != nil {
		return err
	}

	workflows, err := orchestrator.NewManager(townRoot).GetWorkflowStatus(workflowID)
	if err != nil {
		return fmt.Errorf("lookup workflow %s: %w", workflowID, err)
	}
	if len(workflows) == 0 {
		return fmt.Errorf("workflow %q not found", workflowID)
	}

	rigName := workflows[0].Variables["rig"]
	if rigName == "" {
		return fmt.Errorf("workflow %q has no associated rig", workflowID)
	}

	return generateAndOutputRTM(townRoot, rigName)
}

func resolveTownRootForRTM() (string, error) {
	if rigRTMTown != "" {
		return rigRTMTown, nil
	}
	if workflowTownRoot != "" {
		return workflowTownRoot, nil
	}
	root, err := workspace.FindFromCwdOrError()
	if err != nil {
		return "", fmt.Errorf("not in a Gas Town workspace: %w", err)
	}
	return root, nil
}

func generateAndOutputRTM(townRoot, rigName string) error {
	report, err := rtm.GenerateRTM(townRoot, rigName)
	if err != nil {
		return err
	}

	if rigRTMOutput != "" {
		targetPath := rigRTMOutput
		if !filepath.IsAbs(targetPath) {
			targetPath = filepath.Join(townRoot, rigName, "mayor", "rig", targetPath)
		}
		md := report.Markdown()
		if err := os.WriteFile(targetPath, []byte(md), 0644); err != nil {
			return fmt.Errorf("write traceability report to %s: %w", targetPath, err)
		}
		fmt.Printf("%s Wrote traceability report to %s\n", style.Success.Render("✓"), targetPath)
	}

	if rigRTMJSON {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
	} else if rigRTMMarkdown {
		fmt.Println(report.Markdown())
	} else if rigRTMOutput == "" {
		fmt.Print(report.RenderTerminal())
	}

	if rigRTMAudit && report.Failures > 0 {
		return fmt.Errorf("RTM audit failed: %d requirement(s) have test gaps or missing files", report.Failures)
	}

	return nil
}
