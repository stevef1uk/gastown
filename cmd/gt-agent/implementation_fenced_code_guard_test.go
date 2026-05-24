package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

const handlersTutorialTurn = `CMD: export BEADS_DIR=$GT_ROOT/testgt3/.beads && cd testgt3/mayor/rig && bd update te-de1 --status=in_progress
READ: linkshelf/internal/api/handlers.go
` + "```bash\nEDIT: linkshelf/internal/api/handlers.go\n```\n" + `
` + "```go\npackage api\n\nimport \"net/http\"\n\nfunc Register(mux *http.ServeMux) {}\n```\n" + `
CMD: cd linkshelf && go test -count=1 ./internal/api/...
`

func TestResponseLooksLikeMarkdownTutorialImplementation_handlersTurn(t *testing.T) {
	t.Parallel()
	if !responseLooksLikeMarkdownTutorialImplementation(handlersTutorialTurn) {
		t.Fatal("expected tutorial turn to match")
	}
	if responseHasSubstantiveNativeWriteOrEdit(handlersTutorialTurn) {
		t.Fatal("stub EDIT must not count as substantive")
	}
}

func TestResponseLooksLikeMarkdownTutorialImplementation_realWrite(t *testing.T) {
	t.Parallel()
	in := "WRITE: linkshelf/internal/api/handlers.go\npackage api\n\nfunc F() {}\n"
	if responseLooksLikeMarkdownTutorialImplementation(in) {
		t.Fatal("real WRITE should not match tutorial pattern")
	}
}

func TestValidateImplementationFencedCodeGuard_rejectsVerify(t *testing.T) {
	t.Parallel()
	task := &orchestrator.Task{
		State: "implementation",
		Hooks: orchestrator.StateHooks{Track: "implementation", CmdGuard: "implementation"},
		Validation: orchestrator.WorkflowValidation{
			LayoutRoot:        "linkshelf",
			QAVerifyCommand:   "cd linkshelf && go test ./...",
			BeadTitleContains: "Implement linkshelf/",
			RequiredFiles:     []string{"linkshelf/internal/api/handlers.go"},
		},
	}
	r := newStateRunner(task, t.TempDir(), "testgt3")
	r.track.activeBead = "te-de1"
	r.turnResponse = handlersTutorialTurn
	r.turnHadSuccessfulNative = false
	cmd := "cd linkshelf && go test -count=1 ./internal/api/..."
	err := r.validateImplementationFencedCodeGuard(cmd)
	if err == nil || !strings.Contains(err.Error(), "markdown") {
		t.Fatalf("got %v", err)
	}
}

func TestProcessOrchestratedTools_tutorialTurnRejectsVerify(t *testing.T) {
	t.Parallel()
	task := &orchestrator.Task{
		State: "implementation",
		Hooks: orchestrator.StateHooks{
			Track:           "implementation",
			CmdGuard:        "implementation",
			NativeEditTools: true,
		},
		Validation: orchestrator.WorkflowValidation{
			LayoutRoot:        "linkshelf",
			QAVerifyCommand:   "cd linkshelf && go test ./...",
			BeadTitleContains: "Implement linkshelf/",
		},
	}
	r := newStateRunner(task, t.TempDir(), "testgt3")
	r.track.activeBead = "te-de1"
	var combined strings.Builder
	_, _, cmdCount := r.processOrchestratedTools(handlersTutorialTurn, "sess", &combined)
	if cmdCount == 0 {
		t.Fatal("expected CMD blocks")
	}
	fb := combined.String()
	if !strings.Contains(fb, "markdown") || !strings.Contains(fb, "WRITE:") {
		t.Fatalf("expected fenced-code feedback, got:\n%s", fb)
	}
	if !strings.Contains(fb, "REJECTED") && !strings.Contains(fb, "Rejected") {
		// verify rejection uses Command REJECTED from validateCommand
		if !strings.Contains(fb, "not written to disk") {
			t.Fatalf("expected verify rejection in feedback:\n%s", fb)
		}
	}
}
