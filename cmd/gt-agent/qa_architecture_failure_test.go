package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func webServerQAValidation() orchestrator.WorkflowValidation {
	return orchestrator.WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go test ./...",
		RequiredFiles: []string{
			"linkshelf/web/index.html",
			"linkshelf/cmd/server/main.go",
			"linkshelf/internal/api/handlers.go",
		},
	}
}

func TestValidateQAArtifacts_architectureFailure_scenario(t *testing.T) {
	v := webServerQAValidation()
	dir := t.TempDir()
	rig := "testrig"
	if !orchestrator.WorkflowNeedsRuntimeSmoke(dir, rig, v) {
		t.Fatal("test profile should require runtime smoke")
	}
	rigDir := rigMayorRigDir(dir, rig)
	writeLinkshelfArchitecture(t, rigDir, false)
	layout := filepath.Join(rigDir, "linkshelf")
	for _, rel := range v.RequiredFiles {
		p := filepath.Join(rigDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		body := qaScenarioFileBody(rel)
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	_ = layout

	// Scenario: unittest green, smoke failed (hadCmdFailure), code present — escalate to architect.
	err := validateQAArtifacts(dir, rig, "architecture_failure", true, true, true, false, false, v)
	if err != nil {
		t.Fatalf("architecture_failure should be allowed: %v", err)
	}

	// Wrong outcome: failure when tests pass but smoke failed.
	err = validateQAArtifacts(dir, rig, "failure", true, true, true, false, false, v)
	if err == nil || !strings.Contains(err.Error(), "architecture_failure") {
		t.Fatalf("failure should steer to architecture_failure, got %v", err)
	}

	// Cannot escalate when unit tests failed.
	err = validateQAArtifacts(dir, rig, "architecture_failure", false, true, false, false, false, v)
	if err == nil || !strings.Contains(err.Error(), "architecture_failure requires green") {
		t.Fatalf("want unittest guard, got %v", err)
	}

	// Cannot escalate when smoke passed.
	err = validateQAArtifacts(dir, rig, "architecture_failure", false, true, true, true, false, v)
	if err == nil || !strings.Contains(err.Error(), "all_passed") {
		t.Fatalf("want smoke-pass guard, got %v", err)
	}
}

func qaScenarioFileBody(rel string) string {
	switch {
	case strings.HasSuffix(rel, ".html"):
		return strings.Repeat("<!DOCTYPE html><html><body><section id=\"items\"><a href=\"/#items\">items</a><p>QA scenario fixture with enough bytes.</p></section></body></html>\n", 5)
	case strings.HasSuffix(rel, "main.go"):
		return "package main\n\nimport \"net/http\"\n\nfunc main() {\n\thttp.HandleFunc(\"/\", func(w http.ResponseWriter, r *http.Request) {})\n\thttp.ListenAndServe(\":8080\", nil)\n}\n"
	default:
		return "package api\n\nimport \"net/http\"\n\nfunc List(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }\nfunc Create(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusCreated) }\n"
	}
}

func TestWorkflowReworkHints_qaArchitectureFailure(t *testing.T) {
	got := workflowReworkHints("qa_review", "design", "testrig", "POST /api/items 405", webServerQAValidation())
	if !strings.Contains(got, "architecture rework") {
		t.Fatalf("want architect hints: %q", got)
	}
}
