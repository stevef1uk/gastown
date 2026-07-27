package orchestrator

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestOrchestratedForRole(t *testing.T) {
	if OrchestratedForRole(true, "witness") {
		t.Fatal("witness must not be orchestrated")
	}
	if OrchestratedForRole(true, "refinery") {
		t.Fatal("refinery must not be orchestrated")
	}
	if OrchestratedForRole(true, "mechanic") {
		t.Fatal("mechanic must not be orchestrated")
	}
	if !OrchestratedForRole(true, "mayor") {
		t.Fatal("mayor should be orchestrated")
	}
	if !OrchestratedForRole(true, "analyst") {
		t.Fatal("analyst should be orchestrated when orchestrator is running")
	}
	if OrchestratedForRole(false, "analyst") {
		t.Fatal("analyst should not be orchestrated when orchestrator is down")
	}
	if !OrchestratedForTownPolecat(true) {
		t.Fatal("hq polecat should be orchestrated")
	}
	if OrchestratedForTownPolecat(false) {
		t.Fatal("hq polecat should not be orchestrated when service down")
	}
}

func TestIsPipelineRole(t *testing.T) {
	for _, role := range []string{"mayor", "architect", "analyst", "planner", "setup", "polecat", "qa"} {
		if !IsPipelineRole(role) {
			t.Errorf("IsPipelineRole(%q) = false, want true", role)
		}
	}
	for _, role := range []string{"witness", "refinery", "mechanic", "deacon", "dog"} {
		if IsPipelineRole(role) {
			t.Errorf("IsPipelineRole(%q) = true, want false", role)
		}
	}
}

func TestAgentMatchesTask_edgeCases(t *testing.T) {
	vars := map[string]string{"rig": "mockrig"}
	if !AgentMatchesTask("any", "architect", vars) {
		t.Fatal("any should match")
	}
	if AgentMatchesTask("mockrig/qa", "architect", vars) {
		t.Fatal("wrong role suffix should not match")
	}
	if !AgentMatchesTask("mockrig/qa", "qa", vars) {
		t.Fatal("rig-qualified qa should match")
	}
	if AgentMatchesTask("qa", "qa", vars) {
		t.Fatal("bare qa should not match when workflow has rig")
	}
	if !AgentMatchesTask("mockrig/polecat", "polecat", vars) {
		t.Fatal("rig polecat should match")
	}
	if AgentMatchesTask("polecat", "polecat", vars) {
		t.Fatal("bare polecat should not match when workflow has rig")
	}
	if !AgentMatchesTask("planner", "planner", nil) {
		t.Fatal("town planner without rig var should match")
	}
	if AgentMatchesTask("planner", "setup", vars) {
		t.Fatal("planner must not claim project_setup (role setup)")
	}
	if !AgentMatchesTask("setup", "setup", vars) {
		t.Fatal("town setup agent should claim project_setup")
	}
	// Analyst is rig-scoped like architect/qa.
	if !AgentMatchesTask("mockrig/analyst", "analyst", vars) {
		t.Fatal("rig-qualified analyst should match")
	}
	if AgentMatchesTask("analyst", "analyst", vars) {
		t.Fatal("bare analyst should not match when workflow has rig")
	}
}

func readEmbeddedRigFlowPrompt(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("town", "prompts", "rig-flow", name)
	data, err := townAssets.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestRigFlowPrompts_staticURLGuidanceFromArchitecture(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"qa_review.md", "implementation.md"} {
		body := readEmbeddedRigFlowPrompt(t, name)
		if name == "qa_review.md" {
			if !strings.Contains(body, "{{qa_runtime_smoke_block}}") {
				t.Fatalf("qa_review.md must use {{qa_runtime_smoke_block}}")
			}
			continue
		}
		if !strings.Contains(body, "{{static_url_contract") {
			t.Fatalf("%s must use {{static_url_contract_*}} placeholder", name)
		}
		forbidden := []string{
			"often `/app.js`",
			"often /app.js",
			"127.0.0.1:8080/app.js",
			"not `/static/app.js` unless the server",
		}
		for _, bad := range forbidden {
			if strings.Contains(body, bad) {
				t.Fatalf("%s contains contradictory static URL example %q", name, bad)
			}
		}
	}
	qa := readEmbeddedRigFlowPrompt(t, "qa_review.md")
	if !strings.Contains(qa, "{{qa_runtime_smoke_block}}") {
		t.Fatalf("qa_review should inject profile-specific smoke block")
	}
	resolved := SubstituteVars(qa, map[string]string{
		"qa_runtime_smoke_block": RigFlowQARuntimeSmokeBlock(t.TempDir(), "rig", linkshelfLikeValidation()),
	})
	if !strings.Contains(resolved, "gt-agent") || !strings.Contains(resolved, "architecture") {
		t.Fatalf("resolved qa smoke block should describe architecture-driven smoke:\n%s", resolved)
	}
	if strings.Contains(qa, "curl -sf http://127.0.0.1:8080/app.js") {
		t.Fatal("qa_review must not hardcode /app.js smoke example")
	}
}

