package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListImplementLikeBeadsOpenOrInProgress_includesFlatInvalidTitles(t *testing.T) {
	dir := t.TempDir()
	rig := "testrig"
	beadsDir := filepath.Join(dir, rig, ".beads")
	if err := os.MkdirAll(beadsDir, 0755); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:        "linkshelf",
		BeadTitleContains: "Implement linkshelf/",
		RequiredFiles: []string{
			"linkshelf/internal/api/handlers.go",
		},
	}
	if !RequiresExactImplementPaths(v) {
		t.Fatal("test profile should require exact paths")
	}
	title := "Implement linkshelf/handlers.go per architecture"
	if MatchesImplementBeadTitle(title, v) {
		t.Fatal("flat title must not match queue bead title")
	}
	if !looksLikeOpenImplementBeadTitle(title, v) {
		t.Fatal("flat title should look implement-like for prune")
	}
}

func TestPruneNonRequiredOpenImplementBeads_pathNotInRequired(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot:        "linkshelf",
		BeadTitleContains: "Implement linkshelf/",
		RequiredFiles: []string{
			"linkshelf/internal/api/handlers.go",
			"linkshelf/internal/store/store.go",
		},
	}
	p := NormalizePlannerBeadPath("linkshelf/handlers.go", v.LayoutRoot, "testrig")
	if pathMatchesRequiredForProfile(p, v.RequiredFiles, v) {
		t.Fatal("flat handlers.go must not match nested required_files")
	}
	p2 := NormalizePlannerBeadPath("linkshelf/store_test.go", v.LayoutRoot, "testrig")
	if pathMatchesRequiredForProfile(p2, v.RequiredFiles, v) {
		t.Fatal("store_test.go must not match required_files")
	}
}
