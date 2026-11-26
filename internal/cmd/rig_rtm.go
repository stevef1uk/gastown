package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/orchestrator"
	"github.com/steveyegge/gastown/internal/rtm"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/workspace"
)

var (
	rigRTMJSON     bool
	rigRTMMarkdown bool
	rigRTMHTML     bool
	rigRTMWeb      bool
	rigRTMOutput   string
	rigRTMAudit    bool
	rigRTMTown     string
	rigRTMSummary  bool
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
	rigRTMCmd.Flags().BoolVar(&rigRTMHTML, "html", false, "Output interactive HTML report")
	rigRTMCmd.Flags().BoolVarP(&rigRTMWeb, "web", "w", false, "Generate and save interactive HTML report to <rig>/mayor/rig/rtm.html")
	rigRTMCmd.Flags().StringVarP(&rigRTMOutput, "output", "o", "", "Write report to file (.md or .html)")
	rigRTMCmd.Flags().BoolVar(&rigRTMAudit, "audit", false, "Exit non-zero if any requirement has gaps or test failures")
	rigRTMCmd.Flags().BoolVarP(&rigRTMSummary, "summary", "s", false, "Executive summary view: show requirements matrix and delivery counts without full bead breakdown")

	rigCmd.AddCommand(rigRTMCmd)

	mayorWorkflowRTMCmd.Flags().BoolVar(&rigRTMJSON, "json", false, "Output as JSON")
	mayorWorkflowRTMCmd.Flags().BoolVarP(&rigRTMMarkdown, "markdown", "m", false, "Output as Markdown")
	mayorWorkflowRTMCmd.Flags().BoolVar(&rigRTMHTML, "html", false, "Output interactive HTML report")
	mayorWorkflowRTMCmd.Flags().BoolVarP(&rigRTMWeb, "web", "w", false, "Generate and save interactive HTML report to <rig>/mayor/rig/rtm.html")
	mayorWorkflowRTMCmd.Flags().StringVarP(&rigRTMOutput, "output", "o", "", "Write report to file (.md or .html)")
	mayorWorkflowRTMCmd.Flags().BoolVar(&rigRTMAudit, "audit", false, "Exit non-zero if any requirement has gaps or test failures")
	mayorWorkflowRTMCmd.Flags().BoolVarP(&rigRTMSummary, "summary", "s", false, "Executive summary view: show requirements matrix and delivery counts without full bead breakdown")

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

	if rigRTMWeb {
		targetPath := filepath.Join(townRoot, rigName, "mayor", "rig", "rtm.html")
		htmlContent := report.HTML()
		if err := os.WriteFile(targetPath, []byte(htmlContent), 0644); err != nil {
			return fmt.Errorf("write interactive HTML report to %s: %w", targetPath, err)
		}
		fmt.Printf("%s Wrote interactive HTML report to %s\n", style.Success.Render("✓"), targetPath)
		fmt.Printf("  Open in browser: file://%s\n\n", targetPath)
		if !rigRTMHTML && !rigRTMJSON && !rigRTMMarkdown && rigRTMOutput == "" {
			fmt.Print(report.RenderTerminal(rigRTMSummary))
		}
		return nil
	}

	if rigRTMOutput != "" {
		targetPath := rigRTMOutput
		if !filepath.IsAbs(targetPath) {
			targetPath = filepath.Join(townRoot, rigName, "mayor", "rig", targetPath)
		}
		var content []byte
		isHTML := strings.HasSuffix(strings.ToLower(targetPath), ".html") || strings.HasSuffix(strings.ToLower(targetPath), ".htm") || rigRTMHTML
		if isHTML {
			content = []byte(report.HTML())
		} else {
			content = []byte(report.Markdown())
		}
		if err := os.WriteFile(targetPath, content, 0644); err != nil {
			return fmt.Errorf("write traceability report to %s: %w", targetPath, err)
		}
		fmt.Printf("%s Wrote traceability report to %s\n", style.Success.Render("✓"), targetPath)
		if isHTML {
			fmt.Printf("  Open in browser: file://%s\n", targetPath)
		}
	}

	if rigRTMJSON {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
	} else if rigRTMHTML && rigRTMOutput == "" {
		fmt.Println(report.HTML())
	} else if rigRTMMarkdown {
		fmt.Println(report.Markdown())
	} else if rigRTMOutput == "" {
		fmt.Print(report.RenderTerminal(rigRTMSummary))
	}

	if rigRTMAudit && report.Failures > 0 {
		return fmt.Errorf("RTM audit failed: %d requirement(s) have test gaps or missing files", report.Failures)
	}

	return nil
}
