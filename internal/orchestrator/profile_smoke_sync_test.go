package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSyncRigWorkflowProfileFromArchitecture_addsSmokePhase repairs a pytest-only
// spec-index profile (no runtime smoke CMD) by appending a smoke-test delivery phase
// derived from the SPEC/architecture smoke spec, so QA can satisfy the validator.
func TestSyncRigWorkflowProfileFromArchitecture_addsSmokePhase(t *testing.T) {
	townRoot := t.TempDir()
	rig := "ping_rig"
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	profileDir := filepath.Join(rigDir, ".gastown")
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		t.Fatal(err)
	}
	spec := `# PingApp

## Goal

A tiny Python app with a single HTTP endpoint.

## Layout

` + "```" + `
pingapp/
  requirements.txt
  main.py
  test_main.py
` + "```" + `

` + "`" + `cd pingapp && uvicorn main:app --port 8080` + "`" + ` serves /ping.
`
	arch := `# Architecture

## HTTP route table

| Method | Path  | Description     | Expected response            |
|--------|-------|-----------------|------------------------------|
| GET    | /ping | Health check    | 200 {"message":"pong"}      |

## Server entrypoint

uvicorn main:app --port 8080

For manual verification, run the server in the background and curl the endpoint:
cd pingapp && uvicorn main:app --port 8080 &
curl http://localhost:8080/ping
`
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	// Pytest-only profile: single backend-core phase, no runtime smoke CMD anywhere.
	profile := rigProfileEnvelope{
		Version: 1,
		Validation: WorkflowValidation{
			LayoutRoot:         "pingapp",
			BeadTitleContains:  "Implement pingapp/",
			RequiredFiles:      []string{"pingapp/requirements.txt", "pingapp/main.py", "pingapp/test_main.py"},
			QAVerifyCommand:    "cd pingapp && pytest",
			ActivePhaseIDField: "backend-core",
			DevServerPort:       8080,
			DeliveryPhases: []DeliveryPhase{
				{
					ID:              "backend-core",
					Title:           "Backend source and tests",
					RequiredFiles:   []string{"pingapp/main.py", "pingapp/requirements.txt", "pingapp/test_main.py"},
					QAVerifyCommand: "cd pingapp && python -m pytest -v",
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
	var smokeCmd string
	var smokePhase *DeliveryPhase
	for i := range saved.DeliveryPhases {
		if hasRuntimeSmokeCommand(saved.DeliveryPhases[i].QAVerifyCommand) {
			smokeCmd = saved.DeliveryPhases[i].QAVerifyCommand
			smokePhase = &saved.DeliveryPhases[i]
			break
		}
	}
	if smokeCmd == "" {
		t.Fatalf("sync did not add a runtime smoke phase: phases=%+v", saved.DeliveryPhases)
	}
	if !strings.Contains(smokeCmd, "uvicorn") {
		t.Fatalf("smoke cmd missing server start: %q", smokeCmd)
	}
	if !strings.Contains(smokeCmd, "curl ") {
		t.Fatalf("smoke cmd missing curl: %q", smokeCmd)
	}
	if !strings.Contains(smokeCmd, "127.0.0.1") && !strings.Contains(smokeCmd, "localhost") {
		t.Fatalf("smoke cmd missing loopback host: %q", smokeCmd)
	}
	if smokePhase == nil || len(smokePhase.RequiredFiles) == 0 {
		t.Fatalf("smoke phase has no required_files: %+v", smokePhase)
	}
	// The smoke phase must be last (final phase) so QA runs it last.
	last := saved.DeliveryPhases[len(saved.DeliveryPhases)-1]
	if !hasRuntimeSmokeCommand(last.QAVerifyCommand) {
		t.Fatalf("smoke phase not last: %+v", saved.DeliveryPhases)
	}
}

// TestSyncRigWorkflowProfileFromArchitecture_keepsExistingSmoke confirms the sync
// does not duplicate the smoke phase when the profile already has a runtime smoke CMD.
func TestSyncRigWorkflowProfileFromArchitecture_keepsExistingSmoke(t *testing.T) {
	townRoot := t.TempDir()
	rig := "ping_rig"
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	profileDir := filepath.Join(rigDir, ".gastown")
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		t.Fatal(err)
	}
	spec := `# PingApp

## Layout

` + "```" + `
pingapp/
  requirements.txt
  main.py
  test_main.py
` + "```" + `
`
	arch := `# Architecture

## HTTP route table

| Method | Path  | Description  | Expected response           |
|--------|-------|--------------|-----------------------------|
| GET    | /ping | Health check | 200 {"message":"pong"}     |

uvicorn main:app --port 8080
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
			LayoutRoot:         "pingapp",
			RequiredFiles:      []string{"pingapp/requirements.txt", "pingapp/main.py", "pingapp/test_main.py"},
			QAVerifyCommand:    "cd pingapp && pytest",
			ActivePhaseIDField: "smoke-test",
			DevServerPort:      8080,
			DeliveryPhases: []DeliveryPhase{
				{
					ID:              "backend-core",
					Title:           "Backend source and tests",
					RequiredFiles:   []string{"pingapp/requirements.txt", "pingapp/main.py", "pingapp/test_main.py"},
					QAVerifyCommand: "cd pingapp && pytest",
				},
				{
					ID:              "smoke-test",
					Title:           "Smoke Test with Running Server",
					RequiredFiles:   []string{"pingapp/main.py"},
					QAVerifyCommand: "cd pingapp && python -m uvicorn main:app --port 8080 & sleep 2 && curl -sf http://localhost:8080/ping && kill $(lsof -ti:8080)",
					DependsOn:       []string{"backend-core"},
				},
			},
		},
	}
	data, _ := marshalRigProfileJSON(profile)
	if err := os.WriteFile(filepath.Join(profileDir, "workflow-profile.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := SyncRigWorkflowProfileFromArchitecture(townRoot, rig); err != nil {
		t.Fatal(err)
	}
	saved, _, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil {
		t.Fatal(err)
	}
	smokeCount := 0
	for _, p := range saved.DeliveryPhases {
		if hasRuntimeSmokeCommand(p.QAVerifyCommand) {
			smokeCount++
		}
	}
	if smokeCount != 1 {
		t.Fatalf("expected exactly 1 smoke phase, got %d: %+v", smokeCount, saved.DeliveryPhases)
	}
	if len(saved.DeliveryPhases) != 2 {
		t.Fatalf("expected 2 phases, got %d: %+v", len(saved.DeliveryPhases), saved.DeliveryPhases)
	}
}

// TestEnsureRuntimeSmokePhaseFromSpec_noRuntimeSmoke confirms a library-only Python
// profile (no server entry) is left unchanged — no invented smoke phase.
func TestEnsureRuntimeSmokePhaseFromSpec_noRuntimeSmoke(t *testing.T) {
	townRoot := t.TempDir()
	rig := "lib_rig"
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	profileDir := filepath.Join(rigDir, ".gastown")
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		t.Fatal(err)
	}
	spec := "# LibApp\n\n## Layout\n\n" + "```" + "\nlibapp/\n  lib.py\n  test_lib.py\n" + "```" + "\n"
	arch := "# Architecture\n\nA pure library with no HTTP server.\n"
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(spec), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	profile := rigProfileEnvelope{
		Version: 1,
		Validation: WorkflowValidation{
			LayoutRoot:         "libapp",
			RequiredFiles:      []string{"libapp/lib.py", "libapp/test_lib.py"},
			QAVerifyCommand:    "cd libapp && pytest",
			ActivePhaseIDField: "backend-core",
			DeliveryPhases: []DeliveryPhase{
				{
					ID:              "backend-core",
					Title:           "Backend",
					RequiredFiles:   []string{"libapp/lib.py", "libapp/test_lib.py"},
					QAVerifyCommand: "cd libapp && pytest",
				},
			},
		},
	}
	data, _ := marshalRigProfileJSON(profile)
	if err := os.WriteFile(filepath.Join(profileDir, "workflow-profile.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := SyncRigWorkflowProfileFromArchitecture(townRoot, rig); err != nil {
		t.Fatal(err)
	}
	saved, _, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range saved.DeliveryPhases {
		if hasRuntimeSmokeCommand(p.QAVerifyCommand) {
			t.Fatalf("library profile got a smoke phase: %+v", saved.DeliveryPhases)
		}
	}
	if len(saved.DeliveryPhases) != 1 {
		t.Fatalf("expected 1 phase, got %d: %+v", len(saved.DeliveryPhases), saved.DeliveryPhases)
	}
}
