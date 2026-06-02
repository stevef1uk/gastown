package orchestrator

import (
	"path/filepath"
	"strings"
)

// ImplementWriteScope carries optional cross-bead allowances during implementation (e.g. QA rework).
type ImplementWriteScope struct {
	QAReworkFromQAReview bool
	QACitedBeadIDs       map[string]bool // lowercase rig-prefixed ids from QA summary (te-*, de-*, …)
}

// QAReworkWriteScopeFromTransition builds write/reopen scope when QA sent the workflow back to implementation.
func QAReworkWriteScopeFromTransition(townRoot, rig, fromState, toState, summary string) ImplementWriteScope {
	if fromState != "qa_review" || toState != "implementation" || townRoot == "" || rig == "" {
		return ImplementWriteScope{}
	}
	known, prefix, err := ListRigBeadIDSet(townRoot, rig)
	if err != nil || prefix == "" {
		return ImplementWriteScope{}
	}
	ids := ExtractKnownRigBeadIDsFromSummary(summary, prefix, known)
	if len(ids) == 0 {
		return ImplementWriteScope{}
	}
	cited := make(map[string]bool, len(ids))
	for _, id := range ids {
		cited[strings.ToLower(strings.TrimSpace(id))] = true
	}
	return ImplementWriteScope{
		QAReworkFromQAReview: true,
		QACitedBeadIDs:       cited,
	}
}

// BeadCited reports whether id (any case) was named in the QA failure summary.
func (s ImplementWriteScope) BeadCited(id string) bool {
	if len(s.QACitedBeadIDs) == 0 {
		return false
	}
	return s.QACitedBeadIDs[strings.ToLower(strings.TrimSpace(id))]
}

func IsFrontendImplementPath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	return strings.HasSuffix(lower, ".html") ||
		strings.HasSuffix(lower, ".css") ||
		strings.HasSuffix(lower, ".js")
}

// AllowedQAReworkWebImplementWrite allows editing cited web/ files across beads after QA failure
// (e.g. align index.html and app.js when QA names both), even when one implement bead is closed.
func AllowedQAReworkWebImplementWrite(townRoot, rig, activeBead, activePath, written string, scope ImplementWriteScope, v WorkflowValidation) bool {
	if !scope.QAReworkFromQAReview || len(scope.QACitedBeadIDs) == 0 {
		return false
	}
	activePath = filepath.ToSlash(strings.TrimSpace(activePath))
	written = filepath.ToSlash(strings.TrimSpace(written))
	if !IsFrontendImplementPath(written) {
		return false
	}
	if !pathMatchesRequired(written, v.RequiredFiles) {
		return false
	}
	if activePath != "" && !IsFrontendImplementPath(activePath) {
		return false
	}
	if scope.BeadCited(activeBead) {
		for citedID := range scope.QACitedBeadIDs {
			p := ImplementBeadPathForID(townRoot, rig, citedID, v)
			if p != "" && IsFrontendImplementPath(p) &&
				PathMatchesImplementWrite(written, p, v.RequiredFiles, v) {
				return true
			}
		}
	}
	if id, ok := ClosedImplementBeadForPath(townRoot, rig, written, v); ok && scope.BeadCited(id) {
		return true
	}
	if id, _, ok := OpenImplementBeadForPath(townRoot, rig, written, v); ok && scope.BeadCited(id) {
		return true
	}
	return false
}
