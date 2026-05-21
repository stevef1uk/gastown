package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatPlanAcceptanceChecklist_mergesPlanAndDefaults(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "rig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	plan := `### te-1: linkshelf/internal/store/store_test.go
- Acceptance: TestAddLink rejects empty title
- Scope: store tests
`
	if err := os.WriteFile(filepath.Join(rigDir, "plan.md"), []byte(plan), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{LayoutRoot: "linkshelf", RequiredFiles: []string{"linkshelf/internal/store/store_test.go"}}
	got := FormatPlanAcceptanceChecklist(dir, rig, "linkshelf/internal/store/store_test.go", v)
	for _, want := range []string{
		"### Acceptance checklist",
		"TestAddLink rejects empty title",
		"Unit tests assert **functional requirements**",
		"Verify** from the Next bead",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

func TestPromptContextBlock_implementBeadContext_includesAcceptanceChecklist(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "testrig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	if err := os.MkdirAll(filepath.Join(rigDir, "linkshelf", "internal", "store"), 0755); err != nil {
		t.Fatal(err)
	}
	plan := `### te-1: linkshelf/internal/store/store.go
- Acceptance: AddLink persists rows
`
	if err := os.WriteFile(filepath.Join(rigDir, "plan.md"), []byte(plan), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		BeadTitleContains: "Implement ",
		LayoutRoot:        "linkshelf",
		TestRunner:        "go",
		RequiredFiles:     []string{"linkshelf/internal/store/store.go"},
	}
	prev := nextOpenImplementBeadHook
	nextOpenImplementBeadHook = func(_, _ string, _ WorkflowValidation) (*PlanBead, error) {
		return &PlanBead{ID: "te-1", Title: "Implement linkshelf/internal/store/store.go per architecture"}, nil
	}
	t.Cleanup(func() { nextOpenImplementBeadHook = prev })

	got := PromptContextBlock("implement_bead_context", dir, rig, v)
	if !strings.Contains(got, "### Acceptance checklist") || !strings.Contains(got, "AddLink persists rows") {
		t.Fatalf("missing checklist in:\n%s", got)
	}
}

func TestAcceptanceBulletsFromPlanExcerpt_skipsScope(t *testing.T) {
	t.Parallel()
	excerpt := `### te-1: path/foo.go
- Scope: internal
- Acceptance: must compile
- also a bullet
`
	got := acceptanceBulletsFromPlanExcerpt(excerpt)
	if len(got) != 2 {
		t.Fatalf("bullets = %v", got)
	}
	if got[0] != "must compile" {
		t.Fatalf("first = %q", got[0])
	}
}
