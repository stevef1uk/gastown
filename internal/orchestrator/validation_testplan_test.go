package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFinStyleTestPlan(t *testing.T, rigDir string) string {
	t.Helper()
	plan := `# Test Plan — backend-core

## Requirements → tests

### store-and-api
Requirement: Store unit coverage (validation boundaries, DESC ordering, non-nil empty list, delete-missing-id error)
Level: unit
Test file: linkshelf/internal/store/store_test.go
Bead ID: fin-3b1

### backend-core
Requirement: HTTP contract coverage (200/201/204/400/404, JSON error shapes, static path traversal rejection)
Level: integration
Test file: linkshelf/internal/api/handlers_test.go
Bead ID: fin-szv

### future-phase
Test file: linkshelf/not_beaded_yet_test.go
`
	path := filepath.Join(rigDir, "TEST_PLAN.md")
	if err := os.WriteFile(path, []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func finPhases() WorkflowValidation {
	return WorkflowValidation{
		LayoutRoot:    "linkshelf",
		RequiredFiles: []string{"linkshelf/internal/store/schema.go", "linkshelf/internal/store/store.go", "linkshelf/internal/api/handlers.go"},
		DeliveryPhases: []DeliveryPhase{
			{ID: "store-and-api", RequiredFiles: []string{"linkshelf/internal/store/schema.go", "linkshelf/internal/store/store.go", "linkshelf/internal/api/handlers.go"}},
			{ID: "cmd-server", RequiredFiles: []string{"linkshelf/cmd/server/main.go"}},
			{ID: "e2e-tests", RequiredFiles: []string{"linkshelf/web/tests/app.spec.ts"}},
		},
	}
}

func TestClampProfileValidationForRig_mergesTestPlanTestFiles(t *testing.T) {
	town := t.TempDir()
	rigDir := filepath.Join(town, "fin", "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFinStyleTestPlan(t, rigDir)

	got := ClampProfileValidationForRig(town, "fin", finPhases())

	findPhase := func(id string) *DeliveryPhase {
		for i := range got.DeliveryPhases {
			if got.DeliveryPhases[i].ID == id {
				return &got.DeliveryPhases[i]
			}
		}
		t.Fatalf("phase %q missing", id)
		return nil
	}

	store := findPhase("store-and-api")
	assertHas := func(files []string, want string) {
		for _, f := range files {
			if f == want {
				return
			}
		}
		t.Fatalf("%q not in %v", want, files)
	}
	assertHas(store.RequiredFiles, "linkshelf/internal/store/store_test.go")

	union := map[string]bool{}
	for _, f := range got.UnionRequiredFiles() {
		union[f] = true
	}
	assertHas(got.RequiredFiles, "linkshelf/internal/api/handlers_test.go")
	if !union["linkshelf/internal/store/store_test.go"] {
		t.Fatalf("store_test.go missing from union: %v", got.UnionRequiredFiles())
	}

	// Rows under a heading matching no delivery phase must NOT leak into the
	// profile — they belong to a different plan revision.
	if union["linkshelf/not_beaded_yet_test.go"] {
		t.Fatalf("unknown-section file leaked into union")
	}

	// Dir-affinity fallback: "backend-core" section (drifted id) still routes
	// handlers_test.go to the phase that owns internal/api/handlers.go.
	storeAfterHeal := findPhase("store-and-api")
	assertHas(storeAfterHeal.RequiredFiles, "linkshelf/internal/api/handlers_test.go")

	// Idempotent: re-running the clamp on the merged result adds nothing.
	before := len(got.RequiredFiles)
	again := ClampProfileValidationForRig(town, "fin", got)
	if len(again.RequiredFiles) != before {
		t.Fatalf("non-idempotent merge: %d -> %d", before, len(again.RequiredFiles))
	}
}

func TestClampProfileValidationForRig_noTestPlanIsNoop(t *testing.T) {
	town := t.TempDir() // no TEST_PLAN.md anywhere
	v := finPhases()
	inUnion := len(v.UnionRequiredFiles())

	got := ClampProfileValidationForRig(town, "fin", v)
	if len(got.UnionRequiredFiles()) != inUnion {
		t.Fatalf("union changed without TEST_PLAN.md: %d -> %d", inUnion, len(got.UnionRequiredFiles()))
	}
}
