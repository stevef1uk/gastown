package orchestrator

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gofrs/flock"
)

// ExecutionBudget is a conservative, durable admission ledger. Reservations are
// never refunded on errors: an interrupted HTTP call may already have been billed.
type ExecutionBudget struct {
	Requests           int   `json:"requests"`
	ReservedTokens     int64 `json:"reserved_tokens"`
	RequestLimit       int64 `json:"request_limit,omitempty"`
	TokenLimit         int64 `json:"token_limit,omitempty"`
	TokenLimitOverride int64 `json:"token_limit_override,omitempty"`
}

func executionLimit(name string, fallback int64) (int64, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("execution budget: %s must be a positive integer", name)
	}
	return n, nil
}

// ReserveWorkflowLLMRequest admits one HTTP attempt across all agents and restarts.
// Identity is JSON data rather than a filename, preventing path traversal.
func ReserveWorkflowLLMRequest(townRoot, rig, workflowID string, tokens int64) error {
	_, err := ReserveWorkflowLLMRequestWithAvailable(townRoot, rig, workflowID, tokens)
	return err
}

// ReserveWorkflowLLMRequestWithAvailable admits one HTTP attempt and reports the
// token headroom observed under the same lock as the reservation.
func ReserveWorkflowLLMRequestWithAvailable(townRoot, rig, workflowID string, tokens int64) (int64, error) {
	if townRoot == "" || rig == "" || workflowID == "" || tokens <= 0 {
		return 0, fmt.Errorf("execution budget: missing request identity or token reservation")
	}
	requests, err := executionLimit("GT_WORKFLOW_MAX_LLM_REQUESTS", 1000)
	if err != nil {
		return 0, err
	}
	tokenLimit, err := executionLimit("GT_WORKFLOW_MAX_LLM_TOKENS", 20000000)
	if err != nil {
		return 0, err
	}
	path := filepath.Join(townRoot, "orchestrator", "execution-budgets.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return 0, fmt.Errorf("execution budget: %w", err)
	}
	lock := flock.New(path + ".lock")
	if err := lock.Lock(); err != nil {
		return 0, fmt.Errorf("execution budget: %w", err)
	}
	defer lock.Unlock()
	ledger := map[string]ExecutionBudget{}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &ledger); err != nil {
			return 0, fmt.Errorf("execution budget: corrupt ledger: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return 0, fmt.Errorf("execution budget: %w", err)
	}
	if ledger == nil {
		return 0, fmt.Errorf("execution budget: null ledger")
	}
	keyBytes, _ := json.Marshal([]string{rig, workflowID})
	key := string(keyBytes)
	used := ledger[key]
	if used.Requests < 0 || used.ReservedTokens < 0 || used.RequestLimit < 0 || used.TokenLimit < 0 || used.TokenLimitOverride < 0 {
		return 0, fmt.Errorf("execution budget: invalid negative reservation")
	}
	if used.TokenLimitOverride > 0 {
		tokenLimit = used.TokenLimitOverride
	}
	available := int64(0)
	if used.ReservedTokens < tokenLimit {
		available = tokenLimit - used.ReservedTokens
	}
	if int64(used.Requests) >= requests || tokens > available {
		return available, fmt.Errorf("execution budget exhausted for %s/%s (%d requests, %d reserved tokens; requested %d reservation units, %d available, token limit %d); review ledger and limits before resuming", rig, workflowID, used.Requests, used.ReservedTokens, tokens, available, tokenLimit)
	}
	used.RequestLimit, used.TokenLimit = requests, tokenLimit
	used.Requests++
	used.ReservedTokens += tokens
	ledger[key] = used
	if err := writeExecutionBudgetLedger(path, ledger); err != nil {
		return available, err
	}
	return available, nil
}

// SetWorkflowTokenLimit sets a durable token-limit override for one workflow.
func SetWorkflowTokenLimit(townRoot, rig, workflowID string, limit int64) error {
	if townRoot == "" || rig == "" || workflowID == "" || limit <= 0 {
		return fmt.Errorf("execution budget: town, rig, workflow, and a positive token limit are required")
	}
	requests, err := executionLimit("GT_WORKFLOW_MAX_LLM_REQUESTS", 1000)
	if err != nil {
		return err
	}
	path := filepath.Join(townRoot, "orchestrator", "execution-budgets.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("execution budget: %w", err)
	}
	lock := flock.New(path + ".lock")
	if err := lock.Lock(); err != nil {
		return fmt.Errorf("execution budget: %w", err)
	}
	defer lock.Unlock()

	ledger := map[string]ExecutionBudget{}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &ledger); err != nil {
			return fmt.Errorf("execution budget: corrupt ledger: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("execution budget: %w", err)
	}
	if ledger == nil {
		return fmt.Errorf("execution budget: null ledger")
	}
	keyBytes, _ := json.Marshal([]string{rig, workflowID})
	key := string(keyBytes)
	used := ledger[key]
	if used.Requests < 0 || used.ReservedTokens < 0 || used.RequestLimit < 0 || used.TokenLimit < 0 || used.TokenLimitOverride < 0 {
		return fmt.Errorf("execution budget: invalid negative reservation")
	}
	if limit <= used.ReservedTokens {
		return fmt.Errorf("execution budget: token limit must exceed the %d tokens already reserved", used.ReservedTokens)
	}
	if used.RequestLimit == 0 {
		used.RequestLimit = requests
	}
	used.TokenLimit = limit
	used.TokenLimitOverride = limit
	ledger[key] = used
	return writeExecutionBudgetLedger(path, ledger)
}

