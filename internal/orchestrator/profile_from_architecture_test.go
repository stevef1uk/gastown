package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnrichWorkflowValidationFromArchitecture_pythonPingapp(t *testing.T) {
	dir := t.TempDir()
	arch := `# Architecture
- ` + "`pingapp/requirements.txt`" + `
- ` + "`pingapp/main.py`" + `
- ` + "`pingapp/test_main.py`" + `
`
	if err := os.WriteFile(filepath.Join(dir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		QAVerifyCommand: "python3 -m pytest -v pingapp/test_main.py",
	}
	got := EnrichWorkflowValidationFromArchitecture(v, dir)
	if !WorkflowUsesPython(got) {
		t.Fatal("expected python workflow")
	}
	if got.LayoutRoot != "pingapp" {
		t.Fatalf("layout_root = %q, want pingapp", got.LayoutRoot)
	}
	if got.RequirementsFilePath() != "pingapp/requirements.txt" {
		t.Fatalf("requirements = %q", got.RequirementsFilePath())
	}
	if !strings.Contains(got.ProjectSetupVerifyHint(), "import pytest") {
		t.Fatalf("verify hint = %q", got.ProjectSetupVerifyHint())
	}
}

func TestEnrichWorkflowValidation_prefersSpecOverBadProfile(t *testing.T) {
	dir := t.TempDir()
	spec := `# PingApp

## Layout

` + "```" + `
pingapp/
├── requirements.txt
├── main.py
└── test_main.py
` + "```" + `

## HTTP API
| GET | /ping |
`
	arch := `# Architecture
- ./requirements.txt
- ./main.py
`
	if err := os.WriteFile(filepath.Join(dir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	// Bad spec-index profile (flat layout) — SPEC must win.
	v := WorkflowValidation{
		LayoutRoot:        ".",
		BeadTitleContains: "Implement ./",
		RequiredFiles:     []string{"requirements.txt", "main.py", "test_main.py"},
		QAVerifyCommand:   "cd . && pytest",
	}
	got := EnrichWorkflowValidationFromArchitecture(v, dir)
	if got.LayoutRoot != "pingapp" {
		t.Fatalf("layout_root = %q, want pingapp from SPEC", got.LayoutRoot)
	}
	if got.RequirementsFilePath() != "pingapp/requirements.txt" {
		t.Fatalf("requirements = %q", got.RequirementsFilePath())
	}
	if !strings.Contains(got.BeadTitleContains, "pingapp") {
		t.Fatalf("bead_title_contains = %q", got.BeadTitleContains)
	}
}

func TestParseSpecLayoutTree(t *testing.T) {
	spec := "## Layout\n\n```\npingapp/\n├── requirements.txt\n├── main.py\n└── test_main.py\n```\n"
	got := parseSpecLayoutTree(spec)
	if len(got) != 3 {
		t.Fatalf("paths = %v", got)
	}
	if got[0] != "pingapp/requirements.txt" {
		t.Fatalf("first = %q", got[0])
	}
}

func TestFormatProjectSetupStackBlock_pythonForbidsGo(t *testing.T) {
	v := WorkflowValidation{
		QAVerifyCommand: "python3 -m pytest -v pingapp/test_main.py",
		RequiredFiles:   []string{"pingapp/main.py", "pingapp/requirements.txt"},
		LayoutRoot:      "pingapp",
	}
	v = SanitizeRigFlowProfile(v)
	block := FormatProjectSetupStackBlock(v)
	if !strings.Contains(block, "Python") || !strings.Contains(block, "go mod") {
		t.Fatalf("expected python-only block with go forbidden: %s", block)
	}
}

func TestFormatProjectSetupStackBlock_nodejsPhase(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot:         ".",
		BeadTitleContains:  "Implement finally/",
		QAVerifyCommand:    "cd backend && pytest && cd ../frontend && npm test",
		TestRunner:         "pytest",
		ActivePhaseIDField: "frontend-ui",
		DeliveryPhases: []DeliveryPhase{
			{
				ID:              "frontend-ui",
				RequiredFiles:   []string{"frontend/components/Watchlist.tsx"},
				QAVerifyCommand: "cd frontend && npm test",
			},
		},
	}
	block := FormatProjectSetupStackBlock(v)
	if !strings.Contains(block, "Node.js") {
		t.Fatalf("expected Node.js stack block, got: %s", block)
	}
	if !strings.Contains(block, "npm install") {
		t.Fatalf("expected Node verify command in block, got: %s", block)
	}
	if strings.Contains(block, "Python") {
		t.Fatalf("Node.js block should not mention Python, got: %s", block)
	}
}

func TestProjectSetupFailureHint_nodejs(t *testing.T) {
	v := WorkflowValidation{
		QAVerifyCommand: "cd frontend && npm test",
		RequiredFiles:   []string{"frontend/app.tsx"},
	}
	hint := ProjectSetupFailureHint(v)
	if !strings.Contains(hint, "npm install") {
		t.Fatalf("expected npm install in failure hint, got: %s", hint)
	}
	if strings.Contains(hint, "Python") {
		t.Fatalf("Node.js failure hint should not mention Python, got: %s", hint)
	}
}

func TestReconcileProfileWithArchitecture_addsNewPaths(t *testing.T) {
	townRoot := t.TempDir()
	rig := "testrig"
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	profileDir := filepath.Join(rigDir, ".gastown")
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		t.Fatal(err)
	}
	profile := rigProfileEnvelope{
		Version: 1,
		Validation: WorkflowValidation{
			RequiredFiles: []string{"backend/main.py", "frontend/app.tsx"},
			DeliveryPhases: []DeliveryPhase{
				{
					ID:            "backend-core",
					RequiredFiles: []string{"backend/main.py"},
				},
				{
					ID:            "frontend-ui",
					RequiredFiles: []string{"frontend/app.tsx"},
				},
				{
					ID:            "e2e-deploy",
					RequiredFiles: []string{"Dockerfile"},
				},
			},
		},
	}
	data, _ := marshalRigProfileJSON(profile)
	if err := os.WriteFile(filepath.Join(profileDir, "workflow-profile.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	arch := "# Architecture\n\n- `scripts/start.sh`\n- `scripts/stop.sh`\n- `backend/main.py`\n"
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileProfileWithArchitecture(townRoot, rig); err != nil {
		t.Fatal(err)
	}
	saved, _, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, f := range saved.RequiredFiles {
		found[f] = true
	}
	if !found["scripts/start.sh"] || !found["scripts/stop.sh"] {
		t.Fatalf("expected scripts/ in required_files, got %v", saved.RequiredFiles)
	}
	if found["backend/main.py"] != true {
		t.Fatal("backend/main.py should still be present")
	}
	lastPhase := saved.DeliveryPhases[len(saved.DeliveryPhases)-1]
	phaseFound := map[string]bool{}
	for _, f := range lastPhase.RequiredFiles {
		phaseFound[f] = true
	}
	if !phaseFound["scripts/start.sh"] || !phaseFound["scripts/stop.sh"] {
		t.Fatalf("expected scripts/ in final phase, got %v", lastPhase.RequiredFiles)
	}
}

func TestReconcileProfileWithArchitecture_noNewPaths(t *testing.T) {
	townRoot := t.TempDir()
	rig := "testrig"
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	profileDir := filepath.Join(rigDir, ".gastown")
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		t.Fatal(err)
	}
	profile := rigProfileEnvelope{
		Version: 1,
		Validation: WorkflowValidation{
			RequiredFiles: []string{"backend/main.py"},
		},
	}
	data, _ := marshalRigProfileJSON(profile)
	if err := os.WriteFile(filepath.Join(profileDir, "workflow-profile.json"), data, 0644); err != nil {
		t.Fatal(err)
	}
	arch := "# Architecture\n\n- `backend/main.py`\n"
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileProfileWithArchitecture(townRoot, rig); err != nil {
		t.Fatal(err)
	}
	saved, _, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.RequiredFiles) != 1 || saved.RequiredFiles[0] != "backend/main.py" {
		t.Fatalf("expected no change, got %v", saved.RequiredFiles)
	}
}

func TestParseSpecLayoutTree_nestedHandler(t *testing.T) {
	// req_flow_rig style tree with nested handler/ directory
	spec := "## Layout\n\n```\nhelloapi/\n├── go.mod\n├── main.go\n├── handler/\n│   └── hello.go\n└── handler/\n    └── hello_test.go\n```\n"
	got := parseSpecLayoutTree(spec)
	if len(got) != 4 {
		t.Fatalf("paths = %v", got)
	}
	want := []string{
		"helloapi/go.mod",
		"helloapi/main.go",
		"helloapi/handler/hello.go",
		"helloapi/handler/hello_test.go",
	}
	for _, w := range want {
		found := false
		for _, g := range got {
			if g == w {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing %q in %v", w, got)
		}
	}
}

func TestParseSpecLayoutTree_flatIndented(t *testing.T) {
	// ping_rig style flat indented tree (no connectors)
	spec := "## Layout\n\n```\npingapp/\n  requirements.txt\n  main.py\n  test_main.py\n```\n"
	got := parseSpecLayoutTree(spec)
	if len(got) != 3 {
		t.Fatalf("paths = %v", got)
	}
	want := map[string]bool{
		"pingapp/requirements.txt": true,
		"pingapp/main.py":          true,
		"pingapp/test_main.py":     true,
	}
	for _, g := range got {
		if !want[g] {
			t.Fatalf("unexpected %q in %v", g, got)
		}
	}
}

func TestExtractSpecLayoutPaths_treeWinsOverProse(t *testing.T) {
	// SPEC has tree with nested paths AND prose with bare paths for same files.
	// The tree should win; bare paths should not appear in the authoritative set.
	spec := "# Hello World API\n\n## Layout\n\n```\nhelloapi/\n├── go.mod\n├── main.go\n├── handler/\n│   └── hello.go\n└── handler/\n    └── hello_test.go\n```\n\n## File Layout\n- `go.mod` – module definition\n- `main.go` – entry point\n- `handler/hello.go` – handler implementation\n- `handler/hello_test.go` – handler tests\n"
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	paths, ok := extractSpecLayoutPaths(dir)
	if !ok {
		t.Fatal("expected paths from SPEC")
	}
	// Should only have the 4 prefixed tree paths; bare prose paths dropped.
	want := map[string]bool{
		"helloapi/go.mod":              true,
		"helloapi/main.go":             true,
		"helloapi/handler/hello.go":    true,
		"helloapi/handler/hello_test.go": true,
	}
	if len(paths) != 4 {
		t.Fatalf("expected 4 paths, got %d: %v", len(paths), paths)
	}
	for _, p := range paths {
		if !want[p] {
			t.Fatalf("unexpected path %q in %v", p, paths)
		}
	}
	// Specifically: bare handler/hello.go must NOT appear.
	for _, p := range paths {
		if p == "handler/hello.go" || p == "handler/hello_test.go" {
			t.Fatalf("bare prose path %q leaked into authoritative set", p)
		}
	}
}

func TestMergeArchWinsOnConflict(t *testing.T) {
	specPaths := []string{
		"helloapi/go.mod",
		"helloapi/main.go",
		"helloapi/hello.go",        // WRONG flat (hallucinated by old parser)
		"helloapi/hello_test.go",   // WRONG flat
	}
	archPaths := []string{
		"helloapi/go.mod",
		"helloapi/main.go",
		"helloapi/handler/hello.go",     // CORRECT nested
		"helloapi/handler/hello_test.go", // CORRECT nested
	}
	got := mergeArchWinsOnConflict(specPaths, archPaths)
	// arch wins on hello.go / hello_test.go basename conflict
	want := map[string]bool{
		"helloapi/go.mod":                 true,
		"helloapi/main.go":                true,
		"helloapi/handler/hello.go":       true,
		"helloapi/handler/hello_test.go":  true,
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 merged paths, got %d: %v", len(got), got)
	}
	for _, g := range got {
		if !want[g] {
			t.Fatalf("unexpected merged path %q in %v", g, got)
		}
	}
	// Ensure flat hallucinated paths are gone
	for _, g := range got {
		if g == "helloapi/hello.go" || g == "helloapi/hello_test.go" {
			t.Fatalf("hallucinated flat path %q survived merge", g)
		}
	}
}

func TestValidateRigWorkflowProfileForQA_layoutDrift(t *testing.T) {
	townRoot := t.TempDir()
	rig := "drift_rig"

	// Profile has same basename at two different paths (layout drift)
	bad := WorkflowValidation{
		LayoutRoot:      "helloapi",
		BeadTitleContains: "Implement helloapi/",
		RequiredFiles:   []string{"helloapi/go.mod", "helloapi/main.go", "helloapi/hello.go", "helloapi/handler/hello.go", "helloapi/handler/hello_test.go"},
		QAVerifyCommand: "cd helloapi && go test ./...",
		DeliveryPhases: []DeliveryPhase{
			{
				ID:              "implementation",
				RequiredFiles:   []string{"helloapi/go.mod", "helloapi/main.go", "helloapi/hello.go", "helloapi/hello_test.go"},
				QAVerifyCommand: "cd helloapi && go build ./...",
			},
			{
				ID:              "verification",
				RequiredFiles:   []string{"helloapi/main.go", "helloapi/go.mod", "helloapi/handler/hello.go", "helloapi/handler/hello_test.go"},
				QAVerifyCommand: "cd helloapi && go test ./...",
			},
		},
	}
	defect := ValidateRigWorkflowProfileForQA(townRoot, rig, bad)
	if defect == "" {
		t.Fatal("expected layout drift defect for hello.go at helloapi/hello.go and helloapi/handler/hello.go")
	}
	if !strings.Contains(defect, "layout drift") {
		t.Fatalf("expected 'layout drift' in defect, got: %s", defect)
	}
	if !strings.Contains(defect, "hello.go") {
		t.Fatalf("expected 'hello.go' in defect, got: %s", defect)
	}

	// Clean profile (no drift) should pass
	good := WorkflowValidation{
		LayoutRoot:        "helloapi",
		BeadTitleContains: "Implement helloapi/",
		RequiredFiles:     []string{"helloapi/go.mod", "helloapi/main.go", "helloapi/handler/hello.go", "helloapi/handler/hello_test.go"},
		QAVerifyCommand:   "cd helloapi && go test ./...",
		DeliveryPhases: []DeliveryPhase{
			{
				ID:            "implementation",
				RequiredFiles: []string{"helloapi/go.mod", "helloapi/main.go", "helloapi/handler/hello.go"},
			},
			{
				ID:            "verification",
				RequiredFiles: []string{"helloapi/main.go", "helloapi/go.mod", "helloapi/handler/hello.go", "helloapi/handler/hello_test.go"},
			},
		},
	}
	if defect := ValidateRigWorkflowProfileForQA(townRoot, rig, good); defect != "" {
		t.Fatalf("clean profile should pass QA, got: %s", defect)
	}
}

func TestValidateRigWorkflowProfileForQA_emptyPhaseFiles(t *testing.T) {
	townRoot := t.TempDir()
	rig := "empty_rig"

	bad := WorkflowValidation{
		LayoutRoot:        "linkshelf",
		BeadTitleContains: "Implement linkshelf/",
		RequiredFiles:     []string{"linkshelf/go.mod", "linkshelf/main.go"},
		QAVerifyCommand:   "cd linkshelf && go test ./...",
		DeliveryPhases: []DeliveryPhase{
			{
				ID:              "go-module",
				RequiredFiles:   nil,
				QAVerifyCommand: "cd linkshelf && echo ok",
			},
			{
				ID:              "store-layer",
				RequiredFiles:   []string{},
				QAVerifyCommand: "cd linkshelf && echo ok",
			},
			{
				ID:              "api-handlers",
				RequiredFiles:   []string{"linkshelf/internal/api/handler.go"},
				QAVerifyCommand: "cd linkshelf && go test ./...",
			},
		},
	}
	defect := ValidateRigWorkflowProfileForQA(townRoot, rig, bad)
	if defect == "" {
		t.Fatal("expected defect for phases with nil/empty required_files")
	}
	if !strings.Contains(defect, "go-module") || !strings.Contains(defect, "store-layer") {
		t.Fatalf("expected phase names in defect, got: %s", defect)
	}
	if !strings.Contains(defect, "no required_files") {
		t.Fatalf("expected 'no required_files' in defect, got: %s", defect)
	}
}
