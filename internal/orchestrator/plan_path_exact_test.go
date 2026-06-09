package orchestrator

import "testing"

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
