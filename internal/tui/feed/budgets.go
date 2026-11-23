package feed

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

type WorkflowCost = orchestrator.WorkflowCostStatus

type WorkflowBudget struct {
	Execution       orchestrator.ExecutionBudget
	PersistedLimits bool
	ExecutionError  string
	Cost            *WorkflowCost
	CostError       string
	RigLimitMicros  int64
	RigLimitError   string
}

func workflowBudgetEndpoint(townRoot string) (string, error) {
	return orchestrator.WorkflowBudgetStatusEndpoint(townRoot)
}

func fetchWorkflowCost(ctx context.Context, client *http.Client, endpoint, rig, id string) (*WorkflowCost, error) {
	return orchestrator.FetchWorkflowCostStatus(ctx, client, endpoint, rig, id)
}

func loadWorkflowBudgets(townRoot string, byRig map[string][]RigWorkflow) {
	endpoint, endpointErr := workflowBudgetEndpoint(townRoot)
	rigLimit, rigLimitErr := orchestrator.RigDollarLimitMicros()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	slots := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for rig, workflows := range byRig {
		for i := range workflows {
			wf := &workflows[i]
			budget := &WorkflowBudget{}
			wf.Budget = budget
			var err error
			budget.Execution, budget.PersistedLimits, err = orchestrator.ReadWorkflowExecutionBudget(townRoot, rig, wf.ID)
			if err != nil {
				budget.ExecutionError = "cannot read execution ledger/limits"
			}
			budget.RigLimitMicros = rigLimit
			if rigLimitErr != nil {
				budget.RigLimitError = "cannot read rig dollar limit"
			}
			if endpointErr != nil {
				budget.CostError = endpointErr.Error()
				continue
			}
			wg.Add(1)
			go func(rig, id string, b *WorkflowBudget) {
				defer wg.Done()
				select {
				case slots <- struct{}{}:
					defer func() { <-slots }()
				case <-ctx.Done():
					b.CostError = "budget refresh timed out"
					return
				}
				cost, err := fetchWorkflowCost(ctx, client, endpoint, rig, id)
				b.Cost = cost
				if err != nil {
					b.CostError = err.Error()
				}
			}(rig, wf.ID, budget)
		}
	}
	wg.Wait()
}

func budgetWarning(used, limit float64) string {
	if limit > 0 && used >= limit {
		return " [limit reached]"
	}
	if limit > 0 && used >= limit*.8 {
		return " [near limit]"
	}
	return ""
}

func renderWorkflowBudget(b *WorkflowBudget) string {
	var out strings.Builder
	workflowLimit := float64(b.Execution.DollarLimitMicros) / 1e6
	rigLimit := float64(b.RigLimitMicros) / 1e6
	if b.Cost == nil {
		fmt.Fprintf(&out, "\n    Cost unavailable: %s", b.CostError)
	} else {
		c := b.Cost
		fmt.Fprintf(&out, "\n    Accounted: $%.4f / $%.2f%s", c.AccountedUSD, workflowLimit, budgetWarning(c.AccountedUSD, workflowLimit))
		if c.RigAccountedUSD != nil {
			fmt.Fprintf(&out, "\n    Rig accounted: $%.4f / $%.2f%s", *c.RigAccountedUSD, rigLimit, budgetWarning(*c.RigAccountedUSD, rigLimit))
		}
	}
	if b.RigLimitError != "" {
		fmt.Fprintf(&out, "\n    Rig limit unavailable: %s", b.RigLimitError)
	}
	if b.ExecutionError != "" {
		fmt.Fprintf(&out, "\n    Execution unavailable: %s", b.ExecutionError)
		return out.String()
	}
	e := b.Execution
	fmt.Fprintf(&out, "\n    Reserved tokens: %d / %d%s", e.ReservedTokens, e.TokenLimit, budgetWarning(float64(e.ReservedTokens), float64(e.TokenLimit)))
	if !b.PersistedLimits {
		out.WriteString(" (local limits)")
	}
	return out.String()
}
