package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidateBeadArtifactOnDisk reports whether a layout-relative implement path exists and is not stubbed.
func ValidateBeadArtifactOnDisk(rigDir, beadPath string, v WorkflowValidation) error {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	if beadPath == "" {
		return fmt.Errorf("empty bead path")
	}
	if beadImplementationNeedsRework(rigDir, beadPath, v.ForActivePhase()) {
		return fmt.Errorf("artifact %s missing, empty, or stub", beadPath)
	}
	return nil
}

// ReconcileClosedImplementBeads reopens closed implement beads whose files are missing,
// empty, or stubbed. Unlike EnsureImplementBeadsAvailable, this runs even when other
// implement beads are still open (e.g. closed te-rnd while te-avv is in progress).
func ReconcileClosedImplementBeads(townRoot, rig string, v WorkflowValidation) ([]string, error) {
	if rig == "" || townRoot == "" {
		return nil, nil
	}
	if !BeadsDatabaseReady(townRoot, rig) {
		return nil, nil
	}
	return reopenClosedImplementBeads(townRoot, rig, v.ForActivePhase())
}

// AuditRequiredImplementFiles reports required_files that are missing, empty, or stubbed on disk.
func AuditRequiredImplementFiles(rigDir string, v WorkflowValidation) []string {
	v = v.ForActivePhase()
	var issues []string
	for _, rel := range v.RequiredFiles {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" {
			continue
		}
		path := filepath.Join(rigDir, filepath.FromSlash(rel))
		info, err := os.Stat(path)
		if err != nil {
			issues = append(issues, fmt.Sprintf("missing %s", rel))
			continue
		}
		if info.Size() == 0 {
			issues = append(issues, fmt.Sprintf("empty %s", rel))
			continue
		}
		if IsProjectSetupArtifactPath(rel, v) {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			issues = append(issues, fmt.Sprintf("unreadable %s", rel))
			continue
		}
		opts := StubCheckOptionsFromValidation(v)
		if err := CheckContentNotStub(data, rel, opts); err != nil {
			issues = append(issues, fmt.Sprintf("stub %s", rel))
		}
		if err := CheckPythonSourceValid(data, rel); err != nil {
			issues = append(issues, fmt.Sprintf("invalid %s: %v", rel, err))
		}
	}
	return issues
}

// AuditClosedImplementBeadMismatches reports closed beads whose on-disk artifact still needs work.
func AuditClosedImplementBeadMismatches(townRoot, rig string, v WorkflowValidation) ([]string, error) {
	v = v.ForActivePhase()
	closed, err := listImplementBeadsByStatus(townRoot, rig, v, "closed")
	if err != nil {
		return nil, err
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	var issues []string
	for _, b := range closed {
		if b.ID == "" {
			continue
		}
		p := resolveImplementBeadPath(b.Title, v)
		if p == "" || IsProjectSetupArtifactPath(p, v) {
			continue
		}
		if beadImplementationNeedsRework(rigDir, p, v) {
			issues = append(issues, fmt.Sprintf("closed %s should not be closed (%s)", b.ID, p))
		}
	}
	return issues, nil
}

// ReconcileImplementBeads audits disk vs required_files, reopens mismatched closed beads,
// then runs EnsureImplementBeadsAvailable when no implement work is in flight.
func ReconcileImplementBeads(townRoot, rig string, v WorkflowValidation) (string, error) {
	if rig == "" || townRoot == "" {
		return "", nil
	}
	v = v.ForActivePhase()
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	var parts []string

	for _, issue := range AuditRequiredImplementFiles(rigDir, v) {
		parts = append(parts, issue)
	}
	mismatches, err := AuditClosedImplementBeadMismatches(townRoot, rig, v)
	if err != nil {
		return joinStrings(parts, "; "), err
	}
	for _, m := range mismatches {
		parts = append(parts, m)
	}

	reopened, err := ReconcileClosedImplementBeads(townRoot, rig, v)
	if err != nil {
		return joinStrings(parts, "; "), err
	}
	if len(reopened) > 0 {
		parts = append(parts, "reopened: "+joinStrings(reopened, ", "))
	}

	more, err := EnsureImplementBeadsAvailable(townRoot, rig, v)
	if err != nil {
		return joinStrings(parts, "; "), err
	}
	for _, id := range more {
		if !containsString(reopened, id) {
			parts = append(parts, "reopened: "+id)
		}
	}
	if len(parts) == 0 {
		return "implement beads and required_files are consistent", nil
	}
	return joinStrings(parts, "; "), nil
}

func containsString(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
