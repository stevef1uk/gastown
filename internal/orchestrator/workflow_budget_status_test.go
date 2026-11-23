package orchestrator

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func budgetStatusServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/budgets" || r.URL.Query().Get("rig") != "rig" || r.URL.Query().Get("workflow") != "wf" {
			t.Errorf("unexpected status request: %s", r.URL)
		}
		w.Write([]byte(body))
	}))
	t.Setenv("GT_BUDGET_STATUS_URL", server.URL+"/api/budgets")
	return server
}

func TestCheckWorkflowDollarBudget(t *testing.T) {
	town := t.TempDir()
	if err := SetWorkflowDollarLimit(town, "rig", "wf", 5); err != nil {
		t.Fatal(err)
	}
	server := budgetStatusServer(t, `{"accounted_cost_usd":4.5,"rig_accounted_cost_usd":6,"dollar_coverage_complete":true}`)
	defer server.Close()
	if err := CheckWorkflowDollarBudget(town, "rig", "wf"); err != nil {
		t.Fatal(err)
	}
}

func TestCheckWorkflowDollarBudgetDeniesExhaustedWorkflowOrRig(t *testing.T) {
	town := t.TempDir()
	if err := SetWorkflowDollarLimit(town, "rig", "wf", 1); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ body, want string }{
		{`{"accounted_cost_usd":1,"rig_accounted_cost_usd":2,"dollar_coverage_complete":true}`, "workflow dollar limit"},
		{`{"accounted_cost_usd":0.5,"rig_accounted_cost_usd":20,"dollar_coverage_complete":true}`, "rig dollar limit"},
		{`{"accounted_cost_usd":0.5,"rig_accounted_cost_usd":2,"dollar_coverage_complete":false,"accounting":"cost unknown"}`, "dollar accounting is incomplete"},
	} {
		server := budgetStatusServer(t, tc.body)
		err := CheckWorkflowDollarBudget(town, "rig", "wf")
		server.Close()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("body=%s err=%v want=%s", tc.body, err, tc.want)
		}
	}
}
