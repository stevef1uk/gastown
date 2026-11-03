package specprofile

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestParseSpecPhasesStandaloneHeadings(t *testing.T) {
	phases := parseSpecPhases("# Application\n# Phase 1 — Bootstrap\n## Deliverables\n- Project setup\n# Phase 2 — Streaming\n# Phase 3 — Browser UI\n# Phase 4 — Refinement\n")
	if len(phases) != 4 || phases[0].ID != "bootstrap" || phases[3].ID != "refinement" {
		t.Fatalf("standalone SPEC phases lost: %+v", phases)
	}
}

func TestIndexRigDoesNotFallbackAfterDeclaredPhaseAssignmentFailure(t *testing.T) {
	town := t.TempDir()
	dir := filepath.Join(town, "sample", "mayor", "rig")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	spec := "# Product\n## Layout\n```\nproject/\n└── engine.py\n```\n# Phase 1 — UX\n# Phase 2 — Polish\n"
	if err := os.WriteFile(filepath.Join(dir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Assignment unavailable: must not switch to an unconstrained extractor.
	_, err := IndexRig(ctx, town, "sample", true)
	if !errors.Is(err, errDeclaredPhaseAssignment) {
		t.Fatalf("expected declared-assignment error, got %v", err)
	}
	if _, err := os.Stat(ProfilePath(town, "sample")); !os.IsNotExist(err) {
		t.Fatalf("failed assignment wrote a profile: %v", err)
	}
}
