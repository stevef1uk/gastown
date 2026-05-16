package specprofile

import (
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestFormatOperatorWorkflowProfileNotice(t *testing.T) {
	p := &ProfileFile{
		Version:    1,
		Confidence: "high",
		Validation: orchestrator.WorkflowValidation{
			BeadTitleContains: "Implement api/",
			QAVerifyCommand:    "pytest -q",
			TestRunner:        "pytest",
			RequiredFiles:     []string{"a.py", "b.py"},
			SpecSummary:       strings.Repeat("x", 500),
		},
	}
	out := FormatOperatorWorkflowProfileNotice("/town/foo/myrig/mayor/rig/.gastown/workflow-profile.json", "myrig", p)
	if !strings.Contains(out, "Workflow profile") {
		t.Fatal(out)
	}
	if !strings.Contains(out, "gt rig spec-index myrig --force") {
		t.Fatal(out)
	}
	if !strings.Contains(out, "pytest -q") {
		t.Fatal(out)
	}
	if strings.Count(out, "x") < specSummaryMaxRunes {
		t.Fatal("expected truncated spec summary")
	}
}
