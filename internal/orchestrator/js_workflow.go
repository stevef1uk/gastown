package orchestrator

import (
	"path/filepath"
	"regexp"
	"strings"
)

var nodeSetupCdRe = regexp.MustCompile(`^cd\s+\S+\s*&&\s*`)

// WorkflowUsesNodeJS reports whether the workflow runs npm/yarn tests (frontend/Node).
func WorkflowUsesNodeJS(v WorkflowValidation) bool {
	return v.detectsNodeProject()
}

func (v WorkflowValidation) detectsNodeProject() bool {
	q := strings.ToLower(strings.TrimSpace(v.QAVerifyCommand))
	if strings.Contains(q, "npm ") || strings.Contains(q, "npm/") || strings.Contains(q, "yarn ") || strings.Contains(q, "pnpm ") {
		return true
	}
	for _, f := range v.RequiredFiles {
		lower := strings.ToLower(strings.TrimSpace(f))
		if strings.HasSuffix(lower, ".tsx") || strings.HasSuffix(lower, ".ts") || strings.HasSuffix(lower, ".js") || strings.HasSuffix(lower, ".jsx") {
			return true
		}
		if strings.HasSuffix(lower, "/package.json") || lower == "package.json" {
			return true
		}
	}
	return false
}

// NodeProjectSetupVerifyCommand returns the green check for project_setup only:
// install Node dependencies (npm/yarn/pnpm install) so later tests can run.
// Preserves any cd <subdir> && prefix from QAVerifyCommand so npm install
// runs in the correct subdirectory (e.g. "cd frontend && npm install").
func NodeProjectSetupVerifyCommand(v WorkflowValidation) string {
	base := strings.TrimSpace(v.QAVerifyCommand)
	lower := strings.ToLower(base)

	pm := "npm"
	for _, candidate := range []string{"pnpm", "yarn", "npm"} {
		if strings.Contains(lower, candidate) {
			pm = candidate
			break
		}
	}

	cdPrefix := nodeSetupCdRe.FindString(base)

	return cdPrefix + pm + " install"
}

// nodeInstallDirFromRequiredFiles returns the common Node directory prefix
// (e.g. "frontend", "app") when all required files share one, or "".
func nodeInstallDirFromRequiredFiles(files []string) string {
	var dir string
	hasRootPackage := false
	for _, f := range files {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if !looksLikeNodeFile(f) {
			continue
		}
		idx := strings.Index(f, "/")
		if idx <= 0 {
			// Root-level Node file - only counts as package root if it's a manifest
			if f == "package.json" || f == "pnpm-lock.yaml" || f == "yarn.lock" || f == "package-lock.json" {
				hasRootPackage = true
			}
			continue
		}
		first := f[:idx]
		if dir == "" {
			dir = first
		} else if dir != first {
			return ""
		}
	}
	if hasRootPackage && dir == "" {
		// Root package.json with no other Node files in subdirs
		return "."
	}
	if hasRootPackage && dir != "" {
		// Root package.json AND subdir Node files - ambiguous, let framework decide
		return ""
	}
	return dir
}

func looksLikeNodeFile(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".tsx") || strings.HasSuffix(lower, ".ts") ||
		strings.HasSuffix(lower, ".jsx") || strings.HasSuffix(lower, ".js") ||
		strings.HasSuffix(lower, "/package.json") || path == "package.json"
}
