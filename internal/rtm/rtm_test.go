package rtm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleTestPlan = `# Test Plan — FinAlly

## Requirements → tests

### project-foundation
Requirement: The root route serves the real dashboard HTML (heading, ten default tickers, $10,000 cash) and never a placeholder; the health route returns HTTP 200 with status ok.
Level: integration
Test file: finally/backend/app/main_test.py
Bead ID: closed
Phase: project-foundation
Scenarios:
- Root route with index.html present returns exported dashboard
- Health route returns HTTP 200
Assertions:
- Body contains heading and tickers
- Body contains $10,000 cash

### core-market-infrastructure
Requirement: GBM simulator and SSE generator behave per SPEC.
Level: unit
Test file: finally/backend/tests/test_market.py
Bead ID: te-57g
Phase: core-market-infrastructure
Scenarios:
- GBM step produces positive price
Assertions:
- Price equals expected value
`

func TestParseTestPlanDetailed(t *testing.T) {
	blocks := ParseTestPlanDetailed(sampleTestPlan)
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}

	b1 := blocks[0]
	if b1.ReqID != "project-foundation" {
		t.Errorf("expected ReqID project-foundation, got %q", b1.ReqID)
	}
	if !strings.Contains(b1.Requirement, "The root route serves the real dashboard HTML") {
		t.Errorf("unexpected Requirement: %q", b1.Requirement)
	}
	if b1.Level != "integration" {
		t.Errorf("expected Level integration, got %q", b1.Level)
	}
	if b1.TestFile != "finally/backend/app/main_test.py" {
		t.Errorf("expected TestFile finally/backend/app/main_test.py, got %q", b1.TestFile)
	}
	if b1.BeadID != "closed" {
		t.Errorf("expected BeadID closed, got %q", b1.BeadID)
	}
	if b1.Phase != "project-foundation" {
		t.Errorf("expected Phase project-foundation, got %q", b1.Phase)
	}
	if len(b1.Scenarios) != 2 {
		t.Errorf("expected 2 scenarios, got %d", len(b1.Scenarios))
	}
	if len(b1.Assertions) != 2 {
		t.Errorf("expected 2 assertions, got %d", len(b1.Assertions))
	}

	b2 := blocks[1]
	if b2.ReqID != "core-market-infrastructure" {
		t.Errorf("expected ReqID core-market-infrastructure, got %q", b2.ReqID)
	}
	if b2.BeadID != "te-57g" {
		t.Errorf("expected BeadID te-57g, got %q", b2.BeadID)
	}
}

func TestParseTestPlanDetailed_MultiRequirement(t *testing.T) {
	content := `### phase-1-core
Requirement: Domain validation: project name trimmed
Level: unit
Test file: internal/model/model_test.go
Bead ID: ta-4rb
Phase: phase-1-core

Requirement: Configuration parsing: env overrides
Level: unit
Test file: internal/config/config_test.go
Bead ID: ta-gd2
Phase: phase-1-core
`
	blocks := ParseTestPlanDetailed(content)
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks from multi-requirement section, got %d", len(blocks))
	}
	if blocks[0].BeadID != "ta-4rb" {
		t.Errorf("expected first bead ta-4rb, got %q", blocks[0].BeadID)
	}
	if blocks[1].BeadID != "ta-gd2" {
		t.Errorf("expected second bead ta-gd2, got %q", blocks[1].BeadID)
	}
	if !strings.Contains(blocks[0].Requirement, "Domain validation") {
		t.Errorf("expected Domain validation in first block, got %q", blocks[0].Requirement)
	}
	if !strings.Contains(blocks[1].Requirement, "Configuration parsing") {
		t.Errorf("expected Configuration parsing in second block, got %q", blocks[1].Requirement)
	}
}

func TestParsePlanBeadMap(t *testing.T) {
	planMD := `
# Implementation plan
## Bead map
### te-dpt: finally/frontend/app/polish.css
- Scope: polish
### te-57g: finally/frontend/tailwind.config.ts
- Scope: config
`
	m := ParsePlanBeadMap(planMD)
	if len(m) != 2 {
		t.Fatalf("expected 2 beads, got %d", len(m))
	}
	if m["te-dpt"] != "finally/frontend/app/polish.css" {
		t.Errorf("unexpected path for te-dpt: %q", m["te-dpt"])
	}
	if m["te-57g"] != "finally/frontend/tailwind.config.ts" {
		t.Errorf("unexpected path for te-57g: %q", m["te-57g"])
	}
}

