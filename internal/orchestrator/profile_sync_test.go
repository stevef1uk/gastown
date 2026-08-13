package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSyncRigWorkflowProfileFromArchitecture_pingApp confirms the sync replaces the
// hallucinated spec-index profile (config.py, pyproject.toml, __init__.py, route
// stub @app.get/post/...) with the SPEC layout, so planning only creates real beads.
func TestSyncRigWorkflowProfileFromArchitecture_pingApp(t *testing.T) {
	townRoot := t.TempDir()
	rig := "ping_rig"
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	profileDir := filepath.Join(rigDir, ".gastown")
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		t.Fatal(err)
	}
	spec := `# PingApp

## Layout (implement beads only)

` + "```" + `
pingapp/
├── requirements.txt
├── main.py
└── test_main.py
` + "```" + `

No extra files or abstractions.
`
	arch := "# Architecture\n\n- `pingapp/requirements.txt`\n- `pingapp/main.py`\n- `pingapp/test_main.py`\n"
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	// Hallucinated spec-index profile.
	profile := rigProfileEnvelope{
		Version: 1,
		Validation: WorkflowValidation{
			LayoutRoot:      "pingapp",
			BeadTitleContains: "Implement pingapp/",
			RequiredFiles:   []string{"pingapp/requirements.txt", "pingapp/main.py", "pingapp/pyproject.toml", "pingapp/__init__.py", "pingapp/config.py", "@app.get/post/..."},
			QAVerifyCommand: "cd pingapp && pytest test_main.py",
			DeliveryPhases: []DeliveryPhase{
				{
					ID:              "implementation",
					RequiredFiles:   []string{"pingapp/requirements.txt", "pingapp/main.py", "pingapp/pyproject.toml", "pingapp/__init__.py", "pingapp/config.py"},
					QAVerifyCommand: "cd pingapp && pytest test_main.py -v",
				},
				{
					ID:              "smoke-test",
					RequiredFiles:   []string{"pingapp/main.py", "@app.get/post/..."},
					QAVerifyCommand: "cd pingapp && python -m uvicorn main:app --port 8080 & sleep 3 && curl -sf http://localhost:8080/ping; exit 0",
				},
			},
		},
	}
	data, _ := marshalRigProfileJSON(profile)
	if err := os.WriteFile(filepath.Join(profileDir, "workflow-profile.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	rewritten, err := SyncRigWorkflowProfileFromArchitecture(townRoot, rig)
	if err != nil {
		t.Fatal(err)
	}
	if !rewritten {
		t.Fatal("expected profile rewrite")
	}
	saved, _, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, f := range saved.RequiredFiles {
		got[f] = true
	}
	for _, want := range []string{"pingapp/requirements.txt", "pingapp/main.py", "pingapp/test_main.py"} {
		if !got[want] {
			t.Fatalf("missing %q in required_files: %v", want, saved.RequiredFiles)
		}
	}
	for _, bad := range []string{"pingapp/config.py", "pingapp/pyproject.toml", "pingapp/__init__.py", "@app.get/post/..."} {
		if got[bad] {
			t.Fatalf("hallucinated %q survived sync: %v", bad, saved.RequiredFiles)
		}
	}
	// Active phase required_files (what planning sees) must also be authoritative.
	active := saved.ForActivePhase()
	for _, bad := range []string{"pingapp/config.py", "pingapp/pyproject.toml", "pingapp/__init__.py", "@app.get/post/..."} {
		for _, f := range active.RequiredFiles {
			if f == bad {
				t.Fatalf("hallucinated %q survived in active phase: %v", bad, active.RequiredFiles)
			}
		}
	}
	foundTest := false
	for _, f := range active.RequiredFiles {
		if strings.HasSuffix(f, "test_main.py") {
			foundTest = true
		}
	}
	if !foundTest {
		t.Fatalf("test_main.py missing from active phase required_files: %v", active.RequiredFiles)
	}
}

// TestExtractArchPaths_plainBulletsWithoutBackticks confirms profile sync survives an
// architecture.md whose Planned file layout uses plain "- finally/... — desc" bullets
// instead of backtick-wrapped paths (the format architects actually emit). Regression
// for fin: the sync silently shrank the profile to only the SPEC-tree files because
// extractArchPaths only matched backticks.
func TestExtractArchPaths_plainBulletsWithoutBackticks(t *testing.T) {
	arch := `# Architecture for fin

## Planned file layout

- finally/.env — environment values.
- finally/.gitignore — ignores secrets.
- finally/backend/app/main.py — app factory and Uvicorn entrypoint.
- finally/backend/app/db/schema.py — SQLite DDL and seeds.
- finally/backend/tests/test_api.py — route status codes.
- finally/frontend/app/page.tsx — dashboard composition.
- finally/test/e2e.spec.ts — Playwright scenarios.
- finally/Dockerfile — Node build and Python runtime stages.

## HTTP + entrypoint integration
The backend listens on port 8000 via uvicorn. No other files exist.
`
	got := extractArchPaths(arch, "finally")
	want := map[string]bool{
		"finally/.env":                    true,
		"finally/.gitignore":              true,
		"finally/backend/app/main.py":     true,
		"finally/backend/app/db/schema.py": true,
		"finally/backend/tests/test_api.py": true,
		"finally/frontend/app/page.tsx":   true,
		"finally/test/e2e.spec.ts":        true,
		"finally/Dockerfile":              true,
	}
	for _, p := range got {
		if want[p] {
			delete(want, p)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing plain-bullet paths %v; extracted %v", want, got)
	}
	if !containsString(got, "finally/Dockerfile") {
		t.Fatalf("Dockerfile (no extension) must be extracted: %v", got)
	}
}

// TestSyncRigWorkflowProfileFromArchitecture_plainBullets confirms the full sync uses
// architecture.md's plain-bullet file list (not the SPEC directory tree) once the
// design is approved — the exact fin regression where the profile was truncated to
// only the SPEC-tree leaf files.
func TestSyncRigWorkflowProfileFromArchitecture_plainBullets(t *testing.T) {
	townRoot := t.TempDir()
	rig := "fin_rig"
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	profileDir := filepath.Join(rigDir, ".gastown")
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		t.Fatal(err)
	}
	// SPEC directory tree lists only directories and a handful of top-level leaf files.
	spec := `# FinAlly

## 4. Directory Structure

` + "```" + `
finally/
├── frontend/                 # Next.js TypeScript project
├── backend/                  # FastAPI uv project
│   └── db/                   # Schema, seed, migrations
├── scripts/
│   ├── start_mac.sh
│   └── stop_mac.sh
├── db/
│   └── .gitkeep
├── Dockerfile
├── docker-compose.yml
├── .env
└── .gitignore
` + "```" + `
`
	// Approved architecture lists every implementation file as plain bullets.
	arch := `# Architecture for fin

## Planned file layout

- finally/.env — environment values.
- finally/.gitignore — ignores secrets.
- finally/backend/app/__init__.py — backend package marker.
- finally/backend/app/main.py — app factory and Uvicorn entrypoint.
- finally/backend/app/db/schema.py — SQLite DDL and seeds.
- finally/backend/app/db/init_db.py — idempotent initialization.
- finally/backend/app/store/store.py — repository and orders.
- finally/backend/app/market/service.py — provider selection and polling.
- finally/backend/app/api/routes.py — exact SPEC REST handlers.
- finally/backend/tests/test_api.py — route status codes.
- finally/frontend/package.json — Next.js dependencies.
- finally/frontend/app/page.tsx — dashboard composition.
- finally/test/e2e.spec.ts — Playwright scenarios.
- finally/Dockerfile — Node build and Python runtime stages.
- finally/docker-compose.yml — production wrapper.

## HTTP + entrypoint integration
Uvicorn on port 8000. No other files exist.
`
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	profile := rigProfileEnvelope{
		Version: 1,
		Validation: WorkflowValidation{
			LayoutRoot:      "finally",
			BeadTitleContains: "Implement finally/",
			RequiredFiles:   []string{"finally/.env", "finally/.gitignore", "finally/db/.gitkeep", "finally/Dockerfile", "finally/docker-compose.yml"},
			QAVerifyCommand: "cd finally && pytest",
		},
	}
	data, _ := marshalRigProfileJSON(profile)
	if err := os.WriteFile(filepath.Join(profileDir, "workflow-profile.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	rewritten, err := SyncRigWorkflowProfileFromArchitecture(townRoot, rig)
	if err != nil {
		t.Fatal(err)
	}
	if !rewritten {
		t.Fatal("expected profile rewrite")
	}
	saved, _, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"finally/.env",
		"finally/.gitignore",
		"finally/backend/app/main.py",
		"finally/backend/app/db/schema.py",
		"finally/backend/app/api/routes.py",
		"finally/backend/tests/test_api.py",
		"finally/frontend/app/page.tsx",
		"finally/test/e2e.spec.ts",
		"finally/Dockerfile",
	} {
		found := false
		for _, f := range saved.RequiredFiles {
			if f == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing %q in required_files after sync: %v", want, saved.RequiredFiles)
		}
	}
	// The profile must not shrink back to the SPEC-tree-only leaf files.
	if len(saved.RequiredFiles) < 10 {
		t.Fatalf("profile truncated to %d files; expected the full architecture list:\n%v", len(saved.RequiredFiles), saved.RequiredFiles)
	}
}

// TestValidateRigWorkflowProfileForQA_catchesHallucinations confirms the QA validator
// flags wildcards, route stubs, and verify-command file references missing from
// required_files — the exact defects that deadlocked ping_rig.
func TestValidateRigWorkflowProfileForQA_catchesHallucinations(t *testing.T) {
	townRoot := t.TempDir()
	rig := "ping_rig"
	// Profile with hallucinated entries.
	bad := WorkflowValidation{
		LayoutRoot:      "pingapp",
		BeadTitleContains: "Implement pingapp/",
		RequiredFiles:   []string{"pingapp/main.py", "pingapp/config.py", "@app.get/post/..."},
		QAVerifyCommand: "cd pingapp && pytest test_main.py",
	}
	if defect := ValidateRigWorkflowProfileForQA(townRoot, rig, bad); defect == "" {
		t.Fatal("expected QA to flag route stub and missing verify-command file")
	}

	// Clean profile passes.
	good := WorkflowValidation{
		LayoutRoot:      "pingapp",
		BeadTitleContains: "Implement pingapp/",
		RequiredFiles:   []string{"pingapp/main.py", "pingapp/test_main.py"},
		QAVerifyCommand: "cd pingapp && pytest test_main.py",
	}
	if defect := ValidateRigWorkflowProfileForQA(townRoot, rig, good); defect != "" {
		t.Fatalf("clean profile should pass QA, got: %s", defect)
	}
}

// TestFilterValidImplementPaths confirms route stubs and wildcards are dropped.
func TestFilterValidImplementPaths(t *testing.T) {
	in := []string{"pingapp/main.py", "pingapp/config.py", "@app.get/post/...", "test_*.py", "http://x/y", "{placeholder}", "pingapp/test_main.py"}
	got := filterValidImplementPaths(in)
	want := map[string]bool{"pingapp/main.py": true, "pingapp/config.py": true, "pingapp/test_main.py": true}
	if len(got) != 3 {
		t.Fatalf("filtered = %v, want 3 concrete paths", got)
	}
	for _, g := range got {
		if !want[g] {
			t.Fatalf("unexpected path %q in filtered set %v", g, got)
		}
	}
}

// TestSyncRigWorkflowProfileFromArchitecture_keepsSpecPhases is a regression test for the
// architect LLM writing its own backtick-laden "## Delivery phases" in architecture.md:
// the sync must keep the SPEC's canonical phase IDs/titles (go-module/core/web/integration-test)
// instead of replacing them with slugified, truncated copies of the architect's prose.
func TestSyncRigWorkflowProfileFromArchitecture_keepsSpecPhases(t *testing.T) {
	townRoot := t.TempDir()
	rig := "pwrig"
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	profileDir := filepath.Join(rigDir, ".gastown")
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		t.Fatal(err)
	}
	spec := `# PingApp

API server on 8000, no extra files.
`
	// Architect drifts: writes its own numbered "## Delivery phases" with backtick titles.
	arch := `# Architecture for PingApp

## Planned file layout

- ` + "`pingapp/go.mod`" + `
- ` + "`pingapp/cmd/server/main.go`" + `
- ` + "`pingapp/web/index.html`" + `
- ` + "`pingapp/e2e/ping.spec.ts`" + `
- ` + "`pingapp/playwright.config.ts`" + `

## Delivery phases

1. Create ` + "`pingapp/go.mod`" + ` and ` + "`pingapp/cmd/server/main.go`" + `; implement the exact /ping
   contract, method handling, PORT behavior, and safe static serving.
2. Create ` + "`pingapp/web/index.html`" + ` with the Hello button; wire the click to POST /ping.
3. Create ` + "`pingapp/e2e/ping.spec.ts`" + `, ` + "`pingapp/playwright.config.ts`" + `, and ` + "`pingapp/package.json`" + `; run the suite.
`
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	// Sensible SPEC-derived phases from spec-index, with files already distributed.
	profile := rigProfileEnvelope{
		Version: 1,
		Validation: WorkflowValidation{
			LayoutRoot:       "pingapp",
			BeadTitleContains: "Implement pingapp/",
			RequiredFiles: []string{
				"pingapp/go.mod",
				"pingapp/cmd/server/main.go",
				"pingapp/web/index.html",
				"pingapp/e2e/ping.spec.ts",
				"pingapp/playwright.config.ts",
			},
			QAVerifyCommand: "cd pingapp && go test ./...",
			DeliveryPhases: []DeliveryPhase{
				{ID: "go-module", Title: "Go module", RequiredFiles: []string{"pingapp/go.mod"}},
				{ID: "core", Title: "Core server", RequiredFiles: []string{"pingapp/cmd/server/main.go"}},
				{ID: "web", Title: "Web UI", RequiredFiles: []string{"pingapp/web/index.html"}},
				{ID: "integration-test", Title: "Integration tests", RequiredFiles: []string{"pingapp/e2e/ping.spec.ts", "pingapp/playwright.config.ts"}},
			},
		},
	}
	data, _ := marshalRigProfileJSON(profile)
	if err := os.WriteFile(filepath.Join(profileDir, "workflow-profile.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	rewritten, err := SyncRigWorkflowProfileFromArchitecture(townRoot, rig)
	if err != nil {
		t.Fatal(err)
	}
	if !rewritten {
		t.Fatal("expected profile rewrite")
	}
	saved, _, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, p := range saved.DeliveryPhases {
		ids = append(ids, p.ID)
	}
	for _, want := range []string{"go-module", "core", "web", "integration-test"} {
		found := false
		for _, id := range ids {
			if id == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("SPEC phase %q lost after sync; got phases %v", want, ids)
		}
	}
	for _, bad := range []string{"create-pingapp-go-mod-and-pingapp-cmd-server-main-go-implement-the-exact-ping", "create-pingapp-e2e-ping-spec-ts"} {
		for _, id := range ids {
			if strings.HasPrefix(id, bad) {
				t.Fatalf("architect-mangled phase %q replaced SPEC phases: %v", bad, ids)
			}
		}
	}
	// Active phase must resolve to a real SPEC phase, never raw backticks.
	active := saved.ActivePhaseID()
	if !phaseIDExists(saved, active) {
		t.Fatalf("active_phase_id %q does not resolve to a phase", active)
	}
	if strings.Contains(active, "`") {
		t.Fatalf("active_phase_id %q contains backticks", active)
	}
}

// TestRebuildDeliveryPhasesFromAuthoritative_keepsPhaseStructure confirms phase
// required_files are filtered to the authoritative set.
func TestRebuildDeliveryPhasesFromAuthoritative_keepsPhaseStructure(t *testing.T) {
	v := WorkflowValidation{
		DeliveryPhases: []DeliveryPhase{
			{
				ID:            "implementation",
				RequiredFiles: []string{"pingapp/main.py", "pingapp/config.py"},
			},
			{
				ID:            "smoke-test",
				RequiredFiles: []string{"pingapp/main.py", "@app.get/post/..."},
			},
		},
	}
	auth := []string{"pingapp/requirements.txt", "pingapp/main.py", "pingapp/test_main.py"}
	v = rebuildDeliveryPhasesFromAuthoritative(v, auth)
	if len(v.DeliveryPhases) != 2 {
		t.Fatalf("expected 2 phases, got %d", len(v.DeliveryPhases))
	}
	for _, f := range v.DeliveryPhases[0].RequiredFiles {
		if f == "pingapp/config.py" {
			t.Fatalf("config.py survived in phase: %v", v.DeliveryPhases[0].RequiredFiles)
		}
	}
	// All authoritative files must be placed somewhere.
	union := v.UnionRequiredFiles()
	for _, want := range auth {
		found := false
		for _, u := range union {
			if u == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("authoritative file %q not placed in any phase: %v", want, union)
		}
	}
}
