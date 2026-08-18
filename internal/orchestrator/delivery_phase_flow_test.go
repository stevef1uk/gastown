package orchestrator

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// qaReviewPhasedTemplate is a minimal rig-flow slice for phase-advance tests (qa_review → completed | planning).
func qaReviewPhasedTemplate() *WorkflowTemplate {
	return &WorkflowTemplate{
		ID:           "rig-flow",
		InitialState: "qa_review",
		States: map[string]State{
			"qa_review": {
				Role:         "qa",
				Instructions: "review phase",
				Transitions: map[string]Transition{
					"all_passed":  {To: "completed"},
					"task_passed": {To: "implementation"},
					"failure":     {To: "implementation"},
				},
			},
			"planning": {
				Role:         "planner",
				Instructions: "plan next phase",
				Transitions: map[string]Transition{
					"success": {To: "plan_review"},
				},
			},
			"plan_review": {
				Role: "qa",
				Transitions: map[string]Transition{
					"success": {To: "implementation"},
				},
			},
			"implementation": {
				Role: "polecat",
				Transitions: map[string]Transition{
					"success": {To: "qa_review"},
				},
			},
			"completed": {Role: "mayor", Instructions: "done"},
		},
	}
}

func writeTestPhasedProfile(t *testing.T, townRoot, rig, activePhase string) {
	t.Helper()
	v := WorkflowValidation{
		LayoutRoot:            "linkshelf",
		BeadTitleContains:       "Implement ",
		MinPlanBytes:            100,
		MinArchitectureBytes:    200,
		QAVerifyCommand:         "cd linkshelf && go test ./...",
		DeliveryPhases: []DeliveryPhase{
			{
				ID:    "backend",
				Title: "Backend Implementation",
				RequiredFiles: []string{
					"linkshelf/go.mod",
					"linkshelf/internal/store/store.go",
					"linkshelf/internal/store/store_test.go",
				},
				QAVerifyCommand: "cd linkshelf && go test internal/store",
				SpecFocus:       "Backend functionality",
			},
			{
				ID:    "frontend",
				Title: "Frontend Implementation",
				RequiredFiles: []string{
					"linkshelf/web/index.html",
					"linkshelf/cmd/server/main.go",
				},
				QAVerifyCommand: "cd linkshelf && go test ./cmd/server",
				SpecFocus:       "Frontend and server wiring",
			},
		},
		ActivePhaseIDField: activePhase,
	}
	v = FinalizeDeliveryPhases(v)
	if err := WriteRigWorkflowProfile(townRoot, rig, v, "test", "high"); err != nil {
		t.Fatal(err)
	}
}

func setupPhasedRigTown(t *testing.T) (townRoot, rig, rigDir, beadsDir string) {
	t.Helper()
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd not in PATH")
	}
	townRoot = t.TempDir()
	rig = "phaserig"
	rigDir = filepath.Join(townRoot, rig, "mayor", "rig")
	beadsDir = filepath.Join(townRoot, rig, ".beads")
	for _, d := range []string{rigDir, beadsDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	// Initialize git repo so bd hooks work
	initGit := exec.Command("git", "init")
	initGit.Dir = rigDir
	initGit.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test")
	if out, err := initGit.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	init := exec.Command("bd", "init")
	init.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
	init.Dir = rigDir
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("bd init: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(strings.Repeat("# arch\n", 80)), 0644); err != nil {
		t.Fatal(err)
	}
	// Write minimal SPEC.md so orchestrator can find it
	spec := "# Test Project\n\n## Layout\n```\nlinkshelf/\n  go.mod\n  internal/store/store.go\n  internal/store/store_test.go\n  web/index.html\n  cmd/server/main.go\n```\n\n## Delivery Phases\n1. backend - Backend Implementation\n2. frontend - Frontend Implementation\n"
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	return townRoot, rig, rigDir, beadsDir
}

func loadPhasedManager(t *testing.T, townRoot, rig string) (*Manager, string) {
	t.Helper()
	m := NewManager(townRoot)
	m.LoadTemplate(qaReviewPhasedTemplate())
	id, err := m.StartWorkflow("rig-flow", map[string]string{"rig": rig})
	if err != nil {
		t.Fatal(err)
	}
	inst := m.instances[id]
	inst.CurrentState = "qa_review"
	inst.Status = "running"
	return m, id
}

