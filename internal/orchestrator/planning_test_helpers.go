package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

// writeMinimalPlanningRigDocs adds SPEC.md and architecture.md so WritePlanningPlanMD
// alignment checks succeed in integration tests that only set up beads/plan.md.
func writeMinimalPlanningRigDocs(t *testing.T, rigDir string) {
	t.Helper()
	spec := "# Spec\n\nMinimal spec for planning sync tests.\n"
	arch := "# Architecture\n\nMinimal architecture for planning sync tests.\n"
	for name, body := range map[string]string{"SPEC.md": spec, "architecture.md": arch} {
		if err := os.WriteFile(filepath.Join(rigDir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
}
