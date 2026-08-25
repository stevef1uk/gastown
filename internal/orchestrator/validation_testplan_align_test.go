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

func TestAlignArchitectureWithTestPlan_appendsMissingTestFiles(t *testing.T) {
	town := t.TempDir()
	rigDir := filepath.Join(town, "fin", "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Architecture lists production files only — no test files
	arch := "# Architecture\n\n## File layout\n\n- `linkshelf/go.mod`\n- `linkshelf/internal/store/schema.go`\n- `linkshelf/internal/store/store.go`\n- `linkshelf/internal/api/handlers.go`\n- `linkshelf/cmd/server/main.go`\n"
	os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(arch), 0o644)

	// TEST_PLAN references two test files: one missing, one already listed
	plan := "### backend-core\nTest file: linkshelf/internal/api/handlers_test.go\n\n### store-tests\nTest file: linkshelf/internal/store/store_test.go\n"
	os.WriteFile(filepath.Join(rigDir, "TEST_PLAN.md"), []byte(plan), 0o644)

	alignArchitectureWithTestPlan(town, "fin")

	result, _ := os.ReadFile(filepath.Join(rigDir, "architecture.md"))
	s := string(result)

	if !strings.Contains(s, "`linkshelf/internal/api/handlers_test.go`") {
		t.Fatal("handlers_test.go was not appended to architecture.md")
	}
	if !strings.Contains(s, "`linkshelf/internal/store/store_test.go`") {
		t.Fatal("store_test.go was not appended to architecture.md")
	}
	// Original files must be preserved
	for _, want := range []string{"`linkshelf/go.mod`", "`linkshelf/internal/store/schema.go`", "`linkshelf/cmd/server/main.go`"} {
		if !strings.Contains(s, want) {
			t.Fatalf("original entry %s lost during alignment", want)
		}
	}
	// Idempotent: second call adds nothing
	alignArchitectureWithTestPlan(town, "fin")
	result2, _ := os.ReadFile(filepath.Join(rigDir, "architecture.md"))
	count := strings.Count(string(result2), "`linkshelf/internal/api/handlers_test.go`")
	if count != 1 {
		t.Fatalf("non-idempotent: handlers_test.go appears %d times", count)
	}
}
