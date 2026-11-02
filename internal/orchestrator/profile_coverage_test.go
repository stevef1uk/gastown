package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSyncArchitecturePreservesFilesOutsidePhaseRequirements(t *testing.T) {
	town := t.TempDir()
	rigDir := filepath.Join(town, "demo", "mayor", "rig")
	meta := filepath.Join(rigDir, ".gastown")
	if err := os.MkdirAll(meta, 0755); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot: "workstation", TestRunner: "pytest", ActivePhaseIDField: "release",
		RequiredFiles: []string{"workstation/backend/market.py", "workstation/frontend/main.js", "workstation/backend/tests/test_api.py", "workstation/db/workstation.db", "workstation/dashboard.spec.ts"},
		DeliveryPhases: []DeliveryPhase{
			{ID: "core", Title: "Backend core", RequiredFiles: []string{"workstation/backend/market.py", "workstation/db/workstation.db"}},
			{ID: "ui", Title: "Frontend UI", RequiredFiles: []string{"workstation/frontend/main.js"}},
			{ID: "release", Title: "Release tests", RequiredFiles: []string{"workstation/backend/tests/test_api.py"}},
		},
	}
	raw, err := marshalRigProfileJSON(rigProfileEnvelope{Validation: v})
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		".gastown/workflow-profile.json": string(raw),
		"SPEC.md":                        "# Workstation\n\n## Layout\n\nworkstation/\n├── backend/\n│   └── market.py\n└── frontend/\n    └── main.js\n\n## Contract\n`backend/app/main.py` must expose the application factory.\n",
		"architecture.md": "# Architecture\n\n## Planned file layout\n" +
			"- `workstation/backend/market.py`\n- `workstation/backend/app/main.py`\n- `workstation/backend/app/api/schemas.py`\n- `workstation/frontend/main.js`\n- `workstation/backend/tests/test_api.py`\n\n" +
			"## Requirements\n\n### core\nImplement `workstation/backend/market.py`.\n\n" +
			"### ui\nImplement `workstation/frontend/main.js`.\n\n" +
			"### release\nImplement `workstation/backend/tests/test_api.py`.\n\n" +
			"## Runtime\nThe runtime database is `workstation/db/workstation.db`; it must not be committed.\n",
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(rigDir, name), []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if changed, err := SyncRigWorkflowProfileFromArchitecture(town, "demo"); err != nil || !changed {
		t.Fatalf("sync changed=%v err=%v", changed, err)
	}
	got, ok, err := LoadRigWorkflowProfileFile(town, "demo")
	if err != nil || !ok {
		t.Fatalf("load profile: ok=%v err=%v", ok, err)
	}
	for _, want := range []string{"workstation/backend/app/main.py", "workstation/backend/app/api/schemas.py"} {
		if !containsString(got.RequiredFiles, want) {
			t.Errorf("top-level required_files lost %s: %v", want, got.RequiredFiles)
		}
		owned := false
		for _, phase := range got.DeliveryPhases {
			owned = owned || containsString(phase.RequiredFiles, want)
		}
		if !owned {
			t.Errorf("no phase owns %s: %+v", want, got.DeliveryPhases)
		}
	}
	for _, unwanted := range []string{"workstation/db/workstation.db", "workstation/dashboard.spec.ts"} {
		if containsString(got.UnionRequiredFiles(), unwanted) {
			t.Errorf("profile retained non-deliverable %s", unwanted)
		}
	}
}
