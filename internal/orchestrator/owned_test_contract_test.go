package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ownedTestFixture(t *testing.T) (string, WorkflowValidation) {
	t.Helper()
	root := t.TempDir()
	v := WorkflowValidation{
		LayoutRoot: "product", TestRunner: "pytest", BeadTitleContains: "Implement product/",
		ActivePhaseIDField: "foundation", MinImplementationFileBytes: 1, MinSubstantiveLines: 1,
		DeliveryPhases: []DeliveryPhase{
			{ID: "foundation", RequiredFiles: []string{"product/backend/main.py", "product/style.css"}, QAVerifyCommand: "docker compose -f product/docker-compose.yml build"},
			{ID: "later", RequiredFiles: []string{"product/backend/worker.py"}},
		},
	}
	v.RequiredFiles = v.UnionRequiredFiles()
	data, err := json.Marshal(rigProfileEnvelope{Version: 1, Validation: v, TestPlanReviewed: true, TestPlanFrozen: true})
	if err != nil {
		t.Fatal(err)
	}
	recoveryFile(t, root, ".gastown/workflow-profile.json", string(data))
	recoveryFile(t, root, "product/backend/main.py", "def health():\n    return {'status': 'ok'}\n")
	recoveryFile(t, root, "TEST_PLAN.md", "# Approved tests\n\n### foundation\nPhase: foundation\nTest file: product/backend/test_main.py\nBead ID: b-main\nAssertions: Health returns real API data.\n\n### later\nPhase: later\nTest file: product/backend/test_worker.py\nBead ID: pending\n")
	return root, v
}

func TestOwnedTestContract_MissingApprovedCompanionCannotFallBackToCompile(t *testing.T) {
	root, v := ownedTestFixture(t)
	tests, err := RequiredOwnedTests(root, "product/backend/main.py", v)
	if err != nil || strings.Join(tests, ",") != "product/backend/test_main.py" {
		t.Fatalf("required outputs=%v err=%v", tests, err)
	}
	for _, command := range []string{
		PythonImplementationVerifyCommandForBead(v, root, "product/backend/main.py"),
		ImplementationVerifyCommandForBead(v, root, "product/backend/main.py"),
	} {
		if !strings.Contains(command, "-m pytest") || !strings.Contains(command, "product/backend/test_main.py") || strings.Contains(command, "compileall") || strings.Contains(command, "docker") {
			t.Fatalf("approved tests were bypassed: %s", command)
		}
	}
	if result := AssessImplementationArtifact(root, "product/backend/main.py", v); result.Status != ArtifactMissing || !strings.Contains(result.Reason, "test_main.py") {
		t.Fatalf("source without its mandatory test looked complete: %+v", result)
	}
	if err := RunPythonVerifyForBead(root, "product/backend/main.py", v); err == nil || !strings.Contains(err.Error(), "required test output") {
		t.Fatalf("missing planned test did not block verification: %v", err)
	}
	// Merely adding a test file must not satisfy the artifact contract.
	recoveryFile(t, root, "product/backend/test_main.py", "def test_health():\n    pass\n")
	if result := AssessImplementationArtifact(root, "product/backend/main.py", v); result.Status != ArtifactInvalid {
		t.Fatalf("stub companion satisfied approved contract: %+v", result)
	}
	recoveryFile(t, root, "product/backend/test_main.py", "from main import health\n\ndef test_health():\n    assert health()['status'] == 'ok'\n")
	if result := AssessImplementationArtifact(root, "product/backend/main.py", v); result.Status != ArtifactReady {
		t.Fatalf("real source/test artifacts rejected: %+v", result)
	}
}

func TestOwnedTestContract_PreservesPhaseAndIndependentTestOwnership(t *testing.T) {
	root, v := ownedTestFixture(t)
	if tests, err := RequiredOwnedTests(root, "product/backend/worker.py", v); err != nil || len(tests) != 0 {
		t.Fatalf("future phase leaked into current work: %v %v", tests, err)
	}
	v.DeliveryPhases[0].RequiredFiles = append(v.DeliveryPhases[0].RequiredFiles, "product/backend/test_main.py")
	if tests, err := RequiredOwnedTests(root, "product/backend/main.py", v); err != nil || len(tests) != 0 {
		t.Fatalf("independently queued test was assigned to source bead: %v %v", tests, err)
	}
	// An unapproved draft does not silently add obligations to running code.
	recoveryFile(t, root, ".gastown/workflow-profile.json", `{"version":1,"test_plan_reviewed":false,"test_plan_frozen":false}`)
	if tests, err := RequiredOwnedTests(root, "product/backend/main.py", v); err != nil || len(tests) != 0 {
		t.Fatalf("draft plan changed completion contract: %v %v", tests, err)
	}
}

func TestOwnedTestContract_GoAssertionsMustActuallyPass(t *testing.T) {
	root := t.TempDir()
	v := WorkflowValidation{LayoutRoot: "product", TestRunner: "go", RequiredFiles: []string{"product/value.go"}, MinImplementationFileBytes: 1, MinSubstantiveLines: 1}
	recoveryFile(t, root, ".gastown/workflow-profile.json", `{"version":1,"test_plan_reviewed":true}`)
	recoveryFile(t, root, "TEST_PLAN.md", "### value\nTest file: product/value_test.go\nBead ID: b-value\n")
	recoveryFile(t, root, "product/go.mod", "module example\n\ngo 1.22\n")
	recoveryFile(t, root, "product/value.go", "package example\n\nfunc Value() int { return 42 }\n")
	for _, tc := range []struct {
		assertion string
		passes    bool
	}{{"41", false}, {"42", true}} {
		recoveryFile(t, root, "product/value_test.go", "package example\nimport \"testing\"\nfunc TestValue(t *testing.T) { if got := Value(); got != "+tc.assertion+" { t.Fatalf(\"unexpected value: %d\", got) } }\n")
		if err := RunRequiredOwnedTests(root, "product/value.go", v); (err == nil) != tc.passes {
			t.Fatalf("assertion=%s err=%v", tc.assertion, err)
		}
	}
}

func TestOwnedTestContract_ShowsCompanionInPolecatContext(t *testing.T) {
	town, rig := t.TempDir(), "rig"
	root, v := ownedTestFixture(t)
	// Context builders accept town/rig. Place the fixture at that resolved path.
	rigDir := filepath.Join(town, rig, "mayor")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(rigDir, "rig")); err != nil {
		t.Fatal(err)
	}
	setListImplementBeadsByStatusHook(t, town, rig, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "open" {
			return []PlanBead{{ID: "b-main", Title: "Implement product/backend/main.py per architecture"}, {ID: "b-style", Title: "Implement product/style.css per architecture"}}, nil
		}
		return nil, nil
	})
	for _, context := range []string{formatUnitTestGuidanceForBead(town, rig, "product/backend/main.py", v), FormatImplementationQueueBlock(town, rig, v)} {
		if !strings.Contains(context, "product/backend/test_main.py") {
			t.Fatalf("Polecat cannot see its mandatory output: %s", context)
		}
	}
}
