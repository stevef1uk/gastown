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
