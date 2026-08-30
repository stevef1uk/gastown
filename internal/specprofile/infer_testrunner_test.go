package specprofile

import (
	"testing"
)

func TestInferTestRunner_PrioritizesTestFiles(t *testing.T) {
	tests := []struct {
		name     string
		paths    []string
		expected string
	}{
		{
			name: "Python test files prioritized over TypeScript source",
			paths: []string{
				"finally/frontend/src/components/watchlist.tsx",
				"finally/frontend/src/app/page.tsx",
				"finally/backend/tests/test_market.py",
				"finally/backend/tests/test_portfolio.py",
			},
			expected: "pytest",
		},
		{
			name: "Go test files prioritized over Python source",
			paths: []string{
				"helloapi/internal/store/store.go",
				"helloapi/internal/store/store_test.go",
				"helloapi/cmd/server/main.go",
			},
			expected: "go",
		},
		{
			name: "TypeScript spec files detected",
			paths: []string{
				"test/specs/dashboard.spec.ts",
				"test/pages/login.page.ts",
				"frontend/src/app.tsx",
			},
			expected: "npm",
		},
		{
			name: "JavaScript test files detected",
			paths: []string{
				"test/unit/math.test.js",
				"src/utils.js",
			},
			expected: "npm",
		},
		{
			name: "Fallback to Python source when no test files",
			paths: []string{
				"backend/app/main.py",
				"backend/app/models.py",
			},
			expected: "pytest",
		},
		{
			name: "Fallback to Go source when no test files",
			paths: []string{
				"helloapi/main.go",
				"helloapi/handler.go",
			},
			expected: "go",
		},
		{
			name: "Fallback to npm for JS source when no test files",
			paths: []string{
				"frontend/src/App.tsx",
				"frontend/src/components/Button.tsx",
			},
			expected: "npm",
		},
		{
			name: "Mixed: Python test files win over Go test files",
			paths: []string{
				"helloapi/internal/store/store_test.go",
				"finally/backend/tests/test_api.py",
			},
			expected: "pytest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := inferTestRunner(tt.paths)
			if result != tt.expected {
				t.Errorf("inferTestRunner(%v) = %q, want %q", tt.paths, result, tt.expected)
			}
		})
	}
}