package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestImplementationBrowserVerifyUnblocksCloseAcrossBackendStacks(t *testing.T) {
	for _, backend := range []string{"service.go", "service.py"} {
		t.Run(backend, func(t *testing.T) {
			town, rig := t.TempDir(), "sample"
			writeMayorRigFiles(t, rigMayorRigDir(town, rig), map[string]string{
				"app/playwright.config.js": "const { defineConfig } = require('@playwright/test');\nmodule.exports = defineConfig({\n  testDir: './tests',\n  use: { baseURL: 'http://127.0.0.1:8080' },\n  webServer: { command: 'start-app', url: 'http://127.0.0.1:8080/' }\n});\n",
			})
			v := orchestrator.WorkflowValidation{
				LayoutRoot: "app", BeadTitleContains: "Implement ",
				MinImplementationFileBytes: 1, MinSubstantiveLines: 1,
				ActivePhaseIDField: "browser", CompletedPhaseIDsField: []string{"backend"},
				DeliveryPhases: []orchestrator.DeliveryPhase{
					{ID: "backend", RequiredFiles: []string{"app/" + backend}},
					{ID: "browser", RequiredFiles: []string{"app/playwright.config.js", "app/tests/e2e.spec.js"}, QAVerifyCommand: "cd app && npx playwright test"},
				},
			}
			v.RequiredFiles = v.UnionRequiredFiles()
			orchestrator.ListImplementBeadsByStatusHook = func(_, _ string, _ orchestrator.WorkflowValidation, status string) ([]orchestrator.PlanBead, error) {
				if status == "in_progress" {
					return []orchestrator.PlanBead{{ID: "sm-config", Title: "Implement app/playwright.config.js per architecture"}}, nil
				}
				return nil, nil
			}
			t.Cleanup(func() { orchestrator.ListImplementBeadsByStatusHook = nil })
			r := &stateRunner{
				task: &orchestrator.Task{State: "implementation"}, townRoot: town, rig: rig, v: v,
				track: &cmdTracker{activeBead: "sm-config", activeBeadPath: "app/playwright.config.js"},
			}
			close := "cd sample/mayor/rig && bd close sm-config"
			guard := func() error {
				return validateImplementationCommandWithState(close, town, rig, "sm-config", r.v, r.track.verifyOK, nil, "")
			}
			if err := guard(); err == nil || !strings.Contains(err.Error(), "verify") {
				t.Fatalf("unverified browser bead must not close: %v", err)
			}
			for _, command := range []string{
				"cd sample/mayor/rig/app && npx playwright test",
				"cd sample/mayor/rig && cd app && npx playwright test",
			} {
				trackHandlers["implementation"](r, command, nil)
				if !r.track.verifyOK || r.track.hadCmdFailure {
					t.Fatalf("green browser verify lost: command=%s track=%+v", command, r.track)
				}
				if err := guard(); err != nil {
					t.Fatalf("green browser verification must unblock bd close: %v", err)
				}
				trackHandlers["implementation"](r, command, errors.New("browser assertion failed"))
				if r.track.verifyOK || guard() == nil {
					t.Fatal("failed browser rerun must revoke close permission")
				}
			}
			for _, command := range []string{
				"cd sample/mayor/rig/app && npx playwright test --list",
				"cd sample/mayor/rig/app && npx playwright test || true",
				"cd sample/mayor/rig/app && npx playwright test & curl -f http://localhost:8080/",
				"cd sample/mayor/rig/app && npx playwright test | tee result.log",
				"cd sample/mayor/rig/app && npx playwright test --config=other.config.js",
				"cd sample/mayor/rig/app && go test ./...",
				"cd sample/mayor/rig/app && python -m pytest",
			} {
				if isImplementationVerifyCommandOK(command, town, rig, "sm-config", v) {
					t.Fatalf("noncanonical command credited as browser verification: %s", command)
				}
			}
		})
	}
}
