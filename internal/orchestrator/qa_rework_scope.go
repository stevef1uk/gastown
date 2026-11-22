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
	var ids []string
	if report, ok := ParseQAFailureReport(summary); ok {
		ids = qaFailureRepairBeadIDs(report)
	} else {
		if IsQAAgentShellError(summary) {
			return ImplementWriteScope{}
		}
		known, prefix, err := ListRigBeadIDSet(townRoot, rig)
		if err != nil || prefix == "" {
			return ImplementWriteScope{}
		}
		ids = ExtractKnownRigBeadIDsFromSummary(summary, prefix, known)
	}
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
	if IsE2ETestPath(path) {
		return false
	}
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	return strings.HasSuffix(lower, ".html") ||
		strings.HasSuffix(lower, ".css") ||
		strings.HasSuffix(lower, ".js") ||
		strings.HasSuffix(lower, ".tsx") ||
		strings.HasSuffix(lower, ".ts")
}

// IsE2ETestPath reports whether the implement bead path is an end-to-end test file
// (Playwright, Cypress, or a dedicated e2e directory).
func IsE2ETestPath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	if strings.Contains(lower, "/e2e/") ||
		strings.Contains(lower, "/playwright/") ||
		strings.Contains(lower, "/cypress/") {
		return true
	}
	if strings.HasSuffix(lower, ".spec.ts") ||
		strings.HasSuffix(lower, ".spec.js") ||
		strings.HasSuffix(lower, ".cy.ts") ||
		strings.HasSuffix(lower, ".cy.js") ||
		strings.HasSuffix(lower, ".test.ts") ||
		strings.HasSuffix(lower, ".test.js") {
		return true
	}
	base := strings.ToLower(filepath.Base(path))
	return base == "playwright.config.ts" ||
		base == "playwright.config.js" ||
		base == "cypress.config.ts" ||
		base == "cypress.config.js"
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

// ImplementationBeadRequiresVerify reports whether closing this implement bead must
// wait for a green per-bead or phase verify. Compiled backend source and tests
// (.go, .py) and end-to-end test files (Playwright/Cypress) carry a real
// compile/test verify and stay gated. Docs, config, manifests, scripts, frontend
// assets, and other non-compiled artifacts are validated per-file on disk
// instead: requiring the phase verify for them deadlocks the queue behind a later
// source or package.json bead, because the phase command cannot go green until
// every required file exists. The full phase command is still mandatory before
// implementation can complete.
func ImplementationBeadRequiresVerify(beadPath string) bool {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	if IsE2ETestPath(beadPath) {
		return true
	}
	switch strings.ToLower(filepath.Ext(beadPath)) {
	case ".go", ".py":
		return true
	}
	return false
}

// IsPlaceholderFile reports whether the path is a placeholder file that is
// intentionally small (e.g., .gitkeep files to keep directories in git).
func IsPlaceholderFile(path string) bool {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(path)))
	return base == ".gitkeep" || base == ".gitignore" || base == ".keep"
}
