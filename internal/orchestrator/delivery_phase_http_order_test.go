package orchestrator

import (
	"strings"
	"testing"
)

func TestReorderDeliveryPhasesWebAfterBackend(t *testing.T) {
	v := WorkflowValidation{
		DeliveryPhases: []DeliveryPhase{
			{ID: "go-module", RequiredFiles: []string{"linkshelf/go.mod"}},
			{ID: "web-static", RequiredFiles: []string{"linkshelf/web/style.css", "linkshelf/web/app.js"}},
			{ID: "web-shell", RequiredFiles: []string{"linkshelf/web/index.html"}},
			{ID: "store-layer", RequiredFiles: []string{"linkshelf/internal/store/store.go"}},
			{ID: "api-handlers", RequiredFiles: []string{"linkshelf/internal/api/handlers.go"}, QAVerifyCommand: "cd linkshelf && go test ./internal/api/..."},
			{ID: "server-main", RequiredFiles: []string{"linkshelf/cmd/server/main.go"}},
		},
	}
	got := reorderDeliveryPhasesWebAfterBackend(v)
	var handlerIdx, lastWebIdx = -1, -1
	for i, p := range got.DeliveryPhases {
		for _, f := range p.RequiredFiles {
			if IsHTTPHandlerImplementPath(f) && handlerIdx < 0 {
				handlerIdx = i
			}
			if strings.Contains(f, "/web/") {
				lastWebIdx = i
			}
		}
	}
	if handlerIdx < 0 || lastWebIdx < 0 || lastWebIdx < handlerIdx {
		t.Fatalf("web phases must follow handler phase: handlerIdx=%d lastWebIdx=%d phases=%v",
			handlerIdx, lastWebIdx, phaseIDs(got.DeliveryPhases))
	}
	wantOrder := []string{"go-module", "store-layer", "api-handlers", "server-main", "web-static", "web-shell"}
	if ids := phaseIDs(got.DeliveryPhases); strings.Join(ids, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("phase order = %v, want %v", ids, wantOrder)
	}
}

func TestReorderDeliveryPhasesWebAfterBackend_preservesCorrectOrder(t *testing.T) {
	v := WorkflowValidation{
		DeliveryPhases: []DeliveryPhase{
			{ID: "go-module", RequiredFiles: []string{"linkshelf/go.mod"}},
			{ID: "store-layer", RequiredFiles: []string{"linkshelf/internal/store/store.go"}},
			{ID: "api-handlers", RequiredFiles: []string{"linkshelf/internal/api/handlers.go"}, QAVerifyCommand: "cd linkshelf && go test ./internal/api/..."},
			{ID: "server-main", RequiredFiles: []string{"linkshelf/cmd/server/main.go"}},
			{ID: "web-static", RequiredFiles: []string{"linkshelf/web/style.css", "linkshelf/web/app.js"}},
			{ID: "web-shell", RequiredFiles: []string{"linkshelf/web/index.html"}},
		},
	}
	got := reorderDeliveryPhasesWebAfterBackend(v)
	want := []string{"go-module", "store-layer", "api-handlers", "server-main", "web-static", "web-shell"}
	if ids := phaseIDs(got.DeliveryPhases); strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("phase order = %v, want %v", ids, want)
	}
}

func TestReorderDeliveryPhasesWebAfterBackend_preservesUnrelatedOrder(t *testing.T) {
	v := WorkflowValidation{
		DeliveryPhases: []DeliveryPhase{
			{ID: "setup-infrastructure", RequiredFiles: []string{"backend/main.py"}},
			{ID: "backend-core", RequiredFiles: []string{"backend/db/schema.sql", "Dockerfile", "docker-compose.yml"}},
		},
	}
	got := reorderDeliveryPhasesWebAfterBackend(v)
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
