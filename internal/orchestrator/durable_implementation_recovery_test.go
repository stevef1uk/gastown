package orchestrator

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func recoveryFile(t *testing.T, root, path, body string) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestImplementationRecovery_HybridCompletionSurvivesRepeatedSync(t *testing.T) {
	town, rig := t.TempDir(), "hybrid"
	root := filepath.Join(town, rig, "mayor", "rig")
	files := []string{"product/frontend/src/page.tsx", "product/frontend/src/style.css", "product/frontend/package.json", "product/frontend/tsconfig.json", "product/scripts/start.sh", "product/.gitignore"}
	v := WorkflowValidation{
		LayoutRoot: "product", BeadTitleContains: "Implement product/", TestRunner: "pytest",
		RequiredFiles: files, QAVerifyCommand: "docker compose -f product/docker-compose.yml build && exit 17",
		MinImplementationFileBytes: 1, MinSubstantiveLines: 1,
	}
	bodies := []string{"export default function Page() { return <main>Dashboard</main>; }\n", "body { color: white; background: black; }\n", "{\"scripts\": {\"build\": \"next build\"}}\n", "{\"compilerOptions\": {\"strict\": true}}\n", "#!/bin/sh\nset -eu\nexit 0\n", ""}
	var closed []PlanBead
	for i, path := range files {
		if strings.HasSuffix(path, "tsconfig.json") {
			continue // A missing prerequisite must not invalidate completed siblings.
		}
		recoveryFile(t, root, path, bodies[i])
		closed = append(closed, PlanBead{ID: "b-" + filepath.Base(path), Title: "Implement " + path + " per architecture"})
	}
	setListImplementBeadsByStatusHook(t, town, rig, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "closed" {
			return closed, nil
		}
		if status == "open" {
			return []PlanBead{{ID: "b-tsconfig", Title: "Implement product/frontend/tsconfig.json per architecture"}}, nil
		}
		return nil, nil
	})
	previous := bdUpdateImplementBeadStatusHook
	bdUpdateImplementBeadStatusHook = func(_, _, id, status string) error {
		t.Fatalf("recovery changed completed bead %s to %s", id, status)
		return nil
	}
	t.Cleanup(func() { bdUpdateImplementBeadStatusHook = previous })
	// No verifier command may run, including on a new evaluator after restart.
	bin := t.TempDir()
	for _, tool := range []string{"docker", "python3", "go", "npm"} {
		if err := os.WriteFile(filepath.Join(bin, tool), []byte("#!/bin/sh\nexit 17\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	for pass := 0; pass < 3; pass++ {
		eval := NewImplementBeadVerifyEvaluator(root, v)
		for _, path := range files {
			if strings.HasSuffix(path, "tsconfig.json") {
				continue
			}
			if eval.PythonSatisfied(path) {
				t.Fatalf("Python verifier accepted non-Python path %s", path)
			}
			// Setup manifests deliberately do not auto-close through this API.
			if !IsProjectSetupArtifactPath(path, v) && !eval.VerifySatisfied(path) {
				t.Fatalf("hybrid artifact %s incorrectly failed per-file verification", path)
			}
		}
		if reopened, err := ReopenClosedImplementBeadsForMissingOpenRequired(town, rig, v); err != nil || len(reopened) != 0 {
			t.Fatalf("pass %d: reopened=%v err=%v", pass, reopened, err)
		}
		if changes, err := EnsurePlanningImplementBeads(town, rig, v); err != nil || len(changes) != 0 {
			t.Fatalf("pass %d: planning changed owners: %v, %v", pass, changes, err)
		}
		if reopened, err := reopenClosedImplementBeadsOrdered(town, rig, v, eval); err != nil || len(reopened) != 0 {
			t.Fatalf("pass %d: reopened=%v err=%v", pass, reopened, err)
		}
	}
}

func TestImplementationRecovery_OnlyActualArtifactDamageReopens(t *testing.T) {
	town, rig := t.TempDir(), "rig"
	root := filepath.Join(town, rig, "mayor", "rig")
	v := WorkflowValidation{LayoutRoot: "product", BeadTitleContains: "Implement product/", RequiredFiles: []string{"product/page.tsx", "product/.gitignore"}, MinImplementationFileBytes: 1, MinSubstantiveLines: 1}
	recoveryFile(t, root, "product/page.tsx", "export default function Page() { return <main>Dashboard</main>; }\n")
	setListImplementBeadsByStatusHook(t, town, rig, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "closed" {
			return []PlanBead{{ID: "b-page", Title: "Implement product/page.tsx per architecture"}, {ID: "b-ignore", Title: "Implement product/.gitignore per architecture"}}, nil
		}
		return nil, nil
	})
	var updates []string
	previous := bdUpdateImplementBeadStatusHook
	bdUpdateImplementBeadStatusHook = func(_, _, id, _ string) error { updates = append(updates, id); return nil }
	t.Cleanup(func() { bdUpdateImplementBeadStatusHook = previous })
	if _, err := ReopenClosedImplementBeadsForMissingOpenRequired(town, rig, v); err != nil {
		t.Fatal(err)
	}
	if strings.Join(updates, ",") != "b-ignore" {
		t.Fatalf("reopened %v; only the missing placeholder needs repair", updates)
	}
	// Filesystem failures must not turn into implementation rework.
	if err := os.Symlink("loop", filepath.Join(root, "product", "loop")); err != nil {
		t.Fatal(err)
	}
	if result := AssessImplementationArtifact(root, "product/loop", v); result.Status != ArtifactUnavailable || result.NeedsRepair() {
		t.Fatalf("filesystem failure misclassified: %+v", result)
	}
}

