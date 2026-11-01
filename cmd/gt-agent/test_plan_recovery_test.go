package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

// Exercise the real author and reviewer gates with a closed previous-phase bead,
// a build-only active phase, and a future test phase whose bead doesn't exist.
// All CLI calls are stubbed; these checks never use a town or Beads database.
func TestTestPlanGatesRespectDeliveryTiming(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	bd := `#!/bin/sh
case "$*" in
  "config get issue_prefix") printf 'zz\n' ;;
  *--status=closed*) printf '%s\n' '[{"id":"zz-old1","title":"Implement app/go.mod per architecture"}]' ;;
  *) printf '[]\n' ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "bd"), []byte(bd), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GT_ROOT", root)
	t.Setenv("BEADS_DIR", filepath.Join(root, ".beads"))
	rigDir := filepath.Join(root, "sample", "mayor", "rig")
	if err := os.MkdirAll(filepath.Join(rigDir, ".gastown"), 0755); err != nil {
		t.Fatal(err)
	}
	v := orchestrator.WorkflowValidation{
		LayoutRoot: "app", MinTestPlanBytes: 1, ActivePhaseIDField: "build",
		RequiredFiles: []string{"app/go.mod", "app/main.go", "app/main_test.go"},
		DeliveryPhases: []orchestrator.DeliveryPhase{
			{ID: "module", RequiredFiles: []string{"app/go.mod"}, QAVerifyCommand: "cd app && go mod tidy"},
			{ID: "build", RequiredFiles: []string{"app/main.go"}, QAVerifyCommand: "cd app && go build ./..."},
			{ID: "checks", RequiredFiles: []string{"app/main_test.go"}, QAVerifyCommand: "cd app && go test ./..."},
		},
	}
	profile, err := json.Marshal(map[string]interface{}{"version": 1, "validation": v})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, ".gastown", "workflow-profile.json"), profile, 0644); err != nil {
		t.Fatal(err)
	}
	plan := `# Test Plan
### module
Requirement: module declarations match SPEC
Level: integration
Test file:
Bead ID: zz-old1
Phase: module
Scenarios: run module verification
Assertions: module verification exits zero
### build
Requirement: build the application
Level: integration
Test file:
Bead ID:
Phase: build
Scenarios: run build verification
Assertions: build exits zero
### checks
Requirement: test the application
Level: unit
Test file: app/main_test.go
Bead ID:
Phase: checks
Scenarios: exercise the specified behavior
Assertions: results match SPEC
`
	for _, gate := range []struct {
		name string
		run  func(orchestrator.WorkflowValidation) error
	}{
		{"author", func(v orchestrator.WorkflowValidation) error {
			return validateTestPlanArtifacts(root, "sample", false, true, v)
		}},
		{"reviewer", func(v orchestrator.WorkflowValidation) error {
			return validateTestPlanReviewArtifacts(root, "sample", v)
		}},
	} {
		t.Run(gate.name, func(t *testing.T) {
			for _, tc := range []struct {
				name, doc, want string
			}{
				{"valid deferred work", plan, ""},
				{"invented path", strings.ReplaceAll(plan, "app/main_test.go", "app/imaginary_test.go"), "required_files"},
				{"wrong owning phase", strings.Replace(plan, "Test file:\nBead ID:\nPhase: build", "Test file: app/main_test.go\nBead ID:\nPhase: build", 1), "required_files"},
				{"missing test path", strings.ReplaceAll(plan, "Test file: app/main_test.go", "Test file:"), "missing required fields"},
				{"fabricated bead", strings.ReplaceAll(plan, "zz-old1", "zz-fake1"), "invalid bead IDs"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					if err := os.WriteFile(filepath.Join(rigDir, "TEST_PLAN.md"), []byte(tc.doc), 0644); err != nil {
						t.Fatal(err)
					}
					err := gate.run(v)
					if tc.want == "" && err != nil {
						t.Fatal(err)
					}
					if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
						t.Fatalf("want error containing %q, got %v", tc.want, err)
					}
				})
			}
			t.Run("explicit no-test phases need no fabricated rows", func(t *testing.T) {
				summary := "# Verification summary\nThe profile explicitly declares no automated tests; inspect the setup output.\n"
				if err := os.WriteFile(filepath.Join(rigDir, "TEST_PLAN.md"), []byte(summary), 0644); err != nil {
					t.Fatal(err)
				}
				if err := gate.run(v); err == nil {
					t.Fatal("a project with automated tests must not accept a row-less summary")
				}
				noTests := v
				noTests.DeliveryPhases = []orchestrator.DeliveryPhase{{ID: "bootstrap", QAVerifyCommand: "echo 'no automated tests'"}}
				if err := gate.run(noTests); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

func TestTestPlanFieldsForUnphasedBuildOnlyProject(t *testing.T) {
	v := orchestrator.WorkflowValidation{RequiredFiles: []string{"requirements.txt", "service.py"}}
	rows := []orchestrator.TestPlanBlock{{ReqID: "build", Level: "integration"}}
	if missing := missingTestPlanRequiredFields(v, rows); len(missing) != 0 {
		t.Fatalf("unphased project without declared tests must not invent paths: %v", missing)
	}
	v.RequiredFiles = append(v.RequiredFiles, "tests/test_service.py")
	if missing := missingTestPlanRequiredFields(v, rows); len(missing) == 0 {
		t.Fatal("a project declaring tests must provide its test mapping")
	}
}
