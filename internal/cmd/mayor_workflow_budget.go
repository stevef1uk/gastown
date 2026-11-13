package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gastown/internal/orchestrator"
	"github.com/steveyegge/gastown/internal/style"
)

var (
	workflowBudgetTokens    int64
	workflowBudgetUSD       float64
	workflowBudgetProxyPath string
)

var mayorWorkflowBudgetCmd = &cobra.Command{
	Use:   "budget <workflow-id>",
	Short: "Set per-workflow token and dollar budget overrides",
	Long: `Set one or both limits for a workflow without changing defaults for other workflows.

Examples:
  gt mayor workflow budget wf-1 --tokens 40000000
  gt mayor workflow budget wf-1 --usd 10
  gt mayor workflow budget wf-1 --tokens 40000000 --usd 10`,
	Args: cobra.ExactArgs(1),
	RunE: runMayorWorkflowBudget,
}

func init() {
	mayorWorkflowCmd.AddCommand(mayorWorkflowBudgetCmd)
	mayorWorkflowBudgetCmd.Flags().Int64Var(&workflowBudgetTokens, "tokens", 0, "Override this workflow's reserved-token limit")
	mayorWorkflowBudgetCmd.Flags().Float64Var(&workflowBudgetUSD, "usd", 0, "Override this workflow's dollar allowance")
	mayorWorkflowBudgetCmd.Flags().StringVar(&workflowBudgetProxyPath, "freeride-budget-path", "", "Freeride execution budget ledger path (defaults to FREERIDE_EXECUTION_BUDGET_PATH or the user config directory)")
}

func runMayorWorkflowBudget(cmd *cobra.Command, args []string) error {
	setTokens := cmd.Flags().Changed("tokens")
	setUSD := cmd.Flags().Changed("usd")
	if !setTokens && !setUSD {
		return fmt.Errorf("at least one of --tokens or --usd is required")
	}
	if setTokens && workflowBudgetTokens <= 0 {
		return fmt.Errorf("--tokens must be a positive integer")
	}
	if setUSD && workflowBudgetUSD <= 0 {
		return fmt.Errorf("--usd must be a positive amount")
	}

	townRoot, err := resolveMayorWorkflowTownRoot()
	if err != nil {
		return err
	}
	workflows, err := orchestrator.NewManager(townRoot).GetWorkflowStatus(args[0])
	if err != nil {
		return err
	}
	if len(workflows) != 1 {
		return fmt.Errorf("expected one workflow instance for %q, found %d", args[0], len(workflows))
	}
	rig := workflows[0].Variables["rig"]
	if rig == "" {
		return fmt.Errorf("workflow %q has no rig identity; cannot set workflow budget", args[0])
	}

	if setTokens {
		if err := orchestrator.SetWorkflowTokenLimit(townRoot, rig, args[0], workflowBudgetTokens); err != nil {
			return err
		}
	}
	if setUSD {
		proxyPath, err := resolveWorkflowBudgetProxyPath(workflowBudgetProxyPath)
		if err != nil {
			if setTokens {
				return fmt.Errorf("token limit was updated, but the dollar allowance was not: %w", err)
			}
			return err
		}
		if err := orchestrator.SetWorkflowDollarLimit(proxyPath, rig, args[0], workflowBudgetUSD); err != nil {
			if setTokens {
				return fmt.Errorf("token limit was updated, but the dollar allowance was not: %w", err)
			}
			return err
		}
	}

	fmt.Printf("%s Budget updated for %s/%s:", style.SuccessPrefix, rig, args[0])
	if setTokens {
		fmt.Printf(" tokens=%d", workflowBudgetTokens)
	}
	if setUSD {
		fmt.Printf(" workflow allowance=$%.2f", workflowBudgetUSD)
	}
	fmt.Println()
	fmt.Println("Global defaults and other workflows are unchanged. Resume the workflow explicitly when ready.")
	return nil
}

func resolveWorkflowBudgetProxyPath(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if path := os.Getenv("FREERIDE_EXECUTION_BUDGET_PATH"); path != "" {
		return path, nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve Freeride budget path: %w", err)
	}
	return filepath.Join(configDir, "freeride", "execution-budgets.json"), nil
}
