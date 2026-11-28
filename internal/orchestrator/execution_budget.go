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

const (
	defaultWorkflowDollarLimitUSD = 5
	defaultRigDollarLimitUSD      = 20
)

// ExecutionBudget is a conservative, durable admission ledger. Reservations are
// never refunded on errors: an interrupted HTTP call may already have been billed.
type ExecutionBudget struct {
	ReservedTokens            int64 `json:"reserved_tokens"`
	TokenLimit                int64 `json:"token_limit,omitempty"`
	TokenLimitOverride        int64 `json:"token_limit_override,omitempty"`
	DollarLimitMicros         int64 `json:"dollar_limit_micros,omitempty"`
	DollarLimitOverrideMicros int64 `json:"dollar_limit_override_micros,omitempty"`
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

func dollarLimitMicros(name string, fallback float64) (int64, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return int64(fallback * 1e6), nil
	}
	n, err := strconv.ParseFloat(raw, 64)
	if err != nil || n <= 0 || n > 1000000 || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, fmt.Errorf("execution budget: %s must be a positive finite amount <= 1000000", name)
	}
	return int64(n * 1e6), nil
}

func workflowDollarLimitMicros() (int64, error) {
	return dollarLimitMicros("GT_WORKFLOW_MAX_COST_USD", defaultWorkflowDollarLimitUSD)
}

func RigDollarLimitMicros() (int64, error) {
	return dollarLimitMicros("GT_RIG_MAX_COST_USD", defaultRigDollarLimitUSD)
}

func executionBudgetPath(townRoot string) string {
	return filepath.Join(townRoot, "orchestrator", "execution-budgets.json")
}

func readExecutionBudget(path string) (map[string]ExecutionBudget, error) {
	ledger := map[string]ExecutionBudget{}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &ledger); err != nil {
			return nil, fmt.Errorf("execution budget: corrupt ledger: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("execution budget: %w", err)
	}
	if ledger == nil {
		return nil, fmt.Errorf("execution budget: null ledger")
	}
	return ledger, nil
}

func validateExecutionBudget(b ExecutionBudget) error {
	if b.ReservedTokens < 0 || b.TokenLimit < 0 || b.TokenLimitOverride < 0 || b.DollarLimitMicros < 0 || b.DollarLimitOverrideMicros < 0 {
		return fmt.Errorf("execution budget: invalid negative reservation or limit")
	}
	return nil
}

// ReserveWorkflowLLMRequest admits one HTTP attempt across all agents and restarts.
// Identity is JSON data rather than a filename, preventing path traversal.
func ReserveWorkflowLLMRequest(townRoot, rig, workflowID string, tokens int64) error {
	_, err := ReserveWorkflowLLMRequestWithAvailable(townRoot, rig, workflowID, tokens)
	return err
}

const defaultWorkflowMaxLLMTokens int64 = 60000000

