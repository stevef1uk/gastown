package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanExcerptForBead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "rig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	plan := `## Bead map

### te-1: linkshelf/internal/store/store.go
- Scope: store layer
- Acceptance: AddLink persists rows

### te-2: linkshelf/internal/store/store_test.go
- Acceptance: TestAddLink covers empty title error
`
	if err := os.WriteFile(filepath.Join(rigDir, "plan.md"), []byte(plan), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		RequiredFiles: []string{"linkshelf/internal/store/store.go", "linkshelf/internal/store/store_test.go"},
	}
	got := PlanExcerptForBead(dir, rig, "linkshelf/internal/store/store_test.go", v)
	if !strings.Contains(got, "te-2") || !strings.Contains(got, "TestAddLink") {
		t.Fatalf("excerpt = %q", got)
	}
}

func TestPlanExcerptForBead_exactPathsRejectsFlatSection(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "rig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	plan := `## Bead map

### te-5ie: linkshelf/handlers.go
- Scope: flat wrong path
- Acceptance: must not attach to nested bead

### te-ok: linkshelf/internal/api/handlers.go
- Scope: correct path
- Acceptance: use this section
`
	if err := os.WriteFile(filepath.Join(rigDir, "plan.md"), []byte(plan), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot: "linkshelf",
		RequiredFiles: []string{
			"linkshelf/internal/api/handlers.go",
		},
	}
	got := PlanExcerptForBead(dir, rig, "linkshelf/internal/api/handlers.go", v)
	if strings.Contains(got, "flat wrong path") {
		t.Fatalf("flat plan section must not match nested bead, got %q", got)
	}
	if !strings.Contains(got, "correct path") {
		t.Fatalf("expected nested section, got %q", got)
	}
}
