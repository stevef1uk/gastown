package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestParseDependencyBlockedReport(t *testing.T) {
	outcome, summary, ok := parseOrchestratedJSON(`{"outcome":"dependency_blocked","dependency":{"consumer":"app/a.py","dependency":"app/b.py","evidence":"python: ModuleNotFoundError"}}`)
	var report orchestrator.DependencyBlock
	if !ok || outcome != "dependency_blocked" || json.Unmarshal([]byte(summary), &report) != nil || report.Dependency != "app/b.py" || report.Evidence == "" {
		t.Fatalf("lost structured report: %q %q %v", outcome, summary, ok)
	}
}

func TestMissingManifestCloseRequestsWriteRatherThanPhaseBuild(t *testing.T) {
	previous := orchestrator.ListImplementBeadsByStatusHook
	orchestrator.ListImplementBeadsByStatusHook = func(_, _ string, _ orchestrator.WorkflowValidation, status string) ([]orchestrator.PlanBead, error) {
		if status == "in_progress" {
			return []orchestrator.PlanBead{{ID: "sm-manifest", Title: "Implement app/frontend/package.json per architecture"}}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { orchestrator.ListImplementBeadsByStatusHook = previous })
	v := orchestrator.WorkflowValidation{LayoutRoot: "app", BeadTitleContains: "Implement ", QAVerifyCommand: "docker compose -f app/compose.yml build", RequiredFiles: []string{"app/frontend/package.json"}}
	err := validateImplementationBeadClose("bd close sm-manifest", t.TempDir(), "sample", v, false)
	if err == nil || !strings.Contains(err.Error(), "app/frontend/package.json") || !strings.Contains(err.Error(), "WRITE:") || strings.Contains(err.Error(), "run green verify") {
		t.Fatalf("misleading close rejection: %v", err)
	}
}
