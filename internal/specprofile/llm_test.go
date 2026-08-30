package specprofile

import (
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestExtractPlaywrightTestDir(t *testing.T) {
	tests := []struct {
		name           string
		requiredFiles  []string
		layoutRoot     string
		expected       string
	}{
		{
			name: "standard test/ directory under layout_root",
			requiredFiles: []string{
				"finally/test/dashboard.spec.ts",
				"finally/test/playwright.config.ts",
				"finally/test/mvp.spec.ts",
			},
			layoutRoot: "finally",
			expected:   "finally/test",
		},
		{
			name: "nested test/e2e/ directory",
			requiredFiles: []string{
				"finally/test/e2e/login.spec.ts",
				"finally/test/e2e/playwright.config.ts",
			},
			layoutRoot: "finally",
			expected:   "finally/test/e2e",
		},
		{
			name: "frontend/e2e/ directory",
			requiredFiles: []string{
				"myapp/frontend/e2e/app.spec.ts",
				"myapp/frontend/e2e/playwright.config.ts",
			},
			layoutRoot: "myapp",
			expected:   "myapp/frontend/e2e",
		},
		{
			name: "files at layout_root (no subdirectory)",
			requiredFiles: []string{
				"dashboard.spec.ts",
				"playwright.config.ts",
			},
			layoutRoot: "myapp",
			expected:   "myapp",
		},
		{
			name: "empty layout_root (repo root)",
			requiredFiles: []string{
				"test/dashboard.spec.ts",
				"test/playwright.config.ts",
			},
			layoutRoot: "",
			expected:   "test",
		},
		{
			name: "multiple test dirs - picks most common parent",
			requiredFiles: []string{
				"finally/test/a.spec.ts",
				"finally/test/b.spec.ts",
				"finally/other/c.spec.ts",
			},
			layoutRoot: "finally",
			expected:   "finally",
		},
		{
			name: "only playwright config file",
			requiredFiles: []string{
				"finally/test/playwright.config.ts",
			},
			layoutRoot: "finally",
			expected:   "finally/test",
		},
		{
			name: "no playwright files",
			requiredFiles: []string{
				"finally/src/app.ts",
				"finally/src/components/Button.tsx",
			},
			layoutRoot: "finally",
			expected:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &orchestrator.DeliveryPhase{RequiredFiles: tt.requiredFiles}
			result := extractPlaywrightTestDir(p.RequiredFiles, tt.layoutRoot)
			if result != tt.expected {
				t.Errorf("extractPlaywrightTestDir(%v, %q) = %q, want %q", tt.requiredFiles, tt.layoutRoot, result, tt.expected)
			}
		})
	}
}

func TestDefaultQAVerifyForPhase_Playwright(t *testing.T) {
	tests := []struct {
		name          string
		requiredFiles []string
		layoutRoot    string
		expectedCmd   string
	}{
		{
			name: "Playwright in test/ directory",
			requiredFiles: []string{
				"finally/test/dashboard.spec.ts",
				"finally/test/playwright.config.ts",
			},
			layoutRoot:  "finally",
			expectedCmd: "cd finally/test && npm install --ignore-scripts && npx playwright test",
		},
		{
			name: "Playwright in nested test/e2e/ directory",
			requiredFiles: []string{
				"myapp/test/e2e/login.spec.ts",
				"myapp/test/e2e/playwright.config.ts",
			},
			layoutRoot:  "myapp",
			expectedCmd: "cd myapp/test/e2e && npm install --ignore-scripts && npx playwright test",
		},
		{
			name: "Playwright with docker-compose.test (should use docker)",
			requiredFiles: []string{
				"finally/test/docker-compose.test.yml",
				"finally/test/dashboard.spec.ts",
			},
			layoutRoot:  "finally",
			expectedCmd: "cd finally && docker compose -f test/docker-compose.test.yml up --build --abort-on-container-exit",
		},
		{
			name: "TypeScript files (not Playwright) -> frontend/ tsc",
			requiredFiles: []string{
				"myapp/frontend/src/App.tsx",
				"myapp/frontend/src/components/Button.tsx",
			},
			layoutRoot:  "myapp",
			expectedCmd: "cd myapp/frontend && npm install --ignore-scripts && npx tsc --noEmit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &orchestrator.DeliveryPhase{RequiredFiles: tt.requiredFiles}
			result := defaultQAVerifyForPhase(p, tt.layoutRoot)
			// Check that expected command is contained in result (hardened command may have extra prefixes)
			if !strings.Contains(result, tt.expectedCmd) {
				t.Errorf("defaultQAVerifyForPhase(%v, %q) = %q, want to contain %q", tt.requiredFiles, tt.layoutRoot, result, tt.expectedCmd)
			}
		})
	}
}