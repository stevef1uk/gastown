package orchestrator

import (
	"reflect"
	"sort"
	"testing"
)

// TestIsKebabPhaseID_singleWord verifies that single-word phase IDs like
// "core" and "test" are accepted (not just kebab-case like "backend-store").
func TestIsKebabPhaseID_singleWord(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"core", true},
		{"test", true},
		{"setup", true},
		{"backend-store", true},
		{"frontend-ui", true},
		{"integration-test", true},
		{"Canonical", false},
		{"the", false},
		{"and", false},
		{"", false},
		{"has space", false},
		{"has.dot", false},
		{"UPPER", false},
		{"|---|", false},
		{"123", false},
	}
	for _, tt := range tests {
		if got := isKebabPhaseID(tt.input); got != tt.want {
			t.Errorf("isKebabPhaseID(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// TestParseArchPhases_pythonRig verifies that phases with single-word IDs
// (core, test) are parsed correctly from architecture.md prose.
func TestParseArchPhases_pythonRig(t *testing.T) {
	arch := `# Architecture

## Requirements

### python-setup

Create pingapp/requirements.txt. It must list fastapi and uvicorn.

### core

Create pingapp/main.py. It imports FastAPI, instantiates the module-level symbol app with FastAPI(), and registers exactly one GET route at /ping.

### test

Create pingapp/test_main.py. It imports TestClient from fastapi.testclient and app from main.

### integration-test

Verify the assembled concrete files pingapp/main.py and pingapp/test_main.py after the preceding phases.
`
	phases := parseArchPhases(arch, "pingapp")
	if len(phases) < 4 {
		t.Fatalf("expected 4 phases, got %d", len(phases))
	}
	phaseMap := make(map[string][]string)
	for _, p := range phases {
		phaseMap[p.ID] = p.RequiredFiles
	}
	// core should have main.py, NOT test_main.py
	if files, ok := phaseMap["core"]; ok {
		for _, f := range files {
			if f == "pingapp/test_main.py" {
				t.Errorf("core phase should NOT have test_main.py, got %v", files)
			}
		}
		if len(files) == 0 {
			t.Error("core phase should have at least main.py")
		}
	} else {
		t.Error("core phase not found")
	}
	// test should have test_main.py
	if files, ok := phaseMap["test"]; ok {
		hasTest := false
		for _, f := range files {
			if f == "pingapp/test_main.py" {
				hasTest = true
			}
		}
		if !hasTest {
			t.Errorf("test phase should have test_main.py, got %v", files)
		}
	} else {
		t.Error("test phase not found")
	}
}

// TestParseArchPhases_goRigKebab verifies kebab-case phases still work.
func TestParseArchPhases_goRigKebab(t *testing.T) {
	arch := `# Architecture

## Requirements

### backend-store

Deliver linkshelf/internal/store/schema.go, linkshelf/internal/store/store.go.

### backend-api-server

Creates **linkshelf/internal/api/handlers.go** and **linkshelf/cmd/server/main.go**.

### frontend-ui

Delivers **linkshelf/web/index.html**, **linkshelf/web/app.js**, **linkshelf/web/style.css**.
`
	phases := parseArchPhases(arch, "linkshelf")
	if len(phases) != 3 {
		t.Fatalf("expected 3 phases, got %d", len(phases))
	}
	phaseMap := make(map[string][]string)
	for _, p := range phases {
		phaseMap[p.ID] = p.RequiredFiles
	}
	if files, ok := phaseMap["backend-store"]; ok {
		if len(files) < 2 {
			t.Errorf("backend-store should have 2+ files, got %v", files)
		}
	} else {
		t.Error("backend-store phase not found")
	}
}

// TestParseArchPhases_jsRig verifies JS/Node rigs with mixed prose formats.
func TestParseArchPhases_jsRig(t *testing.T) {
	arch := `# Architecture

## Requirements

### setup

Create myapp/package.json with express dependency.

### routes

Create myapp/src/routes.js with Express router.

### views

Create myapp/views/index.html with EJS template.

### tests

Create myapp/test/routes.test.js with supertest assertions.
`
	phases := parseArchPhases(arch, "myapp")
	if len(phases) < 4 {
		t.Fatalf("expected 4 phases, got %d", len(phases))
	}
	phaseMap := make(map[string][]string)
	for _, p := range phases {
		phaseMap[p.ID] = p.RequiredFiles
		t.Logf("phase %s: %v", p.ID, p.RequiredFiles)
	}
	if _, ok := phaseMap["setup"]; !ok {
		t.Error("setup phase not found")
	}
	if _, ok := phaseMap["routes"]; !ok {
		t.Error("routes phase not found")
	}
}

// TestExtractPhaseLinePaths_prosePath verifies that file paths embedded in
// prose sentences like "Create pingapp/main.py. It imports..." are extracted.
func TestExtractPhaseLinePaths_prosePath(t *testing.T) {
	tests := []struct {
		line      string
		layout    string
		wantPaths []string
	}{
		{
			line:      "Create pingapp/main.py. It imports FastAPI.",
			layout:    "pingapp",
			wantPaths: []string{"pingapp/main.py"},
		},
		{
			line:      "Create myapp/src/routes.js with Express router.",
			layout:    "myapp",
			wantPaths: []string{"myapp/src/routes.js"},
		},
		{
			line:      "Deliver linkshelf/go.mod, linkshelf/internal/store/schema.go.",
			layout:    "linkshelf",
			wantPaths: []string{"linkshelf/go.mod", "linkshelf/internal/store/schema.go"},
		},
	}
	for _, tt := range tests {
		got := extractPhaseLinePaths(tt.line, tt.layout)
		sort.Strings(got)
		sort.Strings(tt.wantPaths)
		if !reflect.DeepEqual(got, tt.wantPaths) {
			t.Errorf("extractPhaseLinePaths(%q, %q) = %v, want %v", tt.line, tt.layout, got, tt.wantPaths)
		}
	}
}

// TestExtractInlinePaths_dynamicLayout verifies extractInlinePaths uses layoutRoot
// dynamically instead of hardcoding "linkshelf/".
func TestExtractInlinePaths_dynamicLayout(t *testing.T) {
	tests := []struct {
		line      string
		layout    string
		wantPaths []string
	}{
		{
			line:      "Create pingapp/main.py and pingapp/test_main.py.",
			layout:    "pingapp",
			wantPaths: []string{"pingapp/main.py", "pingapp/test_main.py"},
		},
		{
			line:      "Deliver linkshelf/go.mod.",
			layout:    "linkshelf",
			wantPaths: []string{"linkshelf/go.mod"},
		},
		{
			line:      "Create myapp/src/routes.js.",
			layout:    "myapp",
			wantPaths: []string{"myapp/src/routes.js"},
		},
	}
	for _, tt := range tests {
		got := extractInlinePaths(tt.line, tt.layout)
		sort.Strings(got)
		sort.Strings(tt.wantPaths)
		if !reflect.DeepEqual(got, tt.wantPaths) {
			t.Errorf("extractInlinePaths(%q, %q) = %v, want %v", tt.line, tt.layout, got, tt.wantPaths)
		}
	}
}