func TestPromptVars_includesStaticURLContractGuidance(t *testing.T) {
	t.Parallel()
	vars := DefaultWorkflowValidation().PromptVars()
	g := vars["static_url_contract_guidance"]
	if g == "" || !strings.Contains(g, "architecture.md") || !strings.Contains(g, "index.html") {
		t.Fatalf("static_url_contract_guidance: %q", g)
	}
	if vars["static_url_contract_short"] == "" {
		t.Fatal("missing static_url_contract_short")
	}
	resolved := SubstituteVars(readEmbeddedRigFlowPrompt(t, "qa_review.md"), vars)
	if strings.Contains(resolved, "{{static_url_contract") {
		t.Fatal("substitution should expand static URL placeholders")
	}
	if !strings.Contains(resolved, "architecture.md") {
		t.Fatal("resolved qa_review should include architecture guidance")
	}
}

func TestFormatPhaseTestGuards_jsdoc(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		RequiredFiles: []string{"frontend/components/Widget.test.tsx", "frontend/package.json"},
	}
	block := FormatPhaseTestGuards("", "", v)
	if !strings.Contains(block, "@jest-environment jsdom") {
		t.Fatalf("expected jsdom docblock hint for .test.tsx files:\n%s", block)
	}
	if !strings.Contains(block, "Test file conventions") {
		t.Fatalf("expected header")
	}
}

func TestFormatPhaseTestGuards_frontendSource(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		RequiredFiles: []string{"finally/frontend/package.json", "finally/frontend/tsconfig.json"},
	}
	block := FormatPhaseTestGuards("", "", v)
	if !strings.Contains(block, "@jest-environment jsdom") {
		t.Fatalf("expected jsdom hint from frontend source files (no .test.tsx):\n%s", block)
	}
}

func TestFormatPhaseTestGuards_sse(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		RequiredFiles: []string{"frontend/components/Watchlist.tsx", "frontend/package.json"},
	}
	block := FormatPhaseTestGuards("", "", v)
	if !strings.Contains(block, "EventSource") {
		t.Fatalf("expected EventSource polyfill hint for SSE component:\n%s", block)
	}
	if !strings.Contains(block, "jest.setup.ts") {
		t.Fatalf("expected jest.setup.ts mention:\n%s", block)
	}
}

func TestFormatPhaseTestGuards_goTest(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		RequiredFiles: []string{"internal/store/store_test.go", "internal/api/handler_test.go"},
	}
	block := FormatPhaseTestGuards("", "", v)
	if !strings.Contains(block, "_test.go") {
		t.Fatalf("expected Go test conventions:\n%s", block)
	}
	if strings.Contains(block, "jsdom") {
		t.Fatalf("should not mention jsdom for Go-only files:\n%s", block)
	}
}

func TestFormatPhaseTestGuards_empty(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		RequiredFiles: []string{"internal/store/store.go", "cmd/server/main.go"},
	}
	block := FormatPhaseTestGuards("", "", v)
	if block != "" {
		t.Fatalf("expected empty block for no test files:\n%s", block)
	}
}

func TestFormatPhaseTestGuards_jsdomAndGo(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		RequiredFiles: []string{
			"frontend/components/Widget.test.tsx",
			"internal/store/store_test.go",
		},
	}
	block := FormatPhaseTestGuards("", "", v)
	if !strings.Contains(block, "@jest-environment jsdom") {
		t.Fatalf("expected jsdom hint:\n%s", block)
	}
	if !strings.Contains(block, "_test.go") {
		t.Fatalf("expected Go test hint:\n%s", block)
	}
}

func TestFormatPhaseTestGuards_skipsE2E(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		RequiredFiles: []string{"e2e/login.test.ts", "playwright/smoke.test.ts"},
	}
	block := FormatPhaseTestGuards("", "", v)
	if block != "" {
		t.Fatalf("expected empty for e2e/playwright test files:\n%s", block)
	}
}

func TestRigFlowYAML_qaInstructionsMentionArchitectureStaticURLs(t *testing.T) {
	tpl := loadRigFlowTemplate(t)
	inst := tpl.States["qa_review"].Instructions
	if !strings.Contains(inst, "architecture.md") || !strings.Contains(inst, "index.html") {
		t.Fatalf("qa_review instructions: %q", inst)
	}
	if strings.Contains(inst, "/app.js") {
		t.Fatal("qa_review yaml instructions must not hardcode /app.js")
	}
}
