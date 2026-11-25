package orchestrator

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
	"time"

	"github.com/steveyegge/gastown/internal/config"
)

type WorkflowCostStatus struct {
	AccountedUSD           float64  `json:"accounted_cost_usd"`
	RigAccountedUSD        *float64 `json:"rig_accounted_cost_usd"`
	Accounting             string   `json:"accounting"`
	UnknownCostRequests    int64    `json:"unknown_cost_requests"`
	DollarCoverageComplete bool     `json:"dollar_coverage_complete"`
}

func WorkflowBudgetStatusEndpoint(townRoot string) (string, error) {
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

func FetchWorkflowCostStatus(ctx context.Context, client *http.Client, endpoint, rig, workflowID string) (*WorkflowCostStatus, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("rig", rig)
	q.Set("workflow", workflowID)
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
	var status WorkflowCostStatus
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&status); err != nil {
		return nil, fmt.Errorf("budget API unavailable; rebuild/restart Freeride")
	}
	if status.AccountedUSD < 0 || (status.RigAccountedUSD != nil && *status.RigAccountedUSD < 0) {
		return nil, fmt.Errorf("invalid budget response")
	}
	return &status, nil
}

func CheckWorkflowDollarBudget(townRoot, rig, workflowID string) error {
	if townRoot == "" || rig == "" || workflowID == "" {
		return fmt.Errorf("execution budget: missing workflow identity")
	}
	budget, _, err := ReadWorkflowExecutionBudget(townRoot, rig, workflowID)
	if err != nil {
		return err
	}
	rigLimit, err := RigDollarLimitMicros()
	if err != nil {
		return err
	}
	if budget.DollarLimitMicros > rigLimit {
		rigLimit = budget.DollarLimitMicros
	}
	endpoint, err := WorkflowBudgetStatusEndpoint(townRoot)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	status, err := FetchWorkflowCostStatus(ctx, client, endpoint, rig, workflowID)
	if err != nil {
		return err
	}
	if accounted := int64(status.AccountedUSD * 1e6); accounted >= budget.DollarLimitMicros {
		return fmt.Errorf("execution budget exhausted for %s/%s: accounted $%.6f, workflow dollar limit $%.6f; review the ledger and resume explicitly after raising the limit", rig, workflowID, status.AccountedUSD, float64(budget.DollarLimitMicros)/1e6)
	}
	if status.RigAccountedUSD != nil {
		if accounted := int64(*status.RigAccountedUSD * 1e6); accounted >= rigLimit {
			return fmt.Errorf("execution budget exhausted for rig %s: accounted $%.6f, rig dollar limit $%.6f; review the ledger and resume explicitly after raising the limit", rig, *status.RigAccountedUSD, float64(rigLimit)/1e6)
		}
	}
	return nil
}
