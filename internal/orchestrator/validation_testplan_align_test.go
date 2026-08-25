package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const alignInputPlan = `# Test Plan

### old-backend
Test file: linkshelf/internal/store/store_test.go
Level: unit

### keeper-e2e
Test file: linkshelf/web/tests/app.spec.ts
Level: e2e

### mystery
Test file: linkshelf/nowhere/thing.txt
`

func writePlan(t *testing.T, town, rig, content string) string {
	t.Helper()
	dir := filepath.Join(town, rig, "mayor", "rig")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "TEST_PLAN.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAlignTestPlanSectionIDs_renamesViaDirAffinity(t *testing.T) {
	town := t.TempDir()
	path := writePlan(t, town, "fin", alignInputPlan)

	newV := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		RequiredFiles: []string{"linkshelf/internal/store/schema.go", "linkshelf/web/tests/app.spec.ts"},
		DeliveryPhases: []DeliveryPhase{
			{ID: "store-and-api", RequiredFiles: []string{"linkshelf/internal/store/schema.go"}},
			{ID: "e2e-tests", RequiredFiles: []string{"linkshelf/web/tests/app.spec.ts"}},
		},
	}

	res := alignTestPlanSectionIDs(town, "fin", newV)
	t.Logf("resulting plan:\n%s", mustRead(t, path))
	if res.renamed != 2 {
		t.Fatalf("renamed = %d, want 2 (old-backend and keeper-e2e are both drift-labeled)", res.renamed)
	}

	out := string(mustRead(t, path))
	wantHead := "### store-and-api\nTest file: linkshelf/internal/store/store_test.go"
	if !strings.Contains(out, wantHead) {
		t.Fatalf("section not relabeled with body intact:\n%s", out)
	}
	if strings.Contains(out, "### keeper-e2e") || !strings.Contains(out, "### e2e-tests") {
		t.Fatalf("keeper-e2e should be relabeled to e2e-tests:\n%s", out)
	}
	if !strings.Contains(out, "### mystery") {
		t.Fatal("unmappable section must be left for Tester rewrite")
	}
}

func TestAlignTestPlanSectionIDs_noopWhenAligned(t *testing.T) {
	town := t.TempDir()
	aligned := strings.ReplaceAll(alignInputPlan, "### old-backend", "### store-and-api")
	path := writePlan(t, town, "fin", aligned)

	newV := WorkflowValidation{
		DeliveryPhases: []DeliveryPhase{
			{ID: "store-and-api", RequiredFiles: []string{"linkshelf/internal/store/schema.go"}},
		},
	}
	res := alignTestPlanSectionIDs(town, "fin", newV)
	if res.renamed != 0 {
		t.Fatalf("renamed = %d on already-aligned plan", res.renamed)
	}
	after, _ := os.ReadFile(path)
	if !strings.Contains(string(after), "### store-and-api") {
		t.Fatal("aligned plan mutated")
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
