package orchestrator

import (
	"reflect"
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
// TestSplitOverlargePhases_dedupFirst verifies that splitOverlargePhases
// doesn't create -1/-2 suffixes when dedup runs first and reduces file count
// below maxFilesPerPhase. Regression for Architect infinite loop where
// backend-store with 11 entries (dupes) split into backend-store-1/2 but
// architecture.md only had ### backend-store heading.
func TestSplitOverlargePhases_dedupFirst(t *testing.T) {
	v := WorkflowValidation{
		DeliveryPhases: []DeliveryPhase{
			{ID: "backend-store", Title: "Data Model & Store", RequiredFiles: []string{
				"linkshelf/go.mod",
				"linkshelf/internal/store/schema.go",
				"linkshelf/internal/store/store.go",
				"linkshelf/SPEC.md",
				"linkshelf/package.json",
				"linkshelf/.gastown/workflow-profile.json",
				"linkshelf/.gastown/workflow-profile.json", // dupe
				"linkshelf/.gastown/workflow-profile.json", // dupe
				"linkshelf/.gastown/workflow-profile.json", // dupe
				"linkshelf/internal/store/store_test.go",
				"linkshelf/internal/api/handlers.go", // unrelated
			}},
		},
	}
	// Before fix: 11 files > 10 -> splits to backend-store-1, backend-store-2
	// After dedup: 8 unique files <= 20 -> no split
	v = ClampProfileValidation(v)
	
	if len(v.DeliveryPhases) != 1 {
		t.Fatalf("expected 1 phase after dedup+split, got %d: %v", len(v.DeliveryPhases), v.DeliveryPhases)
	}
	if v.DeliveryPhases[0].ID != "backend-store" {
		t.Fatalf("expected phase ID 'backend-store', got %q", v.DeliveryPhases[0].ID)
	}
}

// TestParseArchPhases_fromRequirementsSection verifies parseArchPhases
// extracts phases from the "## Requirements" section with ### headings
// instead of the table in "## Delivery phases".
func TestParseArchPhases_fromRequirementsSection(t *testing.T) {
	arch := `# Architecture

## Delivery phases

| Phase | Files |
|---|---|
| backend-store | go.mod, store.go |
| frontend-ui | index.html |

## Requirements

### backend-store

In **linkshelf/internal/store/schema.go** and **linkshelf/internal/store/store.go**.

### backend-api-server

Creates **linkshelf/internal/api/handlers.go** and **linkshelf/cmd/server/main.go**.

### frontend-ui

Delivers **linkshelf/web/index.html**, **linkshelf/web/app.js**, **linkshelf/web/style.css**.
`
	phases := parseArchPhases(arch, "linkshelf")
	if len(phases) != 3 {
		t.Fatalf("expected 3 phases from Requirements section, got %d", len(phases))
	}
	ids := make([]string, len(phases))
	for i, p := range phases {
		ids[i] = p.ID
	}
	expected := []string{"backend-store", "backend-api-server", "frontend-ui"}
	if !reflect.DeepEqual(ids, expected) {
		t.Fatalf("phase IDs = %v, want %v", ids, expected)
	}
	// Check files extracted
	for _, p := range phases {
		if len(p.RequiredFiles) == 0 {
			t.Errorf("phase %q has 0 required_files", p.ID)
		}
	}
}
