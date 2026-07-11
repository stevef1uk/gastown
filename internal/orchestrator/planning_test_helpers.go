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
	arch := "# Architecture\n\nMinimal architecture for planning sync tests.\n\n" +
		"## Docker & Deployment\n\n" +
		"Stage 1: FROM node:20-slim, run `npm ci && npm run build` to produce `frontend/out/`. " +
		"Stage 2: FROM python:3.12-slim, copy the build output, expose port 8000, CMD uvicorn backend.main:app --host 0.0.0.0 --port 8000.\n"
	for name, body := range map[string]string{"SPEC.md": spec, "architecture.md": arch} {
		if err := os.WriteFile(filepath.Join(rigDir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
}
