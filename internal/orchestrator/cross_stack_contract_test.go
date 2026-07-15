package orchestrator

import (
	"testing"
)

func TestExtractFrontendAPICalls_fetch(t *testing.T) {
	content := `
const p = await fetch('/api/portfolio');
await fetch('/api/trade', { method: 'POST', headers: {'Content-Type': 'application/json'} });
`
	calls := ExtractFrontendAPICalls(content, "api.ts")
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d: %+v", len(calls), calls)
	}
	if calls[0].Method != "GET" || calls[0].Path != "/api/portfolio" {
		t.Errorf("first call = %+v", calls[0])
	}
	if calls[1].Method != "POST" || calls[1].Path != "/api/trade" {
		t.Errorf("second call = %+v", calls[1])
	}
}

func TestExtractBackendAPIRoutes_fastapi(t *testing.T) {
	content := `
@api_router.get("/portfolio")
async def get_portfolio():
    pass

@api_router.post("/trade")
async def trade(req: TradeRequest):
    pass
`
	routes := ExtractBackendAPIRoutes(content, "routes.py")
	if len(routes) != 2 {
		t.Fatalf("expected 2 routes, got %d: %+v", len(routes), routes)
	}
	if routes[0].Method != "GET" || routes[0].Path != "/portfolio" {
		t.Errorf("first route = %+v", routes[0])
	}
	if routes[1].Method != "POST" || routes[1].Path != "/trade" {
		t.Errorf("second route = %+v", routes[1])
	}
}

func TestMatchAPIContract_missingBackend(t *testing.T) {
	calls := []APICall{{Method: "GET", Path: "/api/prices", File: "api.ts"}}
	routes := []APIEndpoint{{Method: "GET", Path: "/api/portfolio", File: "routes.py"}}
	report := MatchAPIContract(calls, routes)
	if report.IsClean() {
		t.Fatal("expected mismatches")
	}
	if len(report.MissingBackend) != 1 {
		t.Fatalf("expected 1 missing backend, got %d", len(report.MissingBackend))
	}
	if len(report.MissingFrontend) != 1 {
		t.Fatalf("expected 1 missing frontend, got %d", len(report.MissingFrontend))
	}
}

func TestMatchAPIContract_clean(t *testing.T) {
	calls := []APICall{{Method: "GET", Path: "/api/portfolio", File: "api.ts"}}
	routes := []APIEndpoint{{Method: "GET", Path: "/api/portfolio", File: "routes.py"}}
	report := MatchAPIContract(calls, routes)
	if !report.IsClean() {
		t.Fatalf("expected clean, got: %s", report.String())
	}
}
