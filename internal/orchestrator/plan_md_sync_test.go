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
	if len(open) != len(v.RequiredFiles) {
		t.Fatalf("open implement beads = %d, want %d: %v", len(open), len(v.RequiredFiles), open)
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
