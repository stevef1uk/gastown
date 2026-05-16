package specprofile

import (
	"path/filepath"
)

// GastownMetaDir is the hidden directory under mayor/rig for machine-local metadata.
const GastownMetaDir = ".gastown"

// WorkflowProfileFilename is the JSON file produced by spec-index / LLM extraction.
const WorkflowProfileFilename = "workflow-profile.json"

// ProfilePath returns the path to workflow-profile.json for a rig.
func ProfilePath(townRoot, rig string) string {
	return filepath.Join(townRoot, rig, "mayor", "rig", GastownMetaDir, WorkflowProfileFilename)
}

// SpecPath returns the canonical SPEC.md path for a rig worktree.
func SpecPath(townRoot, rig string) string {
	return filepath.Join(townRoot, rig, "mayor", "rig", "SPEC.md")
}