func TestImplementationRecovery_CanonicalOwnershipAcrossStatuses(t *testing.T) {
	town, rig := t.TempDir(), "rig"
	root := filepath.Join(town, rig, "mayor", "rig")
	v := WorkflowValidation{LayoutRoot: "product", BeadTitleContains: "Implement product/", RequiredFiles: []string{"product/page.tsx", "product/config.json"}, MinImplementationFileBytes: 1, MinSubstantiveLines: 1}
	recoveryFile(t, root, "product/page.tsx", "export default function Page() { return <main>Dashboard</main>; }\n")
	rows := map[string][]PlanBead{
		"closed":  {{ID: "b-closed", Title: "Implement product/page.tsx per architecture"}},
		"open":    {{ID: "b-open", Title: "Implement product/page.tsx per architecture"}},
		"blocked": {{ID: "b-old", Title: "Implement product/page.tsx per architecture"}, {ID: "b-config", Title: "Implement product/config.json per architecture"}},
	}
	setListImplementBeadsByStatusHook(t, town, rig, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) { return rows[status], nil })
	previous := supersedeImplementBeadHook
	supersedeImplementBeadHook = func(_, _, duplicate, canonical string) error {
		if canonical != "b-closed" {
			t.Fatalf("lost verified canonical owner: %s", canonical)
		}
		for status, beads := range rows {
			for i, bead := range beads {
				if bead.ID == duplicate {
					rows[status] = append(beads[:i], beads[i+1:]...)
					bead.CloseReason = supersededImplementReason + canonical
					rows["closed"] = append(rows["closed"], bead)
					return nil
				}
			}
		}
		t.Fatalf("unknown duplicate %s", duplicate)
		return nil
	}
	t.Cleanup(func() { supersedeImplementBeadHook = previous })
	for pass := 0; pass < 3; pass++ {
		changes, err := CanonicalizeImplementBeads(town, rig, v)
		if err != nil || (pass == 0 && len(changes) != 2) || (pass > 0 && len(changes) != 0) {
			t.Fatalf("pass %d: changes=%v err=%v", pass, changes, err)
		}
		if changes, err := EnsurePlanningImplementBeads(town, rig, v); err != nil || len(changes) != 0 {
			t.Fatalf("blocked owner was replaced: %v %v", changes, err)
		}
	}
	if err := ValidatePlanningBeadCreate(town, rig, "Implement product/config.json per architecture", v); err == nil || !strings.Contains(err.Error(), "b-config") {
		t.Fatalf("duplicate creation bypassed blocked ownership: %v", err)
	}
}

func TestDockerVerification_RigRelativePathsRunFromRigRoot(t *testing.T) {
	root := t.TempDir()
	recoveryFile(t, root, "product/docker-compose.yml", "services: {}\n")
	bin := t.TempDir()
	// Check the real cwd/path relationship rather than just comparing strings.
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\n[ \"$1\" = compose ] && [ \"$2\" = -f ] && [ -f \"$3\" ]\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	v := WorkflowValidation{LayoutRoot: "product", QAVerifyCommand: "docker compose -f product/docker-compose.yml config"}
	cmd := exec.Command("/bin/sh", "-c", DockerImplementationVerifyCommandForBead(v, root, "product/page.tsx"))
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("rig-relative verification failed: %v: %s", err, out)
	}
}

func TestImplementationCheckpoint_PreservesTargetedRework(t *testing.T) {
	for _, pending := range []bool{false, true} {
		m := NewManager(t.TempDir())
		m.LoadTemplate(&WorkflowTemplate{ID: "batch", InitialState: "worker", States: map[string]State{
			"worker": {Role: "polecat", Transitions: map[string]Transition{"checkpoint": {To: "worker"}}},
		}})
		id, err := m.StartWorkflow("batch", nil)
		if err != nil {
			t.Fatal(err)
		}
		var original *WorkflowRework
		if pending {
			original = &WorkflowRework{FromState: "test_review", Outcome: "failure", Summary: "repair only b-page"}
			m.instances[id].PendingRework = original
		}
		if next, err := m.CompleteTask(id, "checkpoint", "", "batch completed", "", nil); err != nil || next != "worker" {
			t.Fatalf("checkpoint failed: next=%s err=%v", next, err)
		}
		if m.instances[id].PendingRework != original {
			t.Fatalf("batch yield replaced targeted repair context: %+v", m.instances[id].PendingRework)
		}
	}
}