func TestGenerateRTM(t *testing.T) {
	town := t.TempDir()
	rig := "testrig"
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write TEST_PLAN.md
	if err := os.WriteFile(filepath.Join(rigDir, "TEST_PLAN.md"), []byte(sampleTestPlan), 0644); err != nil {
		t.Fatal(err)
	}

	// Create test file 1 with content (>50 bytes)
	t1Path := filepath.Join(rigDir, "finally/backend/app/main_test.py")
	if err := os.MkdirAll(filepath.Dir(t1Path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(t1Path, []byte("def test_dashboard():\n    assert 200 == 200\n    assert 'heading' in 'heading'\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Test file 2 is missing

	report, err := GenerateRTM(town, rig)
	if err != nil {
		t.Fatalf("GenerateRTM failed: %v", err)
	}

	if report.TotalReqs != 2 {
		t.Errorf("expected 2 total reqs, got %d", report.TotalReqs)
	}
	if report.Passed != 1 {
		t.Errorf("expected 1 passed, got %d", report.Passed)
	}
	if report.Failures != 1 {
		t.Errorf("expected 1 failure (missing test file), got %d", report.Failures)
	}

	md := report.Markdown()
	if !strings.Contains(md, "project-foundation") || !strings.Contains(md, "core-market-infrastructure") {
		t.Errorf("markdown missing requirements: %s", md)
	}
	if !strings.Contains(md, "PASS") || !strings.Contains(md, "FAIL") {
		t.Errorf("markdown missing pass/fail status: %s", md)
	}

	term := report.RenderTerminal()
	if !strings.Contains(term, "Requirements Traceability Matrix") {
		t.Errorf("terminal output missing header: %s", term)
	}

	termSummary := report.RenderTerminal(true)
	if strings.Contains(termSummary, "Implementation & Component Alignment") {
		t.Errorf("summary terminal output should not contain detailed bead table: %s", termSummary)
	}
}

func TestCleanRequirementTitle(t *testing.T) {
	tests := []struct {
		reqID string
		raw   string
		want  string
	}{
		{
			reqID: "phase-1-core",
			raw:   "Domain validation: project name trimmed to 1-80 code points",
			want:  "Domain validation",
		},
		{
			reqID: "phase-1-app",
			raw:   "PostgreSQL store runs the same contract against a dedicated disposable test database",
			want:  "PostgreSQL store",
		},
		{
			reqID: "phase-2-kanban",
			raw:   "Task REST API (list, create, patch, delete) with server-owned status",
			want:  "Task REST API",
		},
		{
			reqID: "phase-3-browser",
			raw:   "Browser E2E: real clicks, typing, and selections drive the UI",
			want:  "Browser E2E",
		},
	}

	for _, tc := range tests {
		got := CleanRequirementTitle(tc.reqID, tc.raw)
		if got != tc.want {
			t.Errorf("CleanRequirementTitle(%q, %q) = %q; want %q", tc.reqID, tc.raw, got, tc.want)
		}
	}
}

func TestClassifyBead(t *testing.T) {
	cat, art, purp := ClassifyBead("taskboard/deploy/k8s/service.yaml", "tasktracker", false, "", "", "")
	if cat != CategoryInfrastructure {
		t.Errorf("expected CategoryInfrastructure, got %q", cat)
	}
	if art != "deploy/k8s/service.yaml" {
		t.Errorf("expected deploy/k8s/service.yaml, got %q", art)
	}
	if !strings.Contains(purp, "Service") {
		t.Errorf("expected Service purpose, got %q", purp)
	}

	cat, art, purp = ClassifyBead("taskboard/go.mod", "tasktracker", false, "", "", "")
	if cat != CategoryBuild {
		t.Errorf("expected CategoryBuild, got %q", cat)
	}
	if art != "go.mod" {
		t.Errorf("expected go.mod, got %q", art)
	}

	cat, art, purp = ClassifyBead("taskboard/internal/web/app.go", "tasktracker", false, "", "", "")
	if cat != CategoryComponentWeb {
		t.Errorf("expected CategoryComponentWeb, got %q", cat)
	}
	if art != "internal/web/app.go" {
		t.Errorf("expected internal/web/app.go, got %q", art)
	}

	cat, art, purp = ClassifyBead("taskboard/internal/model/model.go", "tasktracker", true, "Domain validation", "Implement taskboard/internal/model/model.go per architecture", "")
	if cat != CategoryRequirement {
		t.Errorf("expected CategoryRequirement, got %q", cat)
	}
	if !strings.Contains(purp, "Domain validation") {
		t.Errorf("expected Domain validation in purpose, got %q", purp)
	}
}
