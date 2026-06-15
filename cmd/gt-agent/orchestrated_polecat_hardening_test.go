package main

import (
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestStripOrchestratedShellBackticks(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"`cd testgt3/mayor/rig && go test -count=1./linkshelf/internal/api/...`", "cd testgt3/mayor/rig && go test -count=1./linkshelf/internal/api/..."},
		{"`CMD: cd linkshelf && go test -count=1 ./internal/store/... -run 'TestInitSchema'`", "cd linkshelf && go test -count=1 ./internal/store/... -run 'TestInitSchema'"},
		{"cd linkshelf && go test -count=1 ./internal/store/...`", "cd linkshelf && go test -count=1 ./internal/store/..."},
	}
	for _, tc := range cases {
		if got := stripOrchestratedShellBackticks(tc.in); got != tc.want {
			t.Fatalf("strip(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeGoCommandTypos_countFlag(t *testing.T) {
	t.Parallel()
	cmd := "cd testgt3/mayor/rig && go test -count=1./linkshelf/internal/api/..."
	fixed, ok := normalizeGoCommandTypos(cmd)
	if !ok || !strings.Contains(fixed, "-count=1 ./") {
		t.Fatalf("got ok=%v cmd=%q", ok, fixed)
	}
}

func TestParseOrchestratedCommands_backtickWrappedVerify(t *testing.T) {
	t.Parallel()
	in := "`CMD: cd linkshelf && go mod tidy && go test -count=1 ./internal/store/... -run 'TestInitSchema'`\n"
	cmds := parseOrchestratedCommands(in)
	if len(cmds) != 1 {
		t.Fatalf("cmds = %v", cmds)
	}
	if strings.Contains(cmds[0], "`") {
		t.Fatalf("backticks remain: %q", cmds[0])
	}
	if !strings.Contains(cmds[0], "-count=1 ./") {
		t.Fatalf("want spaced -count=1, got %q", cmds[0])
	}
}

func TestNormalizeNativeEditEndLines_sixArrows(t *testing.T) {
	t.Parallel()
	in := "EDIT: linkshelf/internal/api/handlers.go\n<<<<<<< SEARCH\nx\n=======\ny\n>>>>>> REPLACE\n"
	ops := parseOrchestratedNativeEdits(in)
	if len(ops) != 1 || ops[0].search != "x" || ops[0].replace != "y" {
		t.Fatalf("ops = %+v", ops)
	}
}

func TestFormatMalformedNativeEditFeedback_doubleEquals(t *testing.T) {
	t.Parallel()
	in := "EDIT: linkshelf/internal/api/handlers.go\n<<<<<<< SEARCH\n=======\nold\n=======\nnew\n>>>>>> REPLACE\n"
	got := FormatMalformedNativeEditFeedback(in)
	if got == "" || !strings.Contains(got, "git-merge") {
		t.Fatalf("want git-merge feedback, got %q", got)
	}
}

func TestParseOrchestratedNativeEdits_fencedEditBody(t *testing.T) {
	t.Parallel()
	in := "EDIT: linkshelf/internal/api/handlers.go\n```\n<<<<<<< SEARCH\n\t\tjson.NewEncoder(w).Encode(links)\n=======\n\t\tw.Write([]byte(\"[]\"))\n>>>>>>> REPLACE\n```\n"
	ops := parseOrchestratedNativeEdits(in)
	if len(ops) != 1 || ops[0].kind != "edit" || !strings.Contains(ops[0].search, "json.NewEncoder") {
		t.Fatalf("ops = %+v", ops)
	}
}

func TestReconcileActiveImplementBeadWithQueue_clearsStale(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	v := linkshelfImplementValidation(
		"linkshelf/internal/store/schema.go",
		"linkshelf/internal/api/handlers.go",
	)
	task := implementationTask(t, "wf-reconcile", v.RequiredFiles...)
	r := newStateRunner(task, dir, rig)
	r.track.activeBead = "te-8cz"
	r.track.activeBeadPath = "linkshelf/internal/store/schema.go"
	r.track.verifyOK = true
	orchestrator.ListImplementBeadsByStatusHook = func(_, _ string, _ orchestrator.WorkflowValidation, status string) ([]orchestrator.PlanBead, error) {
		switch status {
		case "open", "in_progress":
			return []orchestrator.PlanBead{{
				ID:    "te-tua",
				Title: "Implement linkshelf/internal/api/handlers.go per architecture",
			}}, nil
		case "closed":
			return []orchestrator.PlanBead{{
				ID:    "te-8cz",
				Title: "Implement linkshelf/internal/store/schema.go per architecture",
			}}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { orchestrator.ListImplementBeadsByStatusHook = nil })

	r.reconcileActiveImplementBeadWithQueue()
	if r.track.activeBead != "te-tua" {
		t.Fatalf("want queue head as active bead, got %q", r.track.activeBead)
	}
	if r.track.verifyOK {
		t.Fatal("verifyOK should be cleared when realigning away from stale active bead")
	}
}

func TestReconcileActiveImplementBeadWithQueue_allowsBdUpdateOnHead(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	v := linkshelfImplementValidation(
		"linkshelf/internal/store/schema.go",
		"linkshelf/internal/api/handlers.go",
	)
	task := implementationTask(t, "wf-reconcile2", v.RequiredFiles...)
	r := newStateRunner(task, dir, rig)
	r.track.activeBead = "te-8cz"
	r.track.verifyOK = true
	orchestrator.ListImplementBeadsByStatusHook = func(_, _ string, _ orchestrator.WorkflowValidation, status string) ([]orchestrator.PlanBead, error) {
		switch status {
		case "open", "in_progress":
			return []orchestrator.PlanBead{{
				ID:    "te-tua",
				Title: "Implement linkshelf/internal/api/handlers.go per architecture",
			}}, nil
		case "closed":
			return []orchestrator.PlanBead{{
				ID:    "te-8cz",
				Title: "Implement linkshelf/internal/store/schema.go per architecture",
			}}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { orchestrator.ListImplementBeadsByStatusHook = nil })

	r.reconcileActiveImplementBeadWithQueue()
	if r.track.activeBead != "te-tua" {
		t.Fatalf("want queue head promoted to active, got %q", r.track.activeBead)
	}
	cmd := "export BEADS_DIR=x && cd mockrig/mayor/rig && bd update te-tua --status=in_progress"
	if err := validateImplementationCommandWithState(cmd, dir, rig, r.track.activeBead, v, false, nil, ""); err != nil {
		t.Fatalf("bd update on queue head should be allowed after reconcile: %v", err)
	}
}
