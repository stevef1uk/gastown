package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectTestComposeIssues_npxPlaywright(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "test"), 0755)
	os.WriteFile(filepath.Join(dir, "test", "package.json"), []byte(`{"devDependencies":{"@playwright/test":"1.51.0"}}`), 0644)
	os.WriteFile(filepath.Join(dir, "test", "docker-compose.test.yml"), []byte(`
version: "3.8"
services:
  playwright:
    image: mcr.microsoft.com/playwright:v1.51.0-jammy
    command: ["npm install && npx playwright test"]
`), 0644)

	report := DetectTestComposeIssues(dir)
	if report.IsClean() {
		t.Fatal("expected issues")
	}
	found := false
	for _, issue := range report.Issues {
		if strings.Contains(issue, "npx playwright") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected npx playwright warning, got: %v", report.Issues)
	}
}

func TestDetectTestComposeIssues_clean(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "test"), 0755)
	os.WriteFile(filepath.Join(dir, "test", "package.json"), []byte(`{"devDependencies":{"@playwright/test":"1.51.0"}}`), 0644)
	os.WriteFile(filepath.Join(dir, "test", "docker-compose.test.yml"), []byte(`
version: "3.8"
services:
  playwright:
    image: playwright-go-test:latest
    user: "${DOCKER_UID:-1000}:${DOCKER_GID:-1000}"
    working_dir: /app/test
    command: ["npm install && ./node_modules/.bin/playwright test"]
`), 0644)

	report := DetectTestComposeIssues(dir)
	if !report.IsClean() {
		t.Fatalf("expected clean, got: %v", report.Issues)
	}
}

func TestDetectTestComposeIssues_playwrightRunsAsRoot(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "test"), 0755)
	os.WriteFile(filepath.Join(dir, "test", "package.json"), []byte(`{"devDependencies":{"@playwright/test":"1.51.0"}}`), 0644)
	os.WriteFile(filepath.Join(dir, "test", "docker-compose.test.yml"), []byte(`
version: "3.8"
services:
  playwright:
    image: mcr.microsoft.com/playwright:v1.51.0-jammy
    command: ["npm install && ./node_modules/.bin/playwright test"]
`), 0644)

	report := DetectTestComposeIssues(dir)
	found := false
	for _, issue := range report.Issues {
		if strings.Contains(issue, "runs as root") || strings.Contains(issue, "root-owned") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected root-user warning, got: %v", report.Issues)
	}
}

func TestDetectTestComposeIssues_publicImageFlagged(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "test"), 0755)
	os.WriteFile(filepath.Join(dir, "test", "package.json"), []byte(`{"devDependencies":{"@playwright/test":"1.51.0"}}`), 0644)
	os.WriteFile(filepath.Join(dir, "test", "docker-compose.test.yml"), []byte(`
version: "3.8"
services:
  playwright:
    image: mcr.microsoft.com/playwright:v1.45.3-jammy
    command: ["npm install && ./node_modules/.bin/playwright test"]
`), 0644)

	report := DetectTestComposeIssues(dir)
	found := false
	for _, issue := range report.Issues {
		if strings.Contains(issue, "playwright-go-test:latest") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected public-image warning recommending playwright-go-test:latest, got: %v", report.Issues)
	}
}

func TestDetectTestComposeIssues_missingPlaywrightService(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "test"), 0755)
	os.WriteFile(filepath.Join(dir, "test", "package.json"), []byte(`{"devDependencies":{"@playwright/test":"1.51.0"}}`), 0644)
	// FinAlly regression: service named `e2e` instead of `playwright`.
	os.WriteFile(filepath.Join(dir, "test", "docker-compose.test.yml"), []byte(`
version: "3.8"
services:
  app:
    build: ..
  e2e:
    image: playwright-go-test:latest
    depends_on: [app]
    command: ["npm install && ./node_modules/.bin/playwright test"]
`), 0644)

	report := DetectTestComposeIssues(dir)
	found := false
	for _, issue := range report.Issues {
		if strings.Contains(issue, "no such service: playwright") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected missing-playwright-service warning, got: %v", report.Issues)
	}
}
