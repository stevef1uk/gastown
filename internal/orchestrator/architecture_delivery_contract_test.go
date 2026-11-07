package orchestrator

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestArchitectureDeliveryContractRoundTrip(t *testing.T) {
	town, rig := t.TempDir(), "sample"
	dir := filepath.Join(town, rig, "mayor", "rig")
	if err := os.MkdirAll(filepath.Join(dir, rigProfileDir), 0755); err != nil {
		t.Fatal(err)
	}
	spec := "# App\n## Layout\n```\ndemo/\n├── server/\n│   └── app.py\n└── client/\n    ├── package.json\n    ├── vite.config.ts\n    └── main.tsx\n```\n# Phase 1 — Foundation\n# Phase 2 — Polish\n"
	arch := "# Architecture\n## Planned file layout\n- `demo/server/app.py`\n- `demo/client/package.json`\n- `demo/client/vite.config.ts`\n- `demo/client/main.tsx`\n- `demo/Dockerfile`\n## Delivery phases\n```yaml\ndelivery_phases:\n  - id: foundation\n    title: Foundation\n    required_files: [demo/server/app.py, demo/client/package.json, demo/client/vite.config.ts, demo/client/main.tsx, demo/Dockerfile]\n    qa_verify_command: \"cd demo/server && python -c 'import app' && cd ../client && npm run build\"\n  - id: polish\n    title: Polish\n    required_files: [demo/client/main.tsx]\n    qa_verify_command: cd demo/client && npm run build\n    depends_on: [foundation]\n```\n## Requirements\n### foundation\nRunnable service and compiled client.\n### polish\nRefined presentation; build the client again.\n"
	for name, content := range map[string]string{"SPEC.md": spec, "architecture.md": arch} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	old := rigProfileEnvelope{Version: 1, TestPlanReviewed: true, TestPlanFrozen: true, Validation: WorkflowValidation{LayoutRoot: "demo", DeliveryPhases: []DeliveryPhase{{ID: "foundation", RequiredFiles: []string{"demo/server/app.py"}, QAVerifyCommand: "python -c 'import sys'"}}, CompletedPhaseIDsField: []string{"foundation"}}}
	raw, _ := marshalRigProfileJSON(old)
	if err := os.WriteFile(filepath.Join(dir, rigProfileDir, rigProfileFile), raw, 0644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateArchitectureStructure(arch); err != nil {
		t.Fatal(err)
	}
	if err := ValidateArchitectureDeliveryContract(town, rig, old.Validation); err != nil {
		t.Fatal(err)
	}
	if changed, err := SyncRigWorkflowProfileFromArchitecture(town, rig); err != nil || !changed {
		t.Fatalf("sync: changed=%v err=%v", changed, err)
	}
	want, _, _ := architectureDeliveryContract(arch)
	for i := 0; i < 2; i++ {
		got, ok, err := LoadRigWorkflowProfileFile(town, rig)
		if err != nil || !ok {
			t.Fatalf("load: %v", err)
		}
		if !got.ExplicitDeliveryContract || !reflect.DeepEqual(got.DeliveryPhases, want) {
			t.Fatalf("load/save rewrote explicit ownership/commands: %+v", got.DeliveryPhases)
		}
		effective := ClampProfileValidation(mergeValidationFields(DefaultWorkflowValidation(), got))
		if !effective.ExplicitDeliveryContract || !reflect.DeepEqual(effective.DeliveryPhases, want) {
			t.Fatal("task validation merge lost explicit delivery contract")
		}
		if len(got.CompletedPhaseIDsField) != 0 || IsTestPlanReviewed(town, rig) || IsTestPlanFrozen(town, rig) {
			t.Fatal("changed verification retained stale completion/test-plan approval")
		}
		if err := SaveRigWorkflowProfileEnvelope(town, rig, rigProfileEnvelope{Version: 1, Validation: got}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestArchitectureDeliveryContractNormalizesShellVariables(t *testing.T) {
	doc := "## Delivery phases\n```yaml\ndelivery_phases:\n  - id: core\n    required_files: [app/main.go]\n    qa_verify_command: 'for i in $$(seq 1 2); do test " + "$$body" + " = ok; done'\n```\n"
	phases, ok, err := architectureDeliveryContract(doc)
	if err != nil || !ok {
		t.Fatalf("parse: ok=%v err=%v", ok, err)
	}
	if phases[0].QAVerifyCommand != "for i in $(seq 1 2); do test $body = ok; done" {
		t.Fatalf("command = %q", phases[0].QAVerifyCommand)
	}
}

func TestArchitectureStructureRejectsAmbiguousAmendments(t *testing.T) {
	for _, doc := range []string{
		"# Design\n## Delivery phases\nFirst table.\n## Delivery phases\nSecond table.\n",
		"# Design\n## Planned file layout\n- `x/main.py`\n## Planned file layout\n- `x/other.py`\n",
		"# Design\n## Reconciliation\n## Requirements\nActual text.\n",
		"# Design\n## Requirements\nSome text.\n## Requirements\nDifferent text.\n",
	} {
		if err := ValidateArchitectureStructure(doc); err == nil {
			t.Fatalf("accepted ambiguous architecture: %s", doc)
		}
	}
	_, _, err := architectureDeliveryContract("## Delivery phases\n```yaml\ndelivery_phases:\n  - id: core\n    required_files: [x/main.py]\n    qa_verify_commmand: echo ok\n```\n")
	if err == nil || !strings.Contains(err.Error(), "qa_verify_commmand") {
		t.Fatalf("misspelled command must fail, not fall back to inference: %v", err)
	}
}

func TestUnfencedDeliveryContractBlocksDesignAndSync(t *testing.T) {
	town, rig := t.TempDir(), "sample"
	dir := filepath.Join(town, rig, "mayor", "rig")
	if err := os.MkdirAll(filepath.Join(dir, rigProfileDir), 0755); err != nil {
		t.Fatal(err)
	}
	doc := "# Architecture\n## Delivery phases\ndelivery_phases:\n  - id: core\n    required_files: [demo/main.go]\n    qa_verify_command: cd demo && go build ./...\n## Requirements\n### core\nBuild the application.\n"
	if err := os.WriteFile(filepath.Join(dir, "architecture.md"), []byte(doc), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{LayoutRoot: "demo", RequiredFiles: []string{"demo/main.go"}}
	raw, err := marshalRigProfileJSON(rigProfileEnvelope{Version: 1, Validation: v})
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(dir, rigProfileDir, rigProfileFile)
	if err := os.WriteFile(profile, raw, 0644); err != nil {
		t.Fatal(err)
	}
	checks := map[string]func() error{
		"structure": func() error { return ValidateArchitectureStructure(doc) },
		"delivery":  func() error { return ValidateArchitectureDeliveryContract(town, rig, v) },
		"requirements": func() error {
			_, err := ArchitectureRequirementPhaseIDs(doc, v)
			return err
		},
		"sync": func() error {
			changed, err := SyncRigWorkflowProfileFromArchitecture(town, rig)
			if changed {
				t.Fatal("invalid delivery contract changed the profile")
			}
			return err
		},
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			if err := check(); err == nil || !strings.Contains(err.Error(), "fenced") || !strings.Contains(err.Error(), "delivery_phases") {
				t.Fatalf("expected actionable delivery contract formatting error, got %v", err)
			}
		})
	}
	got, err := os.ReadFile(profile)
	if err != nil || string(got) != string(raw) {
		t.Fatalf("rejected contract must preserve the existing profile: %v", err)
	}
}

func TestProseDeliveryPhasesRemainSupported(t *testing.T) {
	doc := "# Architecture\n## Delivery phases\n1. Core — build the application.\n## Requirements\n### core\nBuild the application.\n"
	if err := ValidateArchitectureStructure(doc); err != nil {
		t.Fatal(err)
	}
	if _, explicit, err := architectureDeliveryContract(doc); explicit || err != nil {
		t.Fatalf("prose phases should retain inferred delivery behavior: explicit=%v err=%v", explicit, err)
	}
}

func TestMissingDeclaredPhasesAcceptsSplitProvenance(t *testing.T) {
	declared := []DeliveryPhase{{ID: "streaming", Title: "Streaming"}}
	actual := []DeliveryPhase{{ID: "streaming-1", SpecFocus: "Streaming\nDetails"}, {ID: "streaming-2", SpecFocus: "Streaming"}}
	if missing := missingDeclaredPhases(declared, actual); len(missing) != 0 {
		t.Fatalf("split SPEC boundary lost: %v", missing)
	}
	if missing := missingDeclaredPhases(declared, []DeliveryPhase{{ID: "unrelated", SpecFocus: "Streaming"}}); len(missing) == 0 {
		t.Fatal("unrelated phase must not claim arbitrary capability through prose")
	}
}
