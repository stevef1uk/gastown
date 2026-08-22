package orchestrator

import (
	"path/filepath"
	"strings"
)

// isTesterAllowedDocPath reports whether rel is one of the two artifacts the
// tester may write: TEST_PLAN.md and test-report.md (both at rig root).
func isTesterAllowedDocPath(rel string) bool {
	switch strings.ToLower(filepath.Base(rel)) {
	case "test_plan.md", "test-report.md":
		return true
	default:
		return false
	}
}

// TesterCommandMutatesForbidden reports when a tester shell command writes any
// file other than TEST_PLAN.md / test-report.md. The tester is read-only over
// source, tests, and all other docs — it audits sufficiency, it does not fix.
func TesterCommandMutatesForbidden(cmd string, v WorkflowValidation) (path string, ok bool) {
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if p := ExtractImplementWritePathFromCmd(cmd, layout); p != "" {
		// Allow tester to write TEST_PLAN.md and test-report.md — these are
		// tester artifacts, not implement edits, even if the cmd contains
		// a cat > ... redirect.
		if isTesterAllowedDocPath(p) {
			return "", false
		}
		return p, true
	}
	for _, m := range qaShellRedirectRE.FindAllStringSubmatch(cmd, -1) {
		if len(m) < 2 {
			continue
		}
		target := filepath.ToSlash(strings.Trim(m[1], `"'`))
		if target == "" {
			continue
		}
		if !isTesterAllowedDocPath(target) {
			return target, true
		}
	}
	return "", false
}

// QACommandWritesTestPlanDoc reports when QA writes TEST_PLAN.md or test-report.md.
// QA must never touch tester artifacts (GT-TEST-001).
func QACommandWritesTestPlanDoc(cmd string) (path string, ok bool) {
	for _, m := range qaShellRedirectRE.FindAllStringSubmatch(cmd, -1) {
		if len(m) < 2 {
			continue
		}
		target := filepath.ToSlash(strings.Trim(m[1], `"'`))
		if isTesterAllowedDocPath(target) {
			return target, true
		}
	}
	return "", false
}

// IsTesterWritingTestPlan reports whether a tester shell command writes to TEST_PLAN.md.
func IsTesterWritingTestPlan(cmd string) bool {
	for _, m := range qaShellRedirectRE.FindAllStringSubmatch(cmd, -1) {
		if len(m) < 2 {
			continue
		}
		target := filepath.ToSlash(strings.Trim(m[1], `"'`))
		if strings.ToLower(filepath.Base(target)) == "test_plan.md" {
			return true
		}
	}
	return false
}