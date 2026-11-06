package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gofrs/flock"
)

// ExecutionBudget is a conservative, durable admission ledger. Reservations are
// never refunded on errors: an interrupted HTTP call may already have been billed.
type ExecutionBudget struct {
	Requests       int   `json:"requests"`
	ReservedTokens int64 `json:"reserved_tokens"`
	RequestLimit   int64 `json:"request_limit,omitempty"`
	TokenLimit     int64 `json:"token_limit,omitempty"`
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
	if townRoot == "" || rig == "" || workflowID == "" || tokens <= 0 {
		return fmt.Errorf("execution budget: missing request identity or token reservation")
	}
	requests, err := executionLimit("GT_WORKFLOW_MAX_LLM_REQUESTS", 1000)
	if err != nil {
		return err
	}
	tokenLimit, err := executionLimit("GT_WORKFLOW_MAX_LLM_TOKENS", 20000000)
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
	if used.Requests < 0 || used.ReservedTokens < 0 {
		return fmt.Errorf("execution budget: invalid negative reservation")
	}
	if int64(used.Requests) >= requests || tokens > tokenLimit-used.ReservedTokens {
		return fmt.Errorf("execution budget exhausted for %s/%s (%d requests, %d reserved tokens); review ledger and limits before resuming", rig, workflowID, used.Requests, used.ReservedTokens)
	}
	used.RequestLimit, used.TokenLimit = requests, tokenLimit
	used.Requests++
	used.ReservedTokens += tokens
	ledger[key] = used
	data, err = json.MarshalIndent(ledger, "", "  ")
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
	if used.Requests < 0 || used.ReservedTokens < 0 || used.RequestLimit < 0 || used.TokenLimit < 0 {
		return used, false, fmt.Errorf("invalid negative execution budget")
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
