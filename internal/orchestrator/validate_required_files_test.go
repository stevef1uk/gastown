package orchestrator

import (
	"path/filepath"
	"testing"
)

func TestValidateRequiredFilesHaveBeads_PathMatching(t *testing.T) {
	// This test mirrors the actual scenario from the logs
	// Required files from workflow-profile.json (ALL phases)
	allRequiredFiles := []string{
		"linkshelf/go.mod",
		"linkshelf/internal/store/schema.go",
		"linkshelf/internal/store/store.go",
		"linkshelf/internal/store/store_test.go",
		"linkshelf/internal/api/handlers.go",
		"linkshelf/cmd/server/main.go",
		"linkshelf/internal/api/handlers_test.go",
		"linkshelf/web/index.html",
		"linkshelf/web/app.js",
		"linkshelf/web/style.css",
		"linkshelf/web/test/e2e.spec.js",
		"linkshelf/docker-compose.yml",
	}

	// Bead titles from bd list (what actually exists)
	beadTitles := []string{
		"Implement linkshelf/internal/api/handlers_test.go per architecture", // te-as0
		"Implement linkshelf/cmd/server/main.go per architecture",            // te-pss
		"Implement linkshelf/internal/api/handlers.go per architecture",      // te-8w5 (in_progress)
	}

	beadTitleContains := "Implement linkshelf/"
	layoutRoot := "linkshelf"
	rig := "testgt3"

	t.Log("=== Testing UNSCOPED (all required_files) ===")
	testMatching(t, allRequiredFiles, beadTitles, beadTitleContains, layoutRoot, rig)

	// Now test with ACTIVE phase only (what the planner actually validates)
	// The active phase for "implementation" is likely backend-api-server or backend-smoke
	// which has different required_files
	activePhaseFiles := []string{
		"linkshelf/cmd/server/main.go",
		"linkshelf/internal/api/handlers.go",
		"linkshelf/internal/api/handlers_test.go",
	}

	t.Log("\n=== Testing SCOPED to active phase (implementation) ===")
	testMatching(t, activePhaseFiles, beadTitles, beadTitleContains, layoutRoot, rig)
}

func testMatching(t *testing.T, requiredFiles []string, beadTitles []string, beadTitleContains, layoutRoot, rig string) {
	matches := 0
	for _, rf := range requiredFiles {
		found := false
		for _, bt := range beadTitles {
			titlePath := ExtractPathFromBeadTitle(bt, beadTitleContains)
			normTitle := NormalizePlannerBeadPath(titlePath, layoutRoot, rig)
			normReq := filepath.ToSlash(filepath.Clean(rf))
			if normTitle == normReq {
				t.Logf("  MATCH: required=%q → bead_path=%q → norm_title=%q norm_req=%q", rf, titlePath, normTitle, normReq)
				found = true
				matches++
				break
			}
		}
		if !found {
			t.Logf("  MISSING: required=%q (no matching bead)", rf)
		}
	}

	t.Logf("Total required_files: %d, Matched: %d, Missing: %d", len(requiredFiles), matches, len(requiredFiles)-matches)
}

func TestExtractPathFromBeadTitle_Variations(t *testing.T) {
	tests := []struct {
		title            string
		prefix           string
		expectedPath     string
		expectedLayout   string
	}{
		{
			title:          "Implement linkshelf/internal/api/handlers.go per architecture",
			prefix:         "Implement linkshelf/",
			expectedPath:   "linkshelf/internal/api/handlers.go", // Actual behavior - keeps prefix
			expectedLayout: "linkshelf",
		},
		{
			title:          "Implement linkshelf/cmd/server/main.go per architecture",
			prefix:         "Implement linkshelf/",
			expectedPath:   "linkshelf/cmd/server/main.go", // Actual behavior - keeps prefix
			expectedLayout: "linkshelf",
		},
		{
			title:          "Implement pingapp/internal/store/schema.go per architecture",
			prefix:         "Implement pingapp/",
			expectedPath:   "pingapp/internal/store/schema.go", // Actual behavior - keeps prefix
			expectedLayout: "pingapp",
		},
		{
			title:          "Implement handlers.go per architecture",
			prefix:         "Implement ",
			expectedPath:   "handlers.go",
			expectedLayout: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.title, func(t *testing.T) {
			path := ExtractPathFromBeadTitle(tc.title, tc.prefix)
			if path != tc.expectedPath {
				t.Errorf("ExtractPathFromBeadTitle(%q, %q) = %q, want %q", tc.title, tc.prefix, path, tc.expectedPath)
			}
		})
	}
}