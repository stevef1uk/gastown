package orchestrator

import (
	"testing"
)

// TestConsolidateRedundantPhases_removesTestPhaseDuplicateOfCore covers the ping_rig
// regression where "core" and "test" phases both required main_test.go and ran go test.
// The test phase was redundant since it depended on core and had identical verify.
func TestConsolidateRedundantPhases_removesTestPhaseDuplicateOfCore(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot: "pingapp",
		DeliveryPhases: []DeliveryPhase{
			{
				ID:              "go-module",
				RequiredFiles:   []string{"pingapp/go.mod"},
				QAVerifyCommand: "cd pingapp && echo 'verify ok'",
			},
			{
				ID:              "core",
				RequiredFiles:   []string{"pingapp/main.go", "pingapp/main_test.go"},
				QAVerifyCommand: "cd pingapp && go test ./...",
				DependsOn:       []string{"go-module"},
			},
			{
				ID:              "test",
				RequiredFiles:   []string{"pingapp/main_test.go"},
				QAVerifyCommand: "cd pingapp && go test ./...",
				DependsOn:       []string{"core"},
			},
			{
				ID:              "integration-test",
				RequiredFiles:   []string{"pingapp/main.go"},
				QAVerifyCommand: "cd pingapp && go build ./...",
				DependsOn:       []string{"test"},
			},
		},
	}

	got := consolidateRedundantPhases(v)
	if len(got.DeliveryPhases) != 3 {
		t.Fatalf("expected 3 phases after consolidation, got %d: %+v", len(got.DeliveryPhases), got.DeliveryPhases)
	}
	ids := make([]string, len(got.DeliveryPhases))
	for i, p := range got.DeliveryPhases {
		ids[i] = p.ID
	}
	expected := []string{"go-module", "core", "integration-test"}
	for i, id := range expected {
		if ids[i] != id {
			t.Fatalf("phase %d: expected %q, got %q", i, id, ids[i])
		}
	}
	// integration-test should now depend on core (since test was removed)
	for _, p := range got.DeliveryPhases {
		if p.ID == "integration-test" {
			if len(p.DependsOn) != 1 || p.DependsOn[0] != "core" {
				t.Fatalf("integration-test should depend on core after test removal, got %v", p.DependsOn)
			}
		}
	}
}

// TestConsolidateRedundantPhases_keepsDistinctPhases ensures phases with different
// required files or verify commands are not consolidated.
func TestConsolidateRedundantPhases_keepsDistinctPhases(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot: "app",
		DeliveryPhases: []DeliveryPhase{
			{
				ID:              "core",
				RequiredFiles:   []string{"app/main.go", "app/main_test.go"},
				QAVerifyCommand: "cd app && go test ./...",
			},
			{
				ID:              "test",
				RequiredFiles:   []string{"app/main_test.go", "app/other_test.go"},
				QAVerifyCommand: "cd app && go test ./...",
				DependsOn:       []string{"core"},
			},
		},
	}

	got := consolidateRedundantPhases(v)
	if len(got.DeliveryPhases) != 2 {
		t.Fatalf("expected 2 phases (test has extra file), got %d: %+v", len(got.DeliveryPhases), got.DeliveryPhases)
	}
}

// TestConsolidateRedundantPhases_keepsDifferentVerify ensures phases with
// different verify commands are not consolidated.
func TestConsolidateRedundantPhases_keepsDifferentVerify(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot: "app",
		DeliveryPhases: []DeliveryPhase{
			{
				ID:              "core",
				RequiredFiles:   []string{"app/main.go", "app/main_test.go"},
				QAVerifyCommand: "cd app && go test ./...",
			},
			{
				ID:              "test",
				RequiredFiles:   []string{"app/main_test.go"},
				QAVerifyCommand: "cd app && go test -race ./...", // different verify
				DependsOn:       []string{"core"},
			},
		},
	}

	got := consolidateRedundantPhases(v)
	if len(got.DeliveryPhases) != 2 {
		t.Fatalf("expected 2 phases (different verify), got %d: %+v", len(got.DeliveryPhases), got.DeliveryPhases)
	}
}

// TestConsolidateRedundantPhases_emptyVerifyTreatedAsSame treats an empty
// verify in the dependent phase as matching the predecessor's verify.
func TestConsolidateRedundantPhases_emptyVerifyTreatedAsSame(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot: "app",
		DeliveryPhases: []DeliveryPhase{
			{
				ID:              "core",
				RequiredFiles:   []string{"app/main.go", "app/main_test.go"},
				QAVerifyCommand: "cd app && go test ./...",
			},
			{
				ID:              "test",
				RequiredFiles:   []string{"app/main_test.go"},
				QAVerifyCommand: "", // empty - should match core
				DependsOn:       []string{"core"},
			},
		},
	}

	got := consolidateRedundantPhases(v)
	if len(got.DeliveryPhases) != 1 {
		t.Fatalf("expected 1 phase (empty verify matches), got %d: %+v", len(got.DeliveryPhases), got.DeliveryPhases)
	}
	if got.DeliveryPhases[0].ID != "core" {
		t.Fatalf("expected core phase kept, got %q", got.DeliveryPhases[0].ID)
	}
}

// TestConsolidateRedundantPhases_noDependsOnDoesNotConsolidate ensures phases
// without depends_on are not consolidated.
func TestConsolidateRedundantPhases_noDependsOnDoesNotConsolidate(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot: "app",
		DeliveryPhases: []DeliveryPhase{
			{
				ID:              "core",
				RequiredFiles:   []string{"app/main.go", "app/main_test.go"},
				QAVerifyCommand: "cd app && go test ./...",
			},
			{
				ID:              "test",
				RequiredFiles:   []string{"app/main_test.go"},
				QAVerifyCommand: "cd app && go test ./...",
				// no DependsOn
			},
		},
	}

	got := consolidateRedundantPhases(v)
	if len(got.DeliveryPhases) != 2 {
		t.Fatalf("expected 2 phases (no depends_on), got %d: %+v", len(got.DeliveryPhases), got.DeliveryPhases)
	}
}

// TestConsolidateRedundantPhases_multiDependsOnDoesNotConsolidate ensures
// phases with multiple dependencies are not consolidated.
func TestConsolidateRedundantPhases_multiDependsOnDoesNotConsolidate(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot: "app",
		DeliveryPhases: []DeliveryPhase{
			{
				ID:              "core",
				RequiredFiles:   []string{"app/main.go"},
				QAVerifyCommand: "cd app && go build ./...",
			},
			{
				ID:              "test",
				RequiredFiles:   []string{"app/main_test.go"},
				QAVerifyCommand: "cd app && go test ./...",
			},
			{
				ID:              "integration",
				RequiredFiles:   []string{"app/main_test.go"},
				QAVerifyCommand: "cd app && go test ./...",
				DependsOn:       []string{"core", "test"}, // multiple deps
			},
		},
	}

	got := consolidateRedundantPhases(v)
	if len(got.DeliveryPhases) != 3 {
		t.Fatalf("expected 3 phases (multi depends_on), got %d: %+v", len(got.DeliveryPhases), got.DeliveryPhases)
	}
}