func assertActivePhase(t *testing.T, townRoot, rig, want string) {
	t.Helper()
	v, ok, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatalf("load profile: ok=%v err=%v", ok, err)
	}
	if got := v.ActivePhaseID(); got != want {
		t.Fatalf("active_phase_id = %q, want %q", got, want)
	}
}

func closeAllOpenImplementBeads(t *testing.T, townRoot, rig, beadsDir, workDir string, v WorkflowValidation) {
	t.Helper()
	open, err := ListOpenImplementBeads(townRoot, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range open {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, "bd", "close", b.ID, "--reason=test")
		cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
		cmd.Dir = workDir
		if out, err := cmd.CombinedOutput(); err != nil {
			cancel()
			t.Fatalf("bd close %s: %v\n%s", b.ID, err, out)
		}
		cancel()
	}
}

func assertOpenBeadPaths(t *testing.T, townRoot, rig string, v WorkflowValidation, wantPaths []string) {
	t.Helper()
	open, err := ListOpenImplementBeads(townRoot, rig, v.ForActivePhase())
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != len(wantPaths) {
		t.Fatalf("open beads = %d want %d: %+v", len(open), len(wantPaths), open)
	}
	seen := map[string]bool{}
	for _, b := range open {
		p := ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains)
		p = NormalizeBeadPathForLayout(p, v.LayoutRoot)
		seen[p] = true
	}
	for _, want := range wantPaths {
		want = filepath.ToSlash(want)
		if !seen[want] {
			t.Fatalf("missing open bead for %q; got paths %v", want, seen)
		}
	}
}

// TestStartWorkflow_resetsStaleDeliveryPhase verifies that starting a new workflow for a
// phased rig does not inherit the previous run's completed/active phase state (which used
// to fast-forward new workflows straight to the final phase). It must reset active_phase_id
// to the first phase needing work and clear completed_phase_ids / rewound_from_phase_id.
func TestStartWorkflow_resetsStaleDeliveryPhase(t *testing.T) {
	townRoot := t.TempDir()
	rig := "resetric"
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	if err := os.MkdirAll(filepath.Join(rigDir, ".gastown"), 0755); err != nil {
		t.Fatal(err)
	}
	// Simulate a completed previous run: active on the final phase, everything completed,
	// and a stale rewound_from marker.
	writeTestPhasedProfile(t, townRoot, rig, "frontend")
	v, ok, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatalf("load profile: ok=%v err=%v", ok, err)
	}
	v.CompletedPhaseIDsField = []string{"backend", "frontend"}
	v.RewoundFromPhaseIDField = "frontend"
	if err := WriteRigWorkflowProfile(townRoot, rig, v, "test", "high"); err != nil {
		t.Fatal(err)
	}

	m := NewManager(townRoot)
	m.LoadTemplate(qaReviewPhasedTemplate())
	if _, err := m.StartWorkflow("rig-flow", map[string]string{"rig": rig}); err != nil {
		t.Fatal(err)
	}

	// No backend files exist on disk, so the first phase needing work is "backend".
	assertActivePhase(t, townRoot, rig, "backend")
	v, ok, err = LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatalf("reload profile: ok=%v err=%v", ok, err)
	}
	if len(v.CompletedPhaseIDs()) != 0 {
		t.Fatalf("completed_phase_ids = %v, want empty after new workflow start", v.CompletedPhaseIDs())
	}
	if v.RewoundFromPhaseIDField != "" {
		t.Fatalf("rewound_from_phase_id = %q, want empty", v.RewoundFromPhaseIDField)
	}
}

