package orchestrator

import (
	"testing"
)

func TestRequiredFilesForCompletedAndActive(t *testing.T) {
	// Simulate the linkshelf workflow-profile.json phases
	v := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		BeadTitleContains: "Implement linkshelf/",
		RequiredFiles: []string{
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
		},
		DeliveryPhases: []DeliveryPhase{
			{
				ID:    "backend-store",
				Title: "Data Model & Store",
				RequiredFiles: []string{
					"linkshelf/go.mod",
					"linkshelf/internal/store/schema.go",
					"linkshelf/internal/store/store.go",
					"linkshelf/internal/store/store_test.go",
				},
			},
			{
				ID:    "backend-api-server",
				Title: "API Server",
				RequiredFiles: []string{
					"linkshelf/internal/api/handlers.go",
					"linkshelf/cmd/server/main.go",
				},
			},
			{
				ID:    "backend-smoke",
				Title: "Backend Smoke",
				RequiredFiles: []string{
					"linkshelf/internal/api/handlers_test.go",
				},
			},
			{
				ID:    "frontend-ui",
				Title: "Frontend UI",
				RequiredFiles: []string{
					"linkshelf/web/index.html",
					"linkshelf/web/app.js",
					"linkshelf/web/style.css",
				},
			},
			{
				ID:    "frontend-e2e",
				Title: "Frontend E2E",
				RequiredFiles: []string{
					"linkshelf/web/test/e2e.spec.js",
					"linkshelf/docker-compose.yml",
				},
			},
		},
		CompletedPhaseIDsField: []string{"backend-store", "backend-api-server"},
		ActivePhaseIDField:     "backend-smoke",
	}

	// Test Completed + Active (completed: backend-store, backend-api-server; active: backend-smoke)
	expected := []string{
		"linkshelf/go.mod",
		"linkshelf/internal/store/schema.go",
		"linkshelf/internal/store/store.go",
		"linkshelf/internal/store/store_test.go",
		"linkshelf/internal/api/handlers.go",
		"linkshelf/cmd/server/main.go",
		"linkshelf/internal/api/handlers_test.go",
	}

	result := v.RequiredFilesForCompletedAndActive()

	if len(result) != len(expected) {
		t.Errorf("Expected %d files, got %d:\nExpected: %v\nGot: %v", len(expected), len(result), expected, result)
		return
	}

	seen := make(map[string]bool)
	for _, f := range result {
		seen[f] = true
	}
	for _, f := range expected {
		if !seen[f] {
			t.Errorf("Missing expected file: %q", f)
		}
	}

	// Frontend files should NOT be included (future phases)
	frontendFiles := []string{
		"linkshelf/web/index.html",
		"linkshelf/web/app.js",
		"linkshelf/web/style.css",
		"linkshelf/web/test/e2e.spec.js",
		"linkshelf/docker-compose.yml",
	}
	for _, f := range frontendFiles {
		if seen[f] {
			t.Errorf("Frontend file %q should NOT be included (future phase)", f)
		}
	}
}

func TestRequiredFilesForCompletedAndActive_NoPhases(t *testing.T) {
	// Unphased workflow - should return all required_files
	v := WorkflowValidation{
		RequiredFiles: []string{
			"file1.go",
			"file2.go",
			"file3.go",
		},
	}

	result := v.RequiredFilesForCompletedAndActive()

	if len(result) != 3 {
		t.Errorf("Expected 3 files, got %d: %v", len(result), result)
	}
}

func TestRequiredFilesForCompletedAndActive_OnlyActive(t *testing.T) {
	// No completed phases, only active
	v := WorkflowValidation{
		LayoutRoot:    "app",
		BeadTitleContains: "Implement ",
		RequiredFiles: []string{
			"app/file1.go",
			"app/file2.go",
			"app/file3.go",
		},
		DeliveryPhases: []DeliveryPhase{
			{
				ID: "phase1",
				RequiredFiles: []string{
					"app/file1.go",
				},
			},
			{
				ID: "phase2",
				RequiredFiles: []string{
					"app/file2.go",
				},
			},
		},
		CompletedPhaseIDsField: []string{},
		ActivePhaseIDField:     "phase1",
	}

	result := v.RequiredFilesForCompletedAndActive()

	if len(result) != 1 || result[0] != "app/file1.go" {
		t.Errorf("Expected only active phase file, got: %v", result)
	}
}