// SetWorkflowDollarLimit writes a per-workflow USD allowance beside the proxy ledger.
func SetWorkflowDollarLimit(budgetPath, rig, workflowID string, limitUSD float64) error {
	if budgetPath == "" || rig == "" || workflowID == "" || limitUSD <= 0 || limitUSD > 1000000 || math.IsNaN(limitUSD) || math.IsInf(limitUSD, 0) {
		return fmt.Errorf("execution budget: budget path, rig, workflow, and a finite USD limit in (0, 1000000] are required")
	}
	limit := int64(math.Round(limitUSD * 1000000))
	if limit <= 0 {
		return fmt.Errorf("execution budget: USD limit is below one microdollar")
	}
	costLedger, err := readExecutionBudgetValues(budgetPath + ".usd")
	if err != nil {
		return err
	}
	keyBytes, _ := json.Marshal([]string{rig, workflowID})
	key := string(keyBytes)
	if costLedger[key] >= limit {
		return fmt.Errorf("execution budget: USD limit must exceed the $%.6f already accounted for this workflow", float64(costLedger[key])/1000000)
	}
	path := budgetPath + ".limits"
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("execution budget: %w", err)
	}
	lock := flock.New(path + ".lock")
	if err := lock.Lock(); err != nil {
		return fmt.Errorf("execution budget: %w", err)
	}
	defer lock.Unlock()
	limits, err := readExecutionBudgetValues(path)
	if err != nil {
		return err
	}
	for _, current := range limits {
		if current == 0 || current > 1000000000000 {
			return fmt.Errorf("execution budget: invalid workflow dollar limit")
		}
	}
	limits[key] = limit
	return writeExecutionBudgetMap(path, limits)
}

func readExecutionBudgetValues(path string) (map[string]int64, error) {
	values := map[string]int64{}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &values); err != nil {
			return nil, fmt.Errorf("execution budget: corrupt ledger %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("execution budget: %w", err)
	}
	if values == nil {
		return nil, fmt.Errorf("execution budget: null ledger %s", path)
	}
	for _, value := range values {
		if value < 0 {
			return nil, fmt.Errorf("execution budget: negative ledger value in %s", path)
		}
	}
	return values, nil
}

func writeExecutionBudgetLedger(path string, ledger map[string]ExecutionBudget) error {
	return writeExecutionBudgetMap(path, ledger)
}

func writeExecutionBudgetMap(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("execution budget: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".execution-budget-*")
	if err != nil {
		return fmt.Errorf("execution budget: %w", err)
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("execution budget: %w", err)
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("execution budget: %w", err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("execution budget: %w", err)
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return fmt.Errorf("execution budget: %w", err)
	}
	return nil
}

// ReadWorkflowExecutionBudget reads an atomic ledger snapshot without changing it.
// New admissions persist enforcing-process limits; legacy rows use local settings.
func ReadWorkflowExecutionBudget(townRoot, rig, workflowID string) (ExecutionBudget, bool, error) {
	data, err := os.ReadFile(filepath.Join(townRoot, "orchestrator", "execution-budgets.json"))
	ledger := map[string]ExecutionBudget{}
	if err != nil && !os.IsNotExist(err) {
		return ExecutionBudget{}, false, err
	}
	if err == nil {
		if err = json.Unmarshal(data, &ledger); err != nil {
			return ExecutionBudget{}, false, err
		}
		if ledger == nil {
			return ExecutionBudget{}, false, fmt.Errorf("null execution budget ledger")
		}
	}
	key, _ := json.Marshal([]string{rig, workflowID})
	used := ledger[string(key)]
	if used.Requests < 0 || used.ReservedTokens < 0 || used.RequestLimit < 0 || used.TokenLimit < 0 || used.TokenLimitOverride < 0 {
		return used, false, fmt.Errorf("invalid negative execution budget")
	}
	if used.TokenLimitOverride > 0 {
		used.TokenLimit = used.TokenLimitOverride
	}
	persisted := used.RequestLimit > 0 && used.TokenLimit > 0
	if used.RequestLimit == 0 {
		used.RequestLimit, err = executionLimit("GT_WORKFLOW_MAX_LLM_REQUESTS", 1000)
		if err != nil {
			return used, false, err
		}
	}
	if used.TokenLimit == 0 {
		used.TokenLimit, err = executionLimit("GT_WORKFLOW_MAX_LLM_TOKENS", 20000000)
		if err != nil {
			return used, false, err
		}
	}
	return used, persisted, nil
}
