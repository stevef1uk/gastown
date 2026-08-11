package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScaffoldRigIntegrationTemplates_FinAlly(t *testing.T) {
	t.Parallel()

	finSpec := `# FinAlly - AI Trading Workstation

## 1. Vision
A single-user AI trading workstation. The user runs a single Docker command. Browser opens to http://localhost:8000.

## 3. Architecture Overview
Single Docker container: multi-stage build (Node -> Python). Static Next.js export served by FastAPI on port 8000.

## 4. Directory Structure
finally/
- frontend/          # Next.js TypeScript project (static export)
- backend/           # FastAPI uv project (Python)
- test/              # Playwright E2E tests + docker-compose.test.yml
- scripts/
- Dockerfile         # Multi-stage build (Node -> Python)
- docker-compose.yml # Optional convenience wrapper
- .env

## 10. Docker & Deployment
services:
  app:
    build: .
    ports:
      - "8000:8000"
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8000/api/health"]
    volumes:
      - finally_db:/app/db
`

	tmpDir := t.TempDir()
	rigDir := filepath.Join(tmpDir, "fin", "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(finSpec), 0o644); err != nil {
		t.Fatal(err)
	}

	profPath := filepath.Join(rigDir, ".gastown", "workflow-profile.json")
	if err := os.MkdirAll(filepath.Dir(profPath), 0o755); err != nil {
		t.Fatal(err)
	}
	profData := `{
  "version": 1,
  "validation": {
    "layout_root": "finally",
    "dev_server_port": 8000,
    "test_runner": "pytest",
    "delivery_phases": [{"id": "test", "required_files": ["finally/test/package.json", "finally/test/playwright.config.ts", "finally/test/e2e.spec.ts", "finally/Dockerfile", "finally/docker-compose.yml", "finally/test/docker-compose.test.yml"]}],
    "required_files": ["finally/backend/pyproject.toml", "finally/frontend/package.json"]
  }
}`
	if err := os.WriteFile(profPath, []byte(profData), 0o644); err != nil {
		t.Fatal(err)
	}

	written, err := ScaffoldRigIntegrationTemplates(tmpDir, "fin", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Written: %d files", written)

	filepath.Walk(filepath.Join(tmpDir, "fin", "mayor", "rig"), func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			t.Logf("Created: %s", strings.TrimPrefix(path, filepath.Join(tmpDir, "fin", "mayor", "rig")))
		}
		return nil
	})

	dfPath := filepath.Join(tmpDir, "fin", "mayor", "rig", "finally", "Dockerfile")
	if _, err := os.Stat(dfPath); os.IsNotExist(err) {
		t.Errorf("Dockerfile NOT created at finally/Dockerfile")
	} else {
		df, _ := os.ReadFile(dfPath)
		dfStr := string(df)
		if !strings.Contains(dfStr, "FROM node:") || !strings.Contains(dfStr, "FROM python:") {
			t.Errorf("Dockerfile should be hybrid (node + python stages)")
		}
		if !strings.Contains(dfStr, "frontend") || !strings.Contains(dfStr, "backend") {
			t.Errorf("Dockerfile should reference frontend/backend dirs")
		}
	}

	testCompPath := filepath.Join(tmpDir, "fin", "mayor", "rig", "finally", "test", "docker-compose.test.yml")
	if _, err := os.Stat(testCompPath); os.IsNotExist(err) {
		t.Errorf("test harness not created at test/docker-compose.test.yml")
	} else {
		tc, _ := os.ReadFile(testCompPath)
		tcStr := string(tc)
		if !strings.Contains(tcStr, "app:") || !strings.Contains(tcStr, "playwright:") {
			t.Errorf("test compose missing app or playwright service")
		}
		if !strings.Contains(tcStr, "condition: service_healthy") {
			t.Errorf("test compose missing healthcheck dependency")
		}
		if !strings.Contains(tcStr, "http://app:8000") {
			t.Errorf("BASE_URL should be http://app:8000")
		}
	}
}
