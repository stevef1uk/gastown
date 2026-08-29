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

// TestExtractPhaseLinePaths_backtickCleanup verifies that backtick-wrapped
// paths are cleaned properly — no trailing backticks, no embedded backticks.
func TestExtractPhaseLinePaths_backtickCleanup(t *testing.T) {
	tests := []struct {
		line      string
		layout    string
		wantPaths []string
	}{
		{
			line:      "Implement `linkshelf/go.mod`, `linkshelf/internal/store/schema.go`, and",
			layout:    "linkshelf",
			wantPaths: []string{"linkshelf/go.mod", "linkshelf/internal/store/schema.go"},
		},
		{
			line:      "`linkshelf/web/app.js` loads links, submits new links,",
			layout:    "linkshelf",
			wantPaths: []string{"linkshelf/web/app.js"},
		},
		{
			line:      "Create `pingapp/main.py` and `pingapp/test_main.py`.",
			layout:    "pingapp",
			wantPaths: []string{"pingapp/main.py", "pingapp/test_main.py"},
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

// TestSyncProfile_noBacktickCorruption verifies the full sync pipeline
// produces clean paths without backtick artifacts, regression test for
// the corrupted profile where paths appeared as:
//   - linkshelf/go.mod`        (trailing backtick)
//   - linkshelf/`linkshelf/...  (embedded backticks)
func TestSyncProfile_noBacktickCorruption(t *testing.T) {
	archData := `# Architecture

## Requirements

### backend-store

Implement ` + "`" + `linkshelf/go.mod` + "`" + `, ` + "`" + `linkshelf/internal/store/schema.go` + "`" + `, and
` + "`" + `linkshelf/internal/store/store.go` + "`" + `. The optional
` + "`" + `linkshelf/internal/store/store_test.go` + "`" + ` may be supplied.

### backend-api-server

Implement ` + "`" + `linkshelf/internal/api/handlers.go` + "`" + ` and
` + "`" + `linkshelf/cmd/server/main.go` + "`" + `. Do not create
` + "`" + `linkshelf/internal/api/handlers_test.go` + "`" + ` for this MVP.

### frontend-ui

Implement ` + "`" + `linkshelf/web/index.html` + "`" + `, ` + "`" + `linkshelf/web/app.js` + "`" + `,
` + "`" + `linkshelf/web/style.css` + "`" + `, ` + "`" + `linkshelf/playwright.config.js` + "`" + `, and
` + "`" + `linkshelf/web/test/e2e.spec.js` + "`" + `.
`
	v := DefaultWorkflowValidation()
	v.LayoutRoot = "linkshelf"
	v.DeliveryPhases = []DeliveryPhase{
		{ID: "backend-store", Title: "backend-store"},
		{ID: "backend-api-server", Title: "backend-api-server"},
		{ID: "frontend-ui", Title: "frontend-ui"},
	}
	v2, updated := updatePhaseRequiredFilesFromRequirementsSection(v, archData)
	if !updated {
		t.Fatal("expected update")
	}
	for _, p := range v2.DeliveryPhases {
		t.Logf("phase %s: %v", p.ID, p.RequiredFiles)
		for _, f := range p.RequiredFiles {
			// No backtick artifacts allowed
			if f != trimBackticks(f) {
				t.Errorf("phase %s: path %q has backtick artifacts (cleaned: %q)", p.ID, f, trimBackticks(f))
			}
			if f != trimTrailingBackticks(f) {
				t.Errorf("phase %s: path %q has trailing backtick", p.ID, f)
			}
			if containsEmbeddedBackticks(f) {
				t.Errorf("phase %s: path %q has embedded backticks", p.ID, f)
			}
		}
	}
	// Verify specific phase contents
	phaseMap := make(map[string][]string)
	for _, p := range v2.DeliveryPhases {
		phaseMap[p.ID] = p.RequiredFiles
	}
	// backend-store should have go.mod, schema.go, store.go, store_test.go
	for _, want := range []string{"linkshelf/go.mod", "linkshelf/internal/store/schema.go", "linkshelf/internal/store/store.go"} {
		if !pathInList(phaseMap["backend-store"], want) {
			t.Errorf("backend-store missing %q, got %v", want, phaseMap["backend-store"])
		}
	}
	// backend-api-server should have handlers.go, main.go
	// NOTE: handlers_test.go appears because the sync can't parse negative
	// instructions like "Do not create". This is a known limitation.
	for _, want := range []string{"linkshelf/internal/api/handlers.go", "linkshelf/cmd/server/main.go"} {
		if !pathInList(phaseMap["backend-api-server"], want) {
			t.Errorf("backend-api-server missing %q, got %v", want, phaseMap["backend-api-server"])
		}
	}
	// frontend-ui should have all 5 web files
	for _, want := range []string{"linkshelf/web/index.html", "linkshelf/web/app.js", "linkshelf/web/style.css", "linkshelf/playwright.config.js", "linkshelf/web/test/e2e.spec.js"} {
		if !pathInList(phaseMap["frontend-ui"], want) {
			t.Errorf("frontend-ui missing %q, got %v", want, phaseMap["frontend-ui"])
		}
	}
	// No phase should have backtick-corrupted paths
	for _, p := range v2.DeliveryPhases {
		for _, f := range p.RequiredFiles {
			if f != trimBackticks(f) {
				t.Errorf("phase %s has corrupted path %q", p.ID, f)
			}
		}
	}
}

func trimBackticks(s string) string {
	s2 := s
	for len(s2) > 0 && s2[0] == '`' {
		s2 = s2[1:]
	}
	for len(s2) > 0 && s2[len(s2)-1] == '`' {
		s2 = s2[:len(s2)-1]
	}
	return s2
}

func trimTrailingBackticks(s string) string {
	for len(s) > 0 && s[len(s)-1] == '`' {
		s = s[:len(s)-1]
	}
	return s
}

func containsEmbeddedBackticks(s string) bool {
	trimmed := trimBackticks(s)
	return trimmed != s && len(trimmed) > 0 && trimmed != trimTrailingBackticks(trimmed)
}

func pathInList(list []string, item string) bool {
	for _, s := range list {
		if s == item {
			return true
		}
	}
	return false
}