// TestDeliveryPhaseWorkflow_integration exercises phased delivery: profile paths → beads,
// QA all_passed advances active_phase_id and FSM to planning, final phase completes.
func TestDeliveryPhaseWorkflow_integration(t *testing.T) {
	townRoot, rig, rigDir, beadsDir := setupPhasedRigTown(t)
	// bd init installs git hooks; close may block if git remote is unavailable.
	if _, err := os.Stat(filepath.Join(rigDir, ".git")); os.IsNotExist(err) {
		t.Skip("skipping integration test: bd hooks require git repo")
	}
	writeTestPhasedProfile(t, townRoot, rig, "backend")

	backend := WorkflowValidation{
		LayoutRoot:        "linkshelf",
		BeadTitleContains: "Implement ",
		DeliveryPhases: []DeliveryPhase{
			{ID: "backend", RequiredFiles: []string{
				"linkshelf/go.mod",
				"linkshelf/internal/store/store.go",
				"linkshelf/internal/store/store_test.go",
			}},
			{ID: "frontend", RequiredFiles: []string{
				"linkshelf/web/index.html",
				"linkshelf/cmd/server/main.go",
			}},
		},
		ActivePhaseIDField: "backend",
	}
	backend = backend.ForActivePhase()

	logLine, err := SyncPlanningArtifacts(townRoot, rig, backend, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logLine, "wrote plan.md") {
		t.Fatalf("expected plan sync, got %q", logLine)
	}
	assertOpenBeadPaths(t, townRoot, rig, backend, backend.RequiredFiles)

	planData, err := os.ReadFile(filepath.Join(townRoot, rig, "mayor", "rig", "plan.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(planData), "Active delivery phase `backend`") {
		t.Fatalf("plan.md should name active phase:\n%s", planData)
	}

	// QA all_passed requires zero open implement beads for the active phase.
	closeAllOpenImplementBeads(t, townRoot, rig, beadsDir, rigDir, backend)

	m, wfID := loadPhasedManager(t, townRoot, rig)
	next, err := m.CompleteTask(wfID, "all_passed", rig+"/qa", "backend phase green", "")
	if err != nil {
		t.Fatal(err)
	}
	if next != "planning" {
		t.Fatalf("after backend QA all_passed: next=%q want planning", next)
	}
	inst := m.instances[wfID]
	if inst.Status != "running" {
		t.Fatalf("status = %q want running (not completed mid-pipeline)", inst.Status)
	}
	if inst.PendingRework == nil || !strings.Contains(inst.PendingRework.Feedback, "`frontend`") {
		t.Fatalf("expected planner rework for next phase, got %+v", inst.PendingRework)
	}

	assertActivePhase(t, townRoot, rig, "frontend")
	frontend, ok, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatal(err)
	}
	assertOpenBeadPaths(t, townRoot, rig, frontend, []string{
		"linkshelf/web/index.html",
		"linkshelf/cmd/server/main.go",
	})

	// Verify profile state after advance
	if !slices.Equal(frontend.CompletedPhaseIDs(), []string{"backend"}) {
		t.Fatalf("completed_phase_ids = %v want [backend]", frontend.CompletedPhaseIDs())
	}
	if frontend.RewoundFromPhaseIDField != "" {
		t.Fatalf("rewound_from_phase_id = %q want empty", frontend.RewoundFromPhaseIDField)
	}

	// Planner finishes; skip to QA on last phase.
	inst.CurrentState = "qa_review"
	next, err = m.CompleteTask(wfID, "all_passed", rig+"/qa", "frontend phase green", "")
	if err != nil {
		t.Fatal(err)
	}
	if next != "completed" {
		t.Fatalf("after final phase QA all_passed: next=%q want completed", next)
	}
	if inst.Status != "completed" {
		t.Fatalf("status = %q want completed", inst.Status)
	}
	assertActivePhase(t, townRoot, rig, "frontend")
}

// TestTryAdvanceDeliveryPhaseAfterQA_integration verifies phase advance + bead/plan sync without the manager.
func TestTryAdvanceDeliveryPhaseAfterQA_integration(t *testing.T) {
	townRoot, rig, rigDir, beadsDir := setupPhasedRigTown(t)
	if _, err := os.Stat(filepath.Join(townRoot, rig, ".git")); os.IsNotExist(err) {
		t.Skip("skipping integration test: bd hooks require git repo")
	}
	writeTestPhasedProfile(t, townRoot, rig, "backend")

	v, ok, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if _, err := SyncPlanningArtifacts(townRoot, rig, v.ForActivePhase(), true); err != nil {
		t.Fatal(err)
	}
	closeAllOpenImplementBeads(t, townRoot, rig, beadsDir, rigDir, v.ForActivePhase())

	redirected, from, to, logLine, err := TryAdvanceDeliveryPhaseAfterQA(townRoot, rig)
	if err != nil {
		t.Fatal(err)
	}
	if !redirected || from != "backend" || to != "frontend" {
		t.Fatalf("redirected=%v from=%q to=%q log=%q", redirected, from, to, logLine)
	}
	assertActivePhase(t, townRoot, rig, "frontend")
	assertOpenBeadPaths(t, townRoot, rig, v, []string{
		"linkshelf/web/index.html",
		"linkshelf/cmd/server/main.go",
	})

	redirected, _, _, _, err = TryAdvanceDeliveryPhaseAfterQA(townRoot, rig)
	if err != nil {
		t.Fatal(err)
	}
	if redirected {
		t.Fatal("expected no redirect on last phase")
	}
}

