package orchestrator

import (
	"strings"
	"testing"
)

func TestReorderDeliveryPhasesWebBeforeHTTPHandlers(t *testing.T) {
	v := WorkflowValidation{
		ActivePhaseIDField: "api-handlers",
		DeliveryPhases: []DeliveryPhase{
			{ID: "go-module", RequiredFiles: []string{"linkshelf/go.mod"}},
			{ID: "store-layer", RequiredFiles: []string{"linkshelf/internal/store/store.go"}},
			{ID: "api-handlers", RequiredFiles: []string{"linkshelf/internal/api/handlers.go"}, QAVerifyCommand: "cd linkshelf && go test ./internal/api/..."},
			{ID: "server-main", RequiredFiles: []string{"linkshelf/cmd/server/main.go"}},
			{ID: "web-static", RequiredFiles: []string{"linkshelf/web/style.css", "linkshelf/web/app.js"}},
			{ID: "web-shell", RequiredFiles: []string{"linkshelf/web/index.html"}},
		},
	}
	got := reorderDeliveryPhasesWebBeforeHTTPHandlers(v)
	var handlerIdx, firstWebIdx = -1, -1
	for i, p := range got.DeliveryPhases {
		for _, f := range p.RequiredFiles {
			if IsHTTPHandlerImplementPath(f) && handlerIdx < 0 {
				handlerIdx = i
			}
			if strings.Contains(f, "/web/") && firstWebIdx < 0 {
				firstWebIdx = i
			}
		}
	}
	if handlerIdx < 0 || firstWebIdx < 0 || firstWebIdx > handlerIdx {
		t.Fatalf("web phases must precede handler phase: handlerIdx=%d firstWebIdx=%d phases=%v",
			handlerIdx, firstWebIdx, phaseIDs(got.DeliveryPhases))
	}
	wantOrder := []string{"go-module", "store-layer", "web-static", "web-shell", "api-handlers", "server-main"}
	if ids := phaseIDs(got.DeliveryPhases); strings.Join(ids, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("phase order = %v, want %v", ids, wantOrder)
	}
}

func TestReorderDeliveryPhasesWebBeforeHTTPHandlers_preservesUnrelatedOrder(t *testing.T) {
	v := WorkflowValidation{
		DeliveryPhases: []DeliveryPhase{
			{ID: "setup-infrastructure", RequiredFiles: []string{"backend/main.py"}},
			{ID: "backend-core", RequiredFiles: []string{"backend/db/schema.sql", "Dockerfile", "docker-compose.yml"}},
		},
	}
	got := reorderDeliveryPhasesWebBeforeHTTPHandlers(v)
	want := []string{"setup-infrastructure", "backend-core"}
	if ids := phaseIDs(got.DeliveryPhases); strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("phase order = %v, want %v", ids, want)
	}
}

func phaseIDs(phases []DeliveryPhase) []string {
	out := make([]string, len(phases))
	for i, p := range phases {
		out[i] = p.ID
	}
	return out
}
