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

// loadTestComposeFiles locates a test docker-compose file and its sibling package.json
// anywhere under the rig (e.g. test/docker-compose.test.yml or finally/test/docker-compose.test.yml).
// It prefers files whose path contains a test/e2e marker.
func loadTestComposeFiles(mayorRigDir string) (compose, pkg string, found bool) {
	composePath := ""
	var walkErr error
	_ = filepath.WalkDir(mayorRigDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasPrefix(d.Name(), "docker-compose") {
			return nil
		}
		lower := strings.ToLower(filepath.ToSlash(path))
		if !strings.Contains(lower, "test") && !strings.Contains(lower, "e2e") && !strings.Contains(d.Name(), "test") {
			return nil
		}
		if composePath == "" {
			composePath = path
			return nil
		}
		cur := strings.ToLower(filepath.ToSlash(composePath))
		if testHarnessScore(lower) > testHarnessScore(cur) {
			composePath = path
		}
		return nil
	})
	if walkErr != nil {
		return "", "", false
	}
	if composePath == "" {
		return "", "", false
	}
	composeBytes, err := os.ReadFile(composePath)
	if err != nil {
		return "", "", false
	}
	pkgPath := filepath.Join(filepath.Dir(composePath), "package.json")
	pkgBytes, _ := os.ReadFile(pkgPath)
	return string(composeBytes), string(pkgBytes), true
}

func testHarnessScore(lower string) int {
	score := 0
	if strings.Contains(lower, "/test/") || strings.Contains(lower, "/tests/") {
		score += 2
	}
	if strings.Contains(lower, "/e2e/") {
		score += 2
	}
	if strings.Contains(filepath.Base(lower), "test") {
		score += 1
	}
	return score
}

// DetectTestComposeIssues scans test/docker-compose.test.yml and test/package.json
// for common Playwright (and generic Node) test container mistakes.
func DetectTestComposeIssues(mayorRigDir string) TestComposeReport {
	report := TestComposeReport{}

	compose, pkg, found := loadTestComposeFiles(mayorRigDir)
	if !found {
		return report
	}

	// Playwright-specific checks.
	if strings.Contains(pkg, "@playwright/test") || strings.Contains(compose, "playwright") {
		if strings.Contains(compose, "mcr.microsoft.com/playwright") {
			report.Issues = append(report.Issues,
				"test compose uses the public Playwright image (`mcr.microsoft.com/playwright`) "+
					"instead of the preprepared local image `"+PlaywrightDockerImage+"`. "+
					"Pulling the public image downloads hundreds of MB and produces root-owned "+
					"artifacts in bind mounts. Use `image: "+PlaywrightDockerImage+"` on the "+
					"playwright service.")
			// The official Playwright image runs as root by default, so every file it writes
			// into a bind mount (playwright-report/, test-results/, node_modules) is owned by
			// root and cannot be removed from the host. Run it as the host user instead.
			if !strings.Contains(compose, "user:") {
				report.Issues = append(report.Issues,
					"test compose uses the official Playwright image but does not set `user:`. "+
						"The image runs as root, so playwright-report/ and test-results/ written into "+
						"bind mounts are root-owned and cannot be cleaned by the host. Set "+
						"`user: \"${DOCKER_UID:-1000}:${DOCKER_GID:-1000}\"` on the playwright service "+
						"(run with DOCKER_UID=$(id -u) DOCKER_GID=$(id -g)).")
			}
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
		// The QA runner invokes `docker-compose up --exit-code-from playwright`, so the
		// test harness MUST define a service literally named `playwright`. Rigs that name
		// it `e2e` or `tests` (FinAlly regression: `no such service: playwright`) fail the
		// gate even though the tests are correct.
		if !serviceNamed(compose, "playwright") {
			report.Issues = append(report.Issues,
				"test compose does not define a service literally named `playwright`. "+
					"The QA runner executes `docker-compose up --exit-code-from playwright`, "+
					"so the service running Playwright must be `playwright` (a name like `e2e` "+
					"causes `no such service: playwright`).")
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

// serviceNamed reports whether the compose YAML declares a top-level service with the
// given name. It matches `name:` under the `services:` key, tolerating common variations
// like `name : value`, inline `#` comments, and quoted keys.
func serviceNamed(compose, name string) bool {
	inServices := false
	for _, raw := range strings.Split(compose, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "services:") {
			inServices = true
			continue
		}
		if !inServices {
			continue
		}
		indent := len(raw) - len(strings.TrimLeft(raw, " \t"))
		// A top-level (2-space) key inside services: ends the service map.
		if indent < 2 {
			continue
		}
		if indent >= 4 {
			continue
		}
		// Strip inline comments.
		if idx := strings.Index(line, " #"); idx >= 0 {
			line = line[:idx]
		}
		line = strings.TrimSpace(line)
		if line == "" || line == "services:" {
			continue
		}
		// Skip nested keywords that could appear at this depth in malformed files.
		key := strings.TrimSuffix(strings.TrimSpace(strings.SplitN(line, ":", 2)[0]), `"`)
		key = strings.TrimPrefix(key, `"`)
		if key == "" {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(key), name) {
			return true
		}
	}
	return false
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
