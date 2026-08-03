package orchestrator

import (
	"path/filepath"
	"regexp"
	"strings"
)

var nodeSetupCdRe = regexp.MustCompile(`^cd\s+\S+\s*&&\s*`)

// WorkflowUsesNodeJS reports whether the workflow uses Node.js/TypeScript/JavaScript tooling.
// Returns true for actual Node.js projects (package.json, tsconfig.json, or explicit
// npm/pnpm/yarn commands). Also returns true for frontend TypeScript/React projects
// with .ts/.tsx files in a frontend/app directory. Pure Playwright E2E projects
// with only test/e2e spec files do NOT count as Node.js projects.
func WorkflowUsesNodeJS(v WorkflowValidation) bool {
	// Check for explicit Node.js project infrastructure
	hasPackageJSON := false
	hasTSConfig := false
	hasNodeCommand := false
	hasFrontendTypeScript := false

	// Check required files for project infrastructure
	for _, f := range v.RequiredFiles {
		f = strings.ToLower(strings.TrimSpace(f))
		if strings.HasSuffix(f, "package.json") || f == "package.json" {
			hasPackageJSON = true
		}
		if strings.HasSuffix(f, "tsconfig.json") {
			hasTSConfig = true
		}
		// Frontend TypeScript/React files in app/frontend directory indicate a Node.js project
		if (strings.HasSuffix(f, ".ts") || strings.HasSuffix(f, ".tsx")) &&
			(strings.HasPrefix(f, "frontend/") || strings.HasPrefix(f, "app/") || strings.HasPrefix(f, "src/")) {
			hasFrontendTypeScript = true
		}
	}

	// Check verify commands for explicit npm/pnpm/yarn commands (not just npx playwright)
	q := strings.ToLower(strings.TrimSpace(v.QAVerifyCommand))
	if strings.Contains(q, "npm install") || strings.Contains(q, "npm test") ||
		strings.Contains(q, "npm run test") || strings.Contains(q, "yarn install") ||
		strings.Contains(q, "yarn test") || strings.Contains(q, "pnpm install") ||
		strings.Contains(q, "pnpm test") {
		hasNodeCommand = true
	}

	// A Node.js workflow requires explicit project infrastructure
	// Playwright config/spec files alone do NOT make it a Node.js project
	return hasPackageJSON || hasTSConfig || hasNodeCommand || hasFrontendTypeScript
}

func (v WorkflowValidation) detectsNodeProject() bool {
	return WorkflowUsesNodeJS(v)
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

// NodeImplementationVerifyCommandForBead returns verify scoped to the active implement path for Node.js/TypeScript projects.
func NodeImplementationVerifyCommandForBead(v WorkflowValidation, mayorRigDir, beadPath string) string {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	if beadPath == "" {
		return ""
	}

	// Playwright/E2E test files
	if strings.HasSuffix(beadPath, ".spec.ts") || strings.HasSuffix(beadPath, ".spec.tsx") ||
		strings.HasSuffix(beadPath, ".test.ts") || strings.HasSuffix(beadPath, ".test.tsx") ||
		strings.HasSuffix(beadPath, ".e2e.ts") || strings.HasSuffix(beadPath, ".e2e.spec.ts") {
		return "npx playwright test " + beadPath
	}

	// Vitest/Jest unit test files
	if strings.HasSuffix(beadPath, ".test.ts") || strings.HasSuffix(beadPath, ".test.tsx") ||
		strings.HasSuffix(beadPath, ".spec.ts") || strings.HasSuffix(beadPath, ".spec.tsx") {
		if strings.Contains(beadPath, "e2e") || strings.Contains(beadPath, "playwright") {
			return "npx playwright test " + beadPath
		}
		return "npx vitest run " + beadPath
	}

	// package.json / tsconfig.json / playwright.config.* — verify with project-wide test command
	if strings.HasSuffix(beadPath, "package.json") ||
		strings.HasSuffix(beadPath, "tsconfig.json") ||
		strings.HasSuffix(beadPath, "playwright.config.ts") ||
		strings.HasSuffix(beadPath, "playwright.config.js") ||
		strings.HasSuffix(beadPath, "vitest.config.ts") ||
		strings.HasSuffix(beadPath, "jest.config.ts") ||
		strings.HasSuffix(beadPath, "jest.config.js") {
		return "npm test"
	}

	// TypeScript/JavaScript source files — check if they compile
	if strings.HasSuffix(beadPath, ".ts") || strings.HasSuffix(beadPath, ".tsx") ||
		strings.HasSuffix(beadPath, ".js") || strings.HasSuffix(beadPath, ".jsx") {
		return "npx tsc --noEmit"
	}

	// Default to project test command
	return "npm test"
}
