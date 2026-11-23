package feed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestWorkflowBudgetsRefreshAndRender(t *testing.T) {
	town := t.TempDir()
	t.Setenv("GT_WORKFLOW_MAX_LLM_TOKENS", "100")
	if err := orchestrator.ReserveWorkflowLLMRequest(town, "rig name", "wf-1", 85); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Query().Get("rig") != "rig name" || r.URL.Query().Get("workflow") != "wf-1" {
			t.Errorf("wrong status request: %s", r.URL)
		}
		w.Write([]byte(`{"upstream_attempts":8,"upstream_attempt_limit":10,"accounted_cost_usd":4.5,"workflow_allowance_usd":5,"rig_accounted_cost_usd":6,"rig_allowance_usd":20,"accounting":"cost unknown","unknown_cost_requests":2}`))
	}))
	defer server.Close()
	t.Setenv("GT_BUDGET_STATUS_URL", server.URL+"/api/budgets")
	workflows := map[string][]RigWorkflow{"rig name": {{ID: "wf-1", Status: "running"}}}
	loadWorkflowBudgets(town, workflows)
	b := workflows["rig name"][0].Budget
	if b == nil || b.Cost == nil || !b.PersistedLimits || b.Execution.TokenLimit != 100 {
		t.Fatalf("wrong snapshot: %+v", b)
	}
	rendered := (&Model{}).renderWorkflowLine(workflows["rig name"][0])
	for _, want := range []string{"$4.5000 / $5.00", "$6.0000 / $20.00", "85 / 100", "[near limit]"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("missing %q in %s", want, rendered)
		}
	}
	for _, unwanted := range []string{"Proxy attempts", "Accounting", "Unknown-cost calls", "Requests:"} {
		if strings.Contains(rendered, unwanted) {
			t.Errorf("unexpected %q in %s", unwanted, rendered)
		}
	}
}

func TestWorkflowBudgetFailuresRemainVisible(t *testing.T) {
	town := t.TempDir()
	if err := os.MkdirAll(filepath.Join(town, "orchestrator"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(town, "orchestrator", "execution-budgets.json"), []byte("broken"), 0644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	t.Setenv("GT_BUDGET_STATUS_URL", server.URL)
	workflows := map[string][]RigWorkflow{"rig": {{ID: "wf"}}}
	loadWorkflowBudgets(town, workflows)
	rendered := renderWorkflowBudget(workflows["rig"][0].Budget)
	for _, want := range []string{"Cost unavailable:", "HTTP 404", "Execution unavailable:"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("missing %q: %s", want, rendered)
		}
	}
	if strings.Contains(rendered, "$0.0000") {
		t.Fatal("unavailable cost rendered as free")
	}
}

func TestWorkflowCostResponseValidation(t *testing.T) {
	for _, body := range []string{`{}`, `{"workflow_allowance_usd":-1}`, `{"accounted_cost_usd":-1,"workflow_allowance_usd":5}`, `invalid`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(body)) }))
		_, err := fetchWorkflowCost(context.Background(), server.Client(), server.URL, "rig", "wf")
		server.Close()
		if err == nil {
			t.Fatalf("accepted invalid cost status: %s", body)
		}
	}
}

func TestWorkflowBudgetEndpointDoesNotProbeProviders(t *testing.T) {
	t.Setenv("GT_BUDGET_STATUS_URL", "")
	t.Setenv("LLM_ENDPOINT", "https://api.cerebras.ai/v1/chat/completions")
	if _, err := workflowBudgetEndpoint(t.TempDir()); err == nil {
		t.Fatal("remote inference provider inferred as budget service")
	}
	t.Setenv("LLM_ENDPOINT", "http://localhost:11434/v1/chat/completions")
	if got, err := workflowBudgetEndpoint(t.TempDir()); err != nil || got != "http://localhost:11434/api/budgets" {
		t.Fatalf("%s %v", got, err)
	}
}

func TestOldProxyBudgetMessageIsActionable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("Freeride Proxy is running")) }))
	defer server.Close()
	_, err := fetchWorkflowCost(context.Background(), server.Client(), server.URL, "rig", "wf")
	if err == nil || !strings.Contains(err.Error(), "rebuild/restart Freeride") {
		t.Fatalf("unhelpful legacy proxy error: %v", err)
	}
}
