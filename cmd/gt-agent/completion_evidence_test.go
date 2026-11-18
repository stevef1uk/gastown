package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func completionEvidenceFixture(t *testing.T) *stateRunner {
	t.Helper()
	town, rig := t.TempDir(), "hybrid"
	r := newStateRunner(&orchestrator.Task{WorkflowID: "wf-evidence", State: "implementation", Hooks: orchestrator.StateHooks{Track: "implementation", NoProgressLimit: 3}}, town, rig)
	r.v = orchestrator.WorkflowValidation{
		LayoutRoot: "product", BeadTitleContains: "Implement product/", TestRunner: "pytest",
		RequiredFiles:              []string{"product/page.tsx", "product/package.json"},
		QAVerifyCommand:            "docker compose -f product/docker-compose.yml build",
		MinImplementationFileBytes: 1, MinSubstantiveLines: 1,
	}
	r.implProgress = newImplementationProgress(r.task.WorkflowID, r.task.State, rig)
	r.track.activeBead, r.track.activeBeadPath = "b-page", "product/page.tsx"
	root := filepath.Join(rigMayorRigDir(town, rig), "product")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "page.tsx"), []byte("export default function Page() { return <main>Dashboard</main>; }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCompletionEvidence_SurvivesRestartButNotChangedInputs(t *testing.T) {
	r := completionEvidenceFixture(t)
	r.implProgress.mark(implVerifyKey("b-page"))
	r.recordImplementationEvidence("b-page")
	if !r.implementationEvidenceCurrent("b-page") {
		t.Fatal("failed to record verification evidence")
	}
	if err := saveImplementationProgress(r.townRoot, r.rig, r.implProgress); err != nil {
		t.Fatal(err)
	}
	r.implProgress = loadImplementationProgress(r.townRoot, r.rig, r.task.WorkflowID, r.task.State)
	r.clearStaleImplementationVerifyMilestoneOnResume()
	if !r.track.verifyOK {
		t.Fatal("unchanged verification was not restored on restart")
	}
	// A previously missing prerequisite appearing changes verification inputs.
	manifest := filepath.Join(rigMayorRigDir(r.townRoot, r.rig), "product", "package.json")
	if err := os.WriteFile(manifest, []byte("{\"scripts\":{\"build\":\"next build\"}}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if r.implementationEvidenceCurrent("b-page") {
		t.Fatal("dependency changes did not invalidate verification evidence")
	}
	r.recordImplementationEvidence("b-page")
	r.v.QAVerifyCommand = "different acceptance contract"
	if r.implementationEvidenceCurrent("b-page") {
		t.Fatal("contract changes did not invalidate verification evidence")
	}
}

func TestCompletionEvidence_ReopenLoopIsDurableAndBounded(t *testing.T) {
	r := completionEvidenceFixture(t)
	previous := orchestrator.ListImplementBeadsByStatusHook
	orchestrator.ListImplementBeadsByStatusHook = func(_, _ string, _ orchestrator.WorkflowValidation, status string) ([]orchestrator.PlanBead, error) {
		if status == "open" {
			return []orchestrator.PlanBead{{ID: "b-page", Title: "Implement product/page.tsx per architecture"}}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { orchestrator.ListImplementBeadsByStatusHook = previous })
	for pass := 1; pass <= 3; pass++ {
		r.implProgress.mark(implClosedKey("b-page"))
		r.recordImplementationEvidence("b-page")
		r.scrubStaleImplementationProgressMilestones()
		if r.implProgress.done(implClosedKey("b-page")) {
			t.Fatal("open bead retained closed credit")
		}
		if count := r.implProgress.ReopenAttempts["product/page.tsx"].Count; count != pass {
			t.Fatalf("reopens=%d want %d", count, pass)
		}
		if err := r.completionChurnError(); (err != nil) != (pass == 3) {
			t.Fatalf("pass %d: %v", pass, err)
		}
		// Scrubbing twice without another successful close is not another cycle.
		r.scrubStaleImplementationProgressMilestones()
		r.implProgress = loadImplementationProgress(r.townRoot, r.rig, r.task.WorkflowID, r.task.State)
	}
	if err := r.completionChurnError(); err == nil {
		t.Fatal("restart lost the churn guard")
	}
	page := filepath.Join(rigMayorRigDir(r.townRoot, r.rig), "product", "page.tsx")
	if err := os.WriteFile(page, []byte("export default function Page() { return <main>Repaired dashboard</main>; }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := r.completionChurnError(); err != nil {
		t.Fatalf("actual input changes must allow a new repair attempt: %v", err)
	}
}

func TestCompletionEvidence_LegacyReopenCountDoesNotBlockRecovery(t *testing.T) {
	r := completionEvidenceFixture(t)
	previous := orchestrator.ListImplementBeadsByStatusHook
	orchestrator.ListImplementBeadsByStatusHook = func(_, _ string, _ orchestrator.WorkflowValidation, status string) ([]orchestrator.PlanBead, error) {
		if status == "open" {
			return []orchestrator.PlanBead{{ID: "b-page", Title: "Implement product/page.tsx per architecture"}}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { orchestrator.ListImplementBeadsByStatusHook = previous })

	r.recordImplementationEvidence("b-page")
	r.implProgress.ReopenAttempts = map[string]completionReopens{
		"product/page.tsx": {
			Fingerprint: r.implProgress.Evidence["b-page"],
			Count:       r.hooks.NoProgressLimit,
		},
	}
	if err := r.completionChurnError(); err != nil {
		t.Fatalf("legacy reopen count must not block recovery: %v", err)
	}

	r.recordCompletionReopen("b-page")
	reopens := r.implProgress.ReopenAttempts["product/page.tsx"]
	if reopens.Version != completionReopenVersion || reopens.Count != 1 {
		t.Fatalf("legacy count was not restarted under current version: %+v", reopens)
	}
}
