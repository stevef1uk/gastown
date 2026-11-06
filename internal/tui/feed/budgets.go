package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/orchestrator"
)

type WorkflowCost struct {
	UpstreamAttempts int64    `json:"upstream_attempts"`
	UpstreamLimit    *int64   `json:"upstream_attempt_limit"`
	Accounted        float64  `json:"accounted_cost_usd"`
	Limit            *float64 `json:"workflow_allowance_usd"`
	RigAccounted     *float64 `json:"rig_accounted_cost_usd"`
	RigLimit         *float64 `json:"rig_allowance_usd"`
	Accounting       string   `json:"accounting"`
	Unknown          int64    `json:"unknown_cost_requests"`
}

type WorkflowBudget struct {
	Execution       orchestrator.ExecutionBudget
	PersistedLimits bool
	ExecutionError  string
	Cost            *WorkflowCost
	CostError       string
}

// Automatically discover only a local proxy. Remote services opt in explicitly;
// do not send workflow identities to arbitrary inference providers' API roots.
func workflowBudgetEndpoint(townRoot string) (string, error) {
	endpoint := strings.TrimSpace(os.Getenv("GT_BUDGET_STATUS_URL"))
	if endpoint != "" {
		u, err := url.Parse(endpoint)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
			return "", fmt.Errorf("invalid GT_BUDGET_STATUS_URL")
		}
		return u.String(), nil
	}
	endpoint = strings.TrimSpace(os.Getenv("LLM_ENDPOINT"))
	if endpoint == "" {
		settings, err := config.LoadOrCreateTownSettings(config.TownSettingsPath(townRoot))
		if err != nil {
			return "", fmt.Errorf("cannot read proxy configuration")
		}
		endpoint = settings.LLMEndpoint
	}
	if endpoint == "" {
		endpoint = config.DefaultFreerideProxyEndpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return "", fmt.Errorf("invalid proxy endpoint")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return "", fmt.Errorf("set GT_BUDGET_STATUS_URL for remote budget service")
	}
	u.Path = "/api/budgets"
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func fetchWorkflowCost(ctx context.Context, client *http.Client, endpoint, rig, id string) (*WorkflowCost, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("rig", rig)
	q.Set("workflow", id)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("budget service unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("budget service HTTP %d", resp.StatusCode)
	}
	var cost WorkflowCost
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&cost); err != nil {
		return nil, fmt.Errorf("budget API unavailable; rebuild/restart Freeride")
	}
	if cost.Limit == nil || *cost.Limit <= 0 || cost.Accounted < 0 || cost.Unknown < 0 || cost.UpstreamAttempts < 0 || (cost.UpstreamLimit != nil && *cost.UpstreamLimit <= 0) || cost.Accounting == "" {
		return nil, fmt.Errorf("incomplete budget response")
	}
	if (cost.RigAccounted != nil && *cost.RigAccounted < 0) || (cost.RigLimit != nil && *cost.RigLimit <= 0) {
		return nil, fmt.Errorf("invalid rig budget response")
	}
	return &cost, nil
}

func loadWorkflowBudgets(townRoot string, byRig map[string][]RigWorkflow) {
	endpoint, endpointErr := workflowBudgetEndpoint(townRoot)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// Bound concurrency and the whole refresh. Never block the TUI on HTTP.
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
	if b.Cost == nil {
		fmt.Fprintf(&out, "\n    Cost unavailable: %s", b.CostError)
	} else {
		c := b.Cost
		fmt.Fprintf(&out, "\n    Accounted: $%.4f / $%.2f workflow%s", c.Accounted, *c.Limit, budgetWarning(c.Accounted, *c.Limit))
		if c.RigAccounted != nil && c.RigLimit != nil {
			fmt.Fprintf(&out, "\n    Rig accounted: $%.4f / $%.2f%s", *c.RigAccounted, *c.RigLimit, budgetWarning(*c.RigAccounted, *c.RigLimit))
		}
		if c.UpstreamLimit != nil {
			fmt.Fprintf(&out, "\n    Proxy attempts: %d / %d%s", c.UpstreamAttempts, *c.UpstreamLimit, budgetWarning(float64(c.UpstreamAttempts), float64(*c.UpstreamLimit)))
		}
		fmt.Fprintf(&out, "\n    Accounting: %s; unknown-cost calls %d", strings.Join(strings.Fields(c.Accounting), " "), c.Unknown)
	}
	if b.ExecutionError != "" {
		fmt.Fprintf(&out, "\n    Execution unavailable: %s", b.ExecutionError)
	} else {
		e := b.Execution
		fmt.Fprintf(&out, "\n    Requests: %d / %d%s\n    Reserved tokens: %d / %d%s", e.Requests, e.RequestLimit, budgetWarning(float64(e.Requests), float64(e.RequestLimit)), e.ReservedTokens, e.TokenLimit, budgetWarning(float64(e.ReservedTokens), float64(e.TokenLimit)))
		if !b.PersistedLimits {
			out.WriteString(" (local limits)")
		}
	}
	return out.String()
}
