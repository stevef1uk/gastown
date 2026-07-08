package orchestrator

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanningPlanMDNeedsRefresh_missingFile(t *testing.T) {
	town := t.TempDir()
	rig := "mockrig"
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(strings.Repeat("a", 400)), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "plan.md"), []byte("### te-old: linkshelf/go.mod\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		BeadTitleContains: "Implement ",
		RequiredFiles:     []string{"linkshelf/go.mod", "linkshelf/web/index.html"},
	}
	if !PlanningPlanMDNeedsRefresh(town, rig, v) {
		t.Fatal("expected refresh when plan missing second path and beads absent")
	}
}

func TestPlanningPlanMDNeedsRefresh_docAlignment(t *testing.T) {
	t.Parallel()
	town := t.TempDir()
	rig := "mockrig"
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	spec := "# Spec\n| GET | /api/links | 200 | — |\nmodule linkshelf\n"
	arch := "GET /api/links. Files: linkshelf/internal/store/store.go\n"
	plan := strings.Join([]string{
		"# Implementation plan",
		"## Bead map",
		"### te-1: linkshelf/internal/store/schema.go",
		"- Bead is implemented before `internal/store/store.go` in build order.",
	}, "\n")
	for name, body := range map[string]string{"SPEC.md": spec, "architecture.md": arch, "plan.md": plan} {
		if err := os.WriteFile(filepath.Join(rigDir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	v := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		RequiredFiles: []string{"linkshelf/internal/store/schema.go", "linkshelf/internal/store/store.go"},
		MinPlanBytes:  10,
	}
	if !PlanningPlanMDNeedsRefresh(town, rig, v) {
		t.Fatal("expected refresh when plan.md fails doc alignment (bare module path in prose)")
	}
}

func TestPlanningPlanMDNeedsRefresh_flatBeadMapPaths(t *testing.T) {
	t.Parallel()
	town := t.TempDir()
	rig := "mockrig"
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	plan := strings.Join([]string{
		"# Implementation plan",
		"## Bead map",
		"### te-h: linkshelf/handlers.go",
		"- Scope: flat path",
	}, "\n")
	if err := os.WriteFile(filepath.Join(rigDir, "plan.md"), []byte(plan), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		RequiredFiles: []string{"linkshelf/internal/api/handlers.go"},
	}
	if !PlanningPlanMDNeedsRefresh(town, rig, v) {
		t.Fatal("expected refresh when plan.md bead map uses flattened paths")
	}
}

func TestPadPlanningPlanMD_meetsMin(t *testing.T) {
	body := "# plan\n"
	got := padPlanningPlanMD(body, 500)
	if int64(len(got)) < 500 {
		t.Fatalf("len=%d want >= 500", len(got))
	}
}

func TestWritePlanningPlanMD_closedImplementBeads(t *testing.T) {
	town := t.TempDir()
	rig := "testgt3"
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(strings.Repeat("arch\n", 80)), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte("# Spec\nmodule linkshelf\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:        "linkshelf",
		BeadTitleContains: "Link Shelf /",
		RequiredFiles:     []string{"linkshelf/go.mod", "linkshelf/web/index.html"},
		MinPlanBytes:      50,
	}
	closed := []PlanBead{
		{ID: "te-gomod", Title: "Link Shelf /linkshelf/go.mod per architecture"},
		{ID: "te-html", Title: "Link Shelf /linkshelf/web/index.html per architecture"},
	}
	setListImplementBeadsByStatusHook(t, town, rig, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "closed" {
			return closed, nil
		}
		return nil, nil
	})
	wrote, err := WritePlanningPlanMD(town, rig, v)
	if err != nil {
		t.Fatalf("WritePlanningPlanMD with all closed beads: %v", err)
	}
	if !wrote {
		t.Fatal("expected plan.md write")
	}
	data, err := os.ReadFile(filepath.Join(rigDir, "plan.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range closed {
		if !strings.Contains(string(data), "### "+b.ID+":") {
			t.Fatalf("plan.md missing closed bead section %s:\n%s", b.ID, data)
		}
	}
}

func TestWritePlanningPlanMDWithRetry_closedPathsOnly(t *testing.T) {
	town := t.TempDir()
	rig := "testgt3"
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(strings.Repeat("arch\n", 80)), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte("# Spec\nmodule linkshelf\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:        "linkshelf",
		BeadTitleContains: "Link Shelf /",
		RequiredFiles:     []string{"linkshelf/go.mod", "linkshelf/web/index.html"},
		MinPlanBytes:      50,
	}
	closed := []PlanBead{
		{ID: "te-gomod", Title: "Link Shelf /linkshelf/go.mod per architecture"},
		{ID: "te-html", Title: "Link Shelf /linkshelf/web/index.html per architecture"},
	}
	setListImplementBeadsByStatusHook(t, town, rig, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "closed" {
			return closed, nil
		}
		return nil, nil
	})
	wrote, err := writePlanningPlanMDWithRetry(town, rig, v)
	if err != nil {
		t.Fatalf("writePlanningPlanMDWithRetry closed-only: %v", err)
	}
	if !wrote {
		t.Fatal("expected plan.md write")
	}
	if PlanningPlanMDNeedsRefresh(town, rig, v) {
		t.Fatal("expected no refresh after closed-only plan sync")
	}
}

func TestSyncPlanningArtifacts_integration(t *testing.T) {
	if _, err := exec.LookPath("bd"); err != nil {
		t.Skip("bd not in PATH")
	}
	townRoot := t.TempDir()
	rig := "mockrig"
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	beadsDir := filepath.Join(townRoot, rig, ".beads")
	for _, d := range []string{rigDir, beadsDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(townRoot, rig, ".git")); os.IsNotExist(err) {
		t.Skip("skipping integration test: bd hooks require git repo")
	}
	init := exec.Command("bd", "init")
	init.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
	init.Dir = rigDir
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("bd init: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(strings.Repeat("arch line\n", 80)), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte("# Spec\nmodule app\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:        "app",
		BeadTitleContains: "Implement app/",
		RequiredFiles:     []string{"app/go.mod", "app/main.go"},
		MinPlanBytes:      100,
	}
	planPath := filepath.Join(rigDir, "plan.md")

	logLine, err := SyncPlanningArtifacts(townRoot, rig, v, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logLine, "wrote plan.md") {
		t.Fatalf("expected wrote plan.md in log, got %q", logLine)
	}

	open, err := ListOpenImplementBeads(townRoot, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != len(requiredFilesWithCorrelatedTests(v.RequiredFiles, v)) {
		t.Fatalf("open implement beads = %d, want %d: %v", len(open), len(requiredFilesWithCorrelatedTests(v.RequiredFiles, v)), open)
	}
	if err := ValidatePlanBeads(open, filepath.Join(rigDir, "architecture.md"), v, rig); err != nil {
		t.Fatalf("ValidatePlanBeads: %v", err)
	}

	data, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range open {
		section := "### " + b.ID + ":"
		if !strings.Contains(string(data), section) {
			t.Fatalf("plan.md missing section for %s:\n%s", b.ID, data)
		}
	}
	if int64(len(data)) < EffectiveMinPlanBytes(rigDir, v) {
		t.Fatalf("plan.md too small: %d", len(data))
	}
	if PlanningPlanMDNeedsRefresh(townRoot, rig, v) {
		t.Fatal("plan.md should be fresh after sync")
	}
}

func TestOpenImplementPathMap_exactUsesRawPath(t *testing.T) {
	t.Parallel()
	town := t.TempDir()
	rig := "finally"
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Bead title includes rig-name prefix; raw extracted path = "finally/backend/pyproject.toml"
	beadTitle := "Implement finally/backend/pyproject.toml per architecture"
	setListImplementBeadsByStatusHook(t, town, rig, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "open" {
			return []PlanBead{{ID: "te-1", Title: beadTitle}}, nil
		}
		return nil, nil
	})
	// Include at least one file with 2+ slashes after stripping layout root to trigger exact mode
	v := WorkflowValidation{
		LayoutRoot:        "finally",
		BeadTitleContains: "Implement finally/",
		RequiredFiles:     []string{"finally/backend/pyproject.toml", "finally/backend/app/main.py"},
	}
	if !RequiresExactImplementPaths(v) {
		t.Fatal("test setup: expected exact paths (contains backend/app/ with 2+ slashes)")
	}
	pathToID, err := openImplementPathMap(town, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	// The raw path "finally/backend/pyproject.toml" should match required file and map to te-1
	if id := pathToID["finally/backend/pyproject.toml"]; id != "te-1" {
		t.Fatalf("expected te-1 for finally/backend/pyproject.toml, got %q; map: %v", id, pathToID)
	}
}

func TestOpenImplementPathMap_nonExactUsesNormalizedPath(t *testing.T) {
	t.Parallel()
	town := t.TempDir()
	rig := "testrig"
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Flat layout bead with rig-prefix path
	beadTitle := "Implement testrig/Dockerfile per architecture"
	setListImplementBeadsByStatusHook(t, town, rig, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "open" {
			return []PlanBead{{ID: "te-dkr", Title: beadTitle}}, nil
		}
		return nil, nil
	})
	v := WorkflowValidation{
		LayoutRoot:        ".",
		BeadTitleContains: "Implement testrig/",
		RequiredFiles:     []string{"Dockerfile"},
	}
	if RequiresExactImplementPaths(v) {
		t.Fatal("test setup: expected non-exact (flat layout)")
	}
	pathToID, err := openImplementPathMap(town, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	// Normalized path "Dockerfile" should match required file
	if id := pathToID["Dockerfile"]; id != "te-dkr" {
		t.Fatalf("expected te-dkr for Dockerfile, got %q; map: %v", id, pathToID)
	}
}

func TestValidatePlanMDBeadPathAlignment_normalizesPaths(t *testing.T) {
	t.Parallel()
	town := t.TempDir()
	rig := "finally"
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Plan.md uses paths WITH rig prefix
	planBody := strings.Join([]string{
		"# Implementation plan",
		"## Bead map",
		"### te-1: finally/backend/pyproject.toml",
		"- Scope: pyproject",
		"### te-2: finally/backend/app/main.py",
		"- Scope: main",
	}, "\n")
	if err := os.WriteFile(filepath.Join(rigDir, "plan.md"), []byte(planBody), 0644); err != nil {
		t.Fatal(err)
	}
	// Bead titles also use rig prefix — both sides normalize to same paths
	setListImplementBeadsByStatusHook(t, town, rig, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "in_progress" {
			return []PlanBead{
				{ID: "te-1", Title: "Implement finally/backend/pyproject.toml per architecture"},
			}, nil
		}
		if status == "open" {
			return []PlanBead{
				{ID: "te-2", Title: "Implement finally/backend/app/main.py per architecture"},
			}, nil
		}
		return nil, nil
	})
	v := WorkflowValidation{
		LayoutRoot:        "finally",
		BeadTitleContains: "Implement finally/",
		RequiredFiles:     []string{"finally/backend/pyproject.toml", "finally/backend/app/main.py"},
	}
	if err := ValidatePlanMDBeadPathAlignment(town, rig, v); err != nil {
		t.Fatalf("expected no alignment error with normalized paths, got: %v", err)
	}
}
