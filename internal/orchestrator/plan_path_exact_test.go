package orchestrator

import (
	"testing"
)

func TestValidatePlanBeadPathsExact_normalizesBothSides(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:        "finally",
		BeadTitleContains: "Implement finally/",
		RequiredFiles:     []string{"finally/backend/services/massive.py"},
	}
	beads := []PlanBead{
		{ID: "fi-3fz", Title: "Implement finally/backend/services/massive.py per architecture"},
	}
	if err := ValidatePlanBeadPathsExact(beads, v, "finally"); err != nil {
		t.Fatalf("expected no error when both sides normalize to same path, got: %v", err)
	}
	// Bead with non-matching path should still be rejected
	badBeads := []PlanBead{
		{ID: "fi-xxx", Title: "Implement finally/backend/wrong.py per architecture"},
	}
	if err := ValidatePlanBeadPathsExact(badBeads, v, "finally"); err == nil {
		t.Fatal("expected error for bead path not in required_files")
	}
}

func TestRequiresExactImplementPaths_nestedLayout(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot: "linkshelf",
		RequiredFiles: []string{
			"linkshelf/internal/store/schema.go",
			"linkshelf/go.mod",
		},
	}
	if !RequiresExactImplementPaths(v) {
		t.Fatal("expected exact paths for nested linkshelf layout")
	}
}

func TestRequiresExactImplementPaths_flatDockerfile(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:    ".",
		RequiredFiles: []string{"Dockerfile", "docker-compose.yml"},
	}
	if RequiresExactImplementPaths(v) {
		t.Fatal("flat docker rig should allow basename matching")
	}
}

func TestRequiresExactImplementPaths_duplicateBasenames(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:    ".",
		RequiredFiles: []string{"frontend/package.json", "test/package.json", "frontend/tsconfig.json", "test/tsconfig.json"},
	}
	if !RequiresExactImplementPaths(v) {
		t.Fatal("duplicate basenames must force exact path matching")
	}
}

func TestPathMatchesRequiredForProfile_exactRejectsBasename(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot: "linkshelf",
		RequiredFiles: []string{
			"linkshelf/internal/store/schema.go",
		},
	}
	if pathMatchesRequiredForProfile("linkshelf/schema.go", v.RequiredFiles, v) {
		t.Fatal("basename-only path must not match nested required_files")
	}
	if !pathMatchesRequiredForProfile("linkshelf/internal/store/schema.go", v.RequiredFiles, v) {
		t.Fatal("exact path should match")
	}
}

func TestPathMatchesImplementWrite_exact(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		RequiredFiles: []string{"linkshelf/internal/api/handlers.go"},
	}
	allowed := "linkshelf/internal/api/handlers.go"
	if PathMatchesImplementWrite("linkshelf/handlers.go", allowed, v.RequiredFiles, v) {
		t.Fatal("basename-only write must not match nested bead path")
	}
	if !PathMatchesImplementWrite("linkshelf/internal/api/handlers.go", allowed, v.RequiredFiles, v) {
		t.Fatal("exact write path should match")
	}
}

func TestPathMatchesImplementFileForProfile_exact(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{LayoutRoot: "linkshelf", RequiredFiles: []string{"linkshelf/cmd/server/main.go"}}
	if PathMatchesImplementFileForProfile("linkshelf/main.go", "linkshelf/cmd/server/main.go", v) {
		t.Fatal("flattened write must not match cmd/server bead")
	}
	if !PathMatchesImplementFileForProfile("linkshelf/cmd/server/main.go", "linkshelf/cmd/server/main.go", v) {
		t.Fatal("exact paths should match")
	}
}