// TestManager_CompleteTask_qaAllPassed_noPhasesGoesCompleted ensures unphased rigs still complete normally.
func TestManager_CompleteTask_qaAllPassed_noPhasesGoesCompleted(t *testing.T) {
	townRoot := t.TempDir()
	rig := "flatrig"
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	if err := os.MkdirAll(filepath.Join(rigDir, ".gastown"), 0755); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:        "app",
		BeadTitleContains: "Implement ",
		RequiredFiles:     []string{"app/main.go"},
		QAVerifyCommand:   "cd app && go test ./...",
	}
	if err := WriteRigWorkflowProfile(townRoot, rig, v, "test", "high"); err != nil {
		t.Fatal(err)
	}

	m, wfID := loadPhasedManager(t, townRoot, rig)
	next, err := m.CompleteTask(wfID, "all_passed", rig+"/qa", "done", "")
	if err != nil {
		t.Fatal(err)
	}
	if next != "completed" {
		t.Fatalf("unphased rig: next=%q want completed", next)
	}
	if m.instances[wfID].Status != "completed" {
		t.Fatalf("status = %q", m.instances[wfID].Status)
	}
}

// TestTryAdvanceDeliveryPhaseAfterQA_succeedsWithBdErrors verifies that phase advance
// returns redirected=true even when PruneOpenImplementBeadsOutsideRequired or
// SyncPlanningArtifacts fail (e.g. Dolt unreachable). The phase was already advanced
// on disk by SetRigActivePhase, so the workflow must continue at planning, not complete.
func TestTryAdvanceDeliveryPhaseAfterQA_succeedsWithBdErrors(t *testing.T) {
	townRoot := t.TempDir()
	rig := "errrig"
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	// No bd init, no git repo — bd commands will fail, but SetRigActivePhase (JSON write) should still work.
	writeTestPhasedProfile(t, townRoot, rig, "backend")

	// Verify setup
	v, ok, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		t.Fatalf("load profile: ok=%v err=%v", ok, err)
	}
	if !v.HasPhasedDelivery() {
		t.Fatal("expected phased delivery")
	}
	if v.ActivePhaseID() != "backend" {
		t.Fatalf("active phase = %q, want backend", v.ActivePhaseID())
	}

	// Mark current phase as completed before advancing (required by TryAdvanceDeliveryPhaseAfterQA)
	if err := AddRigCompletedPhase(townRoot, rig, "backend"); err != nil {
		t.Fatalf("mark phase completed: %v", err)
	}

	// TryAdvanceDeliveryPhaseAfterQA should succeed even though bd operations will fail
	redirected, fromID, toID, logLine, err := TryAdvanceDeliveryPhaseAfterQA(townRoot, rig)

	// The key assertion: redirected must be true because SetRigActivePhase succeeded
	if !redirected {
		t.Fatalf("expected redirected=true (phase advanced on disk), got false; err=%v logLine=%q", err, logLine)
	}
	if fromID != "backend" {
		t.Fatalf("fromID = %q, want backend", fromID)
	}
	if toID != "frontend" {
		t.Fatalf("toID = %q, want frontend", toID)
	}

	// Verify the phase was actually persisted
	assertActivePhase(t, townRoot, rig, "frontend")

	// Log line should contain the phase advance message
	if !strings.Contains(logLine, "delivery phase advanced backend → frontend") {
		t.Fatalf("logLine = %q", logLine)
	}
}
