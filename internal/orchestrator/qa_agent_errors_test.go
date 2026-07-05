package orchestrator

import (
	"strings"
	"testing"
)

func TestIsQAAgentShellError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		text string
		want bool
	}{
		{"exit status 2, can't cd to linkshelf", true},
		{"command not found", true},
		{"missing handler for /api/links", false},
		{"404 on GET /api/links", false},
	}
	for _, tc := range cases {
		if got := IsQAAgentShellError(tc.text); got != tc.want {
			t.Errorf("IsQAAgentShellError(%q) = %v want %v", tc.text, got, tc.want)
		}
	}
}

func TestQAFailureRequiresImplementationRework_excludesShellError(t *testing.T) {
	t.Parallel()
	if qaFailureRequiresImplementationRework("exit status 2, can't cd to linkshelf") {
		t.Fatal("shell/cd errors must not trigger bead reopen rework")
	}
}

func TestPrepareQAReviewToImplementationFeedback_shellError(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:      "linkshelf",
		QAVerifyCommand: "cd linkshelf && go mod download",
		RequiredFiles:   []string{"linkshelf/go.mod"},
		ActivePhaseIDField: "go-module",
		DeliveryPhases: []DeliveryPhase{
			{ID: "go-module", RequiredFiles: []string{"linkshelf/go.mod"}, QAVerifyCommand: "cd linkshelf && go mod download"},
		},
	}
	out := prepareQAReviewToImplementationFeedback("exit status 2, can't cd to linkshelf", "", v.ForActivePhase())
	if out == "" {
		t.Fatal("expected feedback")
	}
	if !strings.Contains(out, "wrong working directory") || !strings.Contains(out, "success") {
		t.Fatalf("want shell-error guidance, got: %s", out)
	}
}