// ReserveWorkflowLLMRequestWithAvailable admits one HTTP attempt and reports the
// token headroom observed under the same lock as the reservation.
func ReserveWorkflowLLMRequestWithAvailable(townRoot, rig, workflowID string, tokens int64) (int64, error) {
	if townRoot == "" || rig == "" || workflowID == "" || tokens <= 0 {
		return 0, fmt.Errorf("execution budget: missing request identity or token reservation")
	}
	tokenLimit, err := executionLimit("GT_WORKFLOW_MAX_LLM_TOKENS", defaultWorkflowMaxLLMTokens)
	if err != nil {
		return 0, err
	}
	dollarLimit, err := workflowDollarLimitMicros()
	if err != nil {
		return 0, err
	}
	path := executionBudgetPath(townRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return 0, fmt.Errorf("execution budget: %w", err)
	}
	lock := flock.New(path + ".lock")
	if err := lock.Lock(); err != nil {
		return 0, fmt.Errorf("execution budget: %w", err)
	}
	defer lock.Unlock()
	ledger, err := readExecutionBudget(path)
	if err != nil {
		return 0, err
	}
	keyBytes, _ := json.Marshal([]string{rig, workflowID})
	key := string(keyBytes)
	used := ledger[key]
	if err := validateExecutionBudget(used); err != nil {
		return 0, err
	}
	if strings.TrimSpace(os.Getenv("GT_WORKFLOW_MAX_LLM_TOKENS")) == "" && used.TokenLimit > 0 {
		tokenLimit = used.TokenLimit
	}
	if used.TokenLimitOverride > 0 {
		tokenLimit = used.TokenLimitOverride
	}
	if strings.TrimSpace(os.Getenv("GT_WORKFLOW_MAX_COST_USD")) == "" && used.DollarLimitMicros > 0 {
		dollarLimit = used.DollarLimitMicros
	}
	if used.DollarLimitOverrideMicros > 0 {
		dollarLimit = used.DollarLimitOverrideMicros
	}
	available := int64(0)
	if used.ReservedTokens < tokenLimit {
		available = tokenLimit - used.ReservedTokens
	}
	if tokens > available {
		return available, fmt.Errorf("execution budget exhausted for %s/%s (%d reserved tokens; requested %d reservation units, %d available, token limit %d); review ledger and limits before resuming", rig, workflowID, used.ReservedTokens, tokens, available, tokenLimit)
	}
	used.TokenLimit = tokenLimit
	used.DollarLimitMicros = dollarLimit
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
	path := executionBudgetPath(townRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("execution budget: %w", err)
	}
	lock := flock.New(path + ".lock")
	if err := lock.Lock(); err != nil {
		return fmt.Errorf("execution budget: %w", err)
	}
	defer lock.Unlock()
	ledger, err := readExecutionBudget(path)
	if err != nil {
		return err
	}
	keyBytes, _ := json.Marshal([]string{rig, workflowID})
	key := string(keyBytes)
	used := ledger[key]
	if err := validateExecutionBudget(used); err != nil {
		return err
	}
	if limit <= used.ReservedTokens {
		return fmt.Errorf("execution budget: token limit must exceed the %d tokens already reserved", used.ReservedTokens)
	}
	used.TokenLimit = limit
	used.TokenLimitOverride = limit
	ledger[key] = used
	return writeExecutionBudgetLedger(path, ledger)
}

// SetWorkflowDollarLimit writes a durable dollar-limit override for one workflow.
func SetWorkflowDollarLimit(townRoot, rig, workflowID string, limitUSD float64) error {
	if townRoot == "" || rig == "" || workflowID == "" || limitUSD <= 0 || limitUSD > 1000000 || math.IsNaN(limitUSD) || math.IsInf(limitUSD, 0) {
		return fmt.Errorf("execution budget: town, rig, workflow, and a finite USD limit in (0, 1000000] are required")
	}
	limit := int64(math.Round(limitUSD * 1e6))
	if limit <= 0 {
		return fmt.Errorf("execution budget: USD limit is below one microdollar")
	}
	path := executionBudgetPath(townRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("execution budget: %w", err)
	}
	lock := flock.New(path + ".lock")
	if err := lock.Lock(); err != nil {
		return fmt.Errorf("execution budget: %w", err)
	}
	defer lock.Unlock()
	ledger, err := readExecutionBudget(path)
	if err != nil {
		return err
	}
	keyBytes, _ := json.Marshal([]string{rig, workflowID})
	key := string(keyBytes)
	used := ledger[key]
	if err := validateExecutionBudget(used); err != nil {
		return err
	}
	used.DollarLimitMicros = limit
	used.DollarLimitOverrideMicros = limit
	ledger[key] = used
	return writeExecutionBudgetLedger(path, ledger)
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
	ledger, err := readExecutionBudget(executionBudgetPath(townRoot))
	if err != nil {
		return ExecutionBudget{}, false, err
	}
	key, _ := json.Marshal([]string{rig, workflowID})
	used := ledger[string(key)]
	if err := validateExecutionBudget(used); err != nil {
		return used, false, err
	}
	persisted := used.TokenLimit > 0 || used.DollarLimitMicros > 0
	if used.TokenLimitOverride > 0 {
		used.TokenLimit = used.TokenLimitOverride
	}
	if used.DollarLimitOverrideMicros > 0 {
		used.DollarLimitMicros = used.DollarLimitOverrideMicros
	}
	if used.TokenLimit == 0 {
		used.TokenLimit, err = executionLimit("GT_WORKFLOW_MAX_LLM_TOKENS", defaultWorkflowMaxLLMTokens)
		if err != nil {
			return used, false, err
		}
	}
	if used.DollarLimitMicros == 0 {
		used.DollarLimitMicros, err = workflowDollarLimitMicros()
		if err != nil {
			return used, false, err
		}
	}
	return used, persisted, nil
}
