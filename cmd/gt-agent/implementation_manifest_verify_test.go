package main

import (
	"github.com/steveyegge/gastown/internal/orchestrator"
	"testing"
)

func TestImplementationManifestVerifyCreditsAdvertisedCommand(t *testing.T) {
	v := orchestrator.WorkflowValidation{
		LayoutRoot: "pingapp", TestRunner: "go", ActivePhaseIDField: "go-module",
		RequiredFiles:   []string{"pingapp/go.mod", "pingapp/cmd/server/main.go", "pingapp/docker-compose.yml"},
		QAVerifyCommand: "docker compose -f pingapp/docker-compose.yml up --exit-code-from playwright",
		DeliveryPhases: []orchestrator.DeliveryPhase{
			{ID: "go-module", RequiredFiles: []string{"pingapp/go.mod"}, QAVerifyCommand: "test -f pingapp/go.mod && (cd pingapp && go mod tidy && go test ./...)"},
			{ID: "core", RequiredFiles: []string{"pingapp/cmd/server/main.go"}, QAVerifyCommand: "cd pingapp && go test ./..."},
			{ID: "integration", RequiredFiles: []string{"pingapp/docker-compose.yml"}},
		},
	}
	cmd := "cd pwtest/mayor/rig/pingapp && go mod download"
	for _, scoped := range []orchestrator.WorkflowValidation{v, v.ForActivePhase()} {
		if got := scoped.ActivePhaseQAVerifyCommand(); got != "cd pingapp && go mod download" {
			t.Fatalf("effective verify: %q", got)
		}
		r := &stateRunner{townRoot: t.TempDir(), rig: "pwtest", v: scoped, track: &cmdTracker{hadCmdFailure: true}}
		trackHandlers["implementation"](r, cmd, nil)
		if !r.track.verifyOK || r.track.hadCmdFailure {
			t.Fatalf("successful advertised verify was not credited: %+v", r.track)
		}
	}
	v.ActivePhaseIDField = "core"
	if isImplementationVerifyCommandOK(cmd, "", "pwtest", "", v) {
		t.Fatal("dependency download must not certify an implementation phase with source")
	}
}
