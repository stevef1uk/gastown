package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
)

// TestComposeReport lists issues in a docker-compose test setup.
type TestComposeReport struct {
	Issues []string
}

func (r TestComposeReport) IsClean() bool {
	return len(r.Issues) == 0
}

// DetectTestComposeIssues scans test/docker-compose.test.yml and test/package.json
// for common Playwright (and generic Node) test container mistakes.
func DetectTestComposeIssues(mayorRigDir string) TestComposeReport {
	report := TestComposeReport{}

	composePath := filepath.Join(mayorRigDir, "test", "docker-compose.test.yml")
	composeBytes, err := os.ReadFile(composePath)
	if err != nil {
		return report
	}
	compose := string(composeBytes)

	pkgPath := filepath.Join(mayorRigDir, "test", "package.json")
	pkgBytes, _ := os.ReadFile(pkgPath)
	pkg := string(pkgBytes)

	// Playwright-specific checks.
	if strings.Contains(pkg, "@playwright/test") || strings.Contains(compose, "playwright") {
		if strings.Contains(compose, "mcr.microsoft.com/playwright") {
			// Using the official Playwright image with a global `npx playwright` is a common
			// source of version skew: the image may not have the same version as the
			// @playwright/test dependency in package.json.
			if strings.Contains(compose, "npx playwright") {
				report.Issues = append(report.Issues,
					"test compose uses `npx playwright` inside the official Playwright image. "+
						"Run `npm install` in the test directory and invoke `./node_modules/.bin/playwright` "+
						"so the test runner matches the @playwright/test version in package.json.")
			}
		}
	}

	// Generic check: if package.json is mounted, npm install should be run in the same
	// working directory so node_modules resolution is unambiguous.
	if strings.Contains(compose, "package.json") && strings.Contains(compose, "npm install") {
		if !strings.Contains(compose, "working_dir") {
			report.Issues = append(report.Issues,
				"test compose mounts package.json but does not set `working_dir` for `npm install`. "+
					"Set `working_dir` to the directory containing package.json so `node_modules` resolves correctly.")
		}
	}

	return report
}

// FormatTestComposeGuidance returns QA guidance from a TestComposeReport, or empty if clean.
func FormatTestComposeGuidance(report TestComposeReport) string {
	if report.IsClean() {
		return ""
	}
	var b strings.Builder
	b.WriteString("### Test container setup issues\n")
	b.WriteString("The test compose file has patterns that often cause version skew or path resolution failures:\n\n")
	for _, issue := range report.Issues {
		b.WriteString("- " + issue + "\n")
	}
	return strings.TrimSpace(b.String())
}
