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

func TestParseSpecLayoutTree_fileLayoutSection(t *testing.T) {
	// Tree under "## File Layout" (not "## Layout") should still be found.
	spec := `# Link Shelf

## Layout

**layout_root: linkshelf** — all source files live under the ` + "`linkshelf`" + ` directory.

## File Layout

` + "```" + `
linkshelf/
├── go.mod
├── cmd/
│   └── server/
│       └── main.go
├── internal/
│   ├── store/
│   │   └── store.go
│   └── api/
│       └── handler.go
└── web/
    └── index.html
` + "```" + `
`
	got := parseSpecLayoutTree(spec)
	if len(got) != 5 {
		t.Fatalf("paths = %v (len=%d)", got, len(got))
	}
	want := map[string]bool{
		"linkshelf/go.mod":              true,
		"linkshelf/cmd/server/main.go":  true,
		"linkshelf/internal/store/store.go": true,
		"linkshelf/internal/api/handler.go": true,
		"linkshelf/web/index.html":      true,
	}
	for w := range want {
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

func TestParseSpecLayoutTree_deeplyNestedBars(t *testing.T) {
	// 3-level nesting with bars — verify depth counting is column-based.
	spec := `## Layout

` + "```" + `
root/
├── a/
│   └── b/
│       └── c.txt
` + "```" + `
`
	got := parseSpecLayoutTree(spec)
	if len(got) != 1 {
		t.Fatalf("paths = %v", got)
	}
	if got[0] != "root/a/b/c.txt" {
		t.Fatalf("expected root/a/b/c.txt, got %q", got[0])
	}
}


func TestUpdatePhaseRequiredFilesFromRequirements_inlineProsePaths(t *testing.T) {
	arch := `
## Requirements

### python-setup

The dependency manifest must contain fastapi and uvicorn. No route or test
implementation belongs in this requirement. requirements.txt resides in pingapp/.

### core

pingapp/main.py must expose app created by app = FastAPI() and register GET /ping.

### test

pingapp/test_main.py must import TestClient and assert status 200 and the exact
JSON mapping. It must not launch Uvicorn.

### integration-test

The runtime check must use the module-level app through the documented Uvicorn
command on port 8080. This phase creates no file.
`
	v := WorkflowValidation{
		LayoutRoot: "pingapp",
		DeliveryPhases: []DeliveryPhase{
			{ID: "python-setup", Title: "python-setup", RequiredFiles: []string{"pingapp/requirements.txt"}},
			// stale spec-index assignment that the sync must overwrite
			{ID: "core", Title: "core", RequiredFiles: []string{"pingapp/main.py", "pingapp/test_main.py"}},
			{ID: "test", Title: "test", RequiredFiles: nil},
			{ID: "integration-test", Title: "integration-test", RequiredFiles: []string{}},
		},
	}
	out, ok := updatePhaseRequiredFilesFromRequirementsSection(v, arch)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	got := map[string][]string{}
	for _, p := range out.DeliveryPhases {
		got[p.ID] = p.RequiredFiles
	}
	if len(got["core"]) != 1 || got["core"][0] != "pingapp/main.py" {
		t.Errorf("core files = %v, want [pingapp/main.py]", got["core"])
	}
	if len(got["test"]) != 1 || got["test"][0] != "pingapp/test_main.py" {
		t.Errorf("test files = %v, want [pingapp/test_main.py]", got["test"])
	}
	if len(got["python-setup"]) != 1 || got["python-setup"][0] != "pingapp/requirements.txt" {
		t.Errorf("python-setup files = %v, want [pingapp/requirements.txt]", got["python-setup"])
	}
	if len(got["integration-test"]) != 0 {
		t.Errorf("integration-test files = %v, want none", got["integration-test"])
	}
}
