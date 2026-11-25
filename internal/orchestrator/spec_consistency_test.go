package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestRigFlowSpecConsistencyTransitions(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("town", "templates", "rig-flow.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var tpl WorkflowTemplate
	if err := yaml.Unmarshal(data, &tpl); err != nil {
		t.Fatalf("parse rig-flow.yaml: %v", err)
	}

	// 1. Kickoff transitions to spec_analysis on success
	kickoff, ok := tpl.States["kickoff"]
	if !ok {
		t.Fatal("missing kickoff state")
	}
	if got := kickoff.Transitions["success"].To; got != "spec_analysis" {
		t.Errorf("kickoff success -> %q, want spec_analysis", got)
	}

	// 2. spec_analysis exists and has role analyst
	specAnalysis, ok := tpl.States["spec_analysis"]
	if !ok {
		t.Fatal("missing spec_analysis state")
	}
	if specAnalysis.Role != "analyst" {
		t.Errorf("spec_analysis role = %q, want analyst", specAnalysis.Role)
	}
	if got := specAnalysis.Transitions["success"].To; got != "spec_review" {
		t.Errorf("spec_analysis success -> %q, want spec_review", got)
	}
	if got := specAnalysis.Transitions["failure"].To; got != "spec_analysis" {
		t.Errorf("spec_analysis failure -> %q, want spec_analysis", got)
	}

	// 3. spec_review exists and has role qa
	specReview, ok := tpl.States["spec_review"]
	if !ok {
		t.Fatal("missing spec_review state")
	}
	if specReview.Role != "qa" {
		t.Errorf("spec_review role = %q, want qa", specReview.Role)
	}
	if got := specReview.Transitions["success"].To; got != "design" {
		t.Errorf("spec_review success -> %q, want design", got)
	}
	if got := specReview.Transitions["failure"].To; got != "spec_analysis" {
		t.Errorf("spec_review failure -> %q, want spec_analysis", got)
	}

	// 4. Rework feedback routes spec_review->spec_analysis
	fbKey := "spec_review->spec_analysis"
	builderName, ok := tpl.ReworkFeedback[fbKey]
	if !ok {
		t.Fatalf("missing rework_feedback for %q", fbKey)
	}
	if builderName != "spec_review_to_analyst" {
		t.Errorf("rework_feedback[%q] = %q, want spec_review_to_analyst", fbKey, builderName)
	}

	// 5. Test feedback generation
	fb := prepareSpecReviewToAnalystFeedback("Port mismatch between §3 and §8", "Details: frontend expects 3000 but backend serves 8000")
	if !strings.Contains(fb, "QA rejected SPEC consistency findings") || !strings.Contains(fb, "Port mismatch") {
		t.Errorf("unexpected feedback output: %s", fb)
	}
}
