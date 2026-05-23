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
	if !OrchestratedForTownPolecat(true) {
		t.Fatal("hq polecat should be orchestrated")
	}
	if OrchestratedForTownPolecat(false) {
		t.Fatal("hq polecat should not be orchestrated when service down")
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
	if !strings.Contains(qa, "gt-agent **rewrites**") || !strings.Contains(qa, "architecture.md") {
		t.Fatalf("qa_review should describe architecture-driven smoke:\n%s", qa)
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
