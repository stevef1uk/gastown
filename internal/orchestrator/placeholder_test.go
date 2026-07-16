package orchestrator

import (
	"testing"
)

func TestIsPlaceholderFile(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"finally/db/.gitkeep", true},
		{"finally/.gitignore", true},
		{"finally/db/.keep", true},
		{"finally/frontend/.gitkeep", true},
		{"finally/db/.gitkeep ", true}, // with trailing space
		{"finally/db/.GITKEEP", true},  // case insensitive
		{"finally/backend/main.go", false},
		{"finally/frontend/index.tsx", false},
		{"finally/.env", false},
		{"finally/Dockerfile", false},
		{"finally/README.md", false},
		{"", false},
		{".gitkeep", true},
		{".gitignore", true},
		{".keep", true},
		{"path/to/.gitkeep", true},
		{"path/to/.gitignore", true},
		{"path/to/.keep", true},
	}

	for _, tc := range tests {
		result := IsPlaceholderFile(tc.path)
		if result != tc.expected {
			t.Errorf("IsPlaceholderFile(%q) = %v, want %v", tc.path, result, tc.expected)
		}
	}
}
