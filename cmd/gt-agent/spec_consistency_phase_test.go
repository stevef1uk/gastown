package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestValidateSpecAnalysisCommand(t *testing.T) {
	valid := []string{
		"cat SPEC.md",
		"cat testgt4/mayor/rig/SPEC.md",
		"head -n 50 SPEC.md",
		"tail -n 20 SPEC.md",
		"grep -i 'port' SPEC.md",
		"grep -n 'FASTAPI' testgt4/mayor/rig/SPEC.md",
		"wc -c SPEC.md",
		"wc -l SPEC.md",
		"test -f SPEC.md",
		"cat REQUIREMENTS.md",
		"grep 'feature' REQUIREMENTS.md",
		"diff -u REQUIREMENTS.md SPEC.md",
	}

	for _, cmd := range valid {
		if err := validateSpecAnalysisCommand(cmd, "testgt4"); err != nil {
			t.Errorf("validateSpecAnalysisCommand(%q) unexpected error: %v", cmd, err)
		}
	}

	invalid := []string{
		"rm SPEC.md",
		"cat > SPEC.md << 'EOF'\n# bad\nEOF",
		"sed -i 's/foo/bar/' SPEC.md",
		"npm run build",
		"python3 -m pytest",
		"touch foo.txt",
		"gt bd list",
	}

	for _, cmd := range invalid {
		if err := validateSpecAnalysisCommand(cmd, "testgt4"); err == nil {
			t.Errorf("validateSpecAnalysisCommand(%q) expected error, got nil", cmd)
		}
	}
}

func TestValidateSpecReviewCommand(t *testing.T) {
	valid := []string{
		"cat SPEC.md",
		"head -n 40 SPEC.md",
		"tail -n 40 SPEC.md",
		"grep '8000' SPEC.md",
		"wc -c SPEC.md",
		"sed -i 's/port 8080/port 8000/g' SPEC.md",
		"patch -p1 < fix.patch SPEC.md",
		"diff -u SPEC.md.bak SPEC.md",
		"git diff SPEC.md",
		"git status",
		"test -f SPEC.md",
		"cat > SPEC.md << 'EOF'\n# patched\nEOF",
		"gt rig spec-index --force testgt4",
		"cd testgt4/mayor/rig && gt rig spec-index testgt4",
	}

	for _, cmd := range valid {
		if err := validateSpecReviewCommand(cmd, "testgt4"); err != nil {
			t.Errorf("validateSpecReviewCommand(%q) unexpected error: %v", cmd, err)
		}
	}

	invalid := []string{
		"npm run build",
		"pytest",
		"cat > architecture.md << 'EOF'\n# arch\nEOF",
		"rm -rf *",
		"python3 main.py",
	}

	for _, cmd := range invalid {
		if err := validateSpecReviewCommand(cmd, "testgt4"); err == nil {
			t.Errorf("validateSpecReviewCommand(%q) expected error, got nil", cmd)
		} else if !strings.Contains(err.Error(), "spec review may only") {
			t.Errorf("validateSpecReviewCommand(%q) unexpected error message: %v", cmd, err)
		}
	}
}

func TestFormatWorkflowReworkBlock_SpecAnalysisHandoff(t *testing.T) {
	task := &orchestrator.Task{
		WorkflowID: "wf-test",
		State:      "spec_review",
		PendingRework: &orchestrator.WorkflowRework{
			FromState: "spec_analysis",
			AgentID:   "testgt/analyst",
			Summary:   "Found port conflict: 8080 vs 3000",
		},
	}
	block := formatWorkflowReworkBlock(task, "/tmp/town", "testgt")
	if !strings.Contains(block, "Analyst spec analysis findings") {
		t.Fatalf("expected header 'Analyst spec analysis findings', got:\n%s", block)
	}
	if !strings.Contains(block, "Found port conflict: 8080 vs 3000") {
		t.Fatalf("expected summary in block, got:\n%s", block)
	}
	if !strings.Contains(block, "Vet the Analyst's findings") {
		t.Fatalf("expected vetting instruction in block, got:\n%s", block)
	}
}
