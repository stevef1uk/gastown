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
	got := PlanExcerptForBead(dir, rig, "linkshelf/internal/store/store_test.go")
	if !strings.Contains(got, "te-2") || !strings.Contains(got, "TestAddLink") {
		t.Fatalf("excerpt = %q", got)
	}
}
