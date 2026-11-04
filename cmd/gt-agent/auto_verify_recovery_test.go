package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestAutoVerifyFailedTestsRemainRepairable(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	orchestrator.SetListImplementBeadsByStatusHook(func(_, _, status string) ([]orchestrator.PlanBead, error) {
		if status == "in_progress" {
			return []orchestrator.PlanBead{{ID: "example-value", Title: "Implement app/internal/store/store.go per architecture"}}, nil
		}
		return nil, nil
	})
	t.Cleanup(func() { orchestrator.SetListImplementBeadsByStatusHook(nil) })
	for _, load := range []func() (*orchestrator.WorkflowTemplate, error){
		orchestrator.LoadRigFlowTemplate, orchestrator.LoadReqFlowTemplate,
	} {
		tpl, err := load()
		if err != nil {
			t.Fatal(err)
		}
		for _, legacy := range []bool{false, true} {
			name := tpl.ID + "/canonical"
			hooks := tpl.States["implementation"].Hooks
			if legacy {
				name = tpl.ID + "/legacy-setup-hook"
				hooks.AutoVerify = []orchestrator.AutoVerifyHook{{When: "go_mod_tidy", Verify: "go_setup"}}
			}
			t.Run(name, func(t *testing.T) {
				town := t.TempDir()
				moduleDir := filepath.Join(town, "example", "mayor", "rig", "app")
				if err := os.MkdirAll(moduleDir, 0755); err != nil {
					t.Fatal(err)
				}
				files := map[string]string{
					"go.mod":                       "module example\n\ngo 1.22\n",
					"internal/store/store.go":      "package store\n\nfunc Value() string { return \"ok\" }\n",
					"internal/store/store_test.go": "package store\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) { t.Fatal(\"needs repair\") }\n",
				}
				for path, content := range files {
					if err := os.MkdirAll(filepath.Dir(filepath.Join(moduleDir, path)), 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(moduleDir, path), []byte(content), 0644); err != nil {
						t.Fatal(err)
					}
				}
				v := orchestrator.WorkflowValidation{
					LayoutRoot: "app", TestRunner: "go",
					QAVerifyCommand:            "cd app && go test ./...",
					RequiredFiles:              []string{"app/internal/store/store.go", "app/internal/store/store_test.go"},
					MinImplementationFileBytes: 1, MinSubstantiveLines: 1,
				}
				r := newStateRunner(&orchestrator.Task{
					State: "implementation", TemplateID: tpl.ID, Validation: v, Hooks: hooks,
				}, town, "example")
				r.v = v
				r.track.activeBead = "example-value"
				r.track.activeBeadPath = "app/internal/store/store.go"
				r.track.verifyOK = false
				if legacy && isImplementationVerifyCommandOK("cd example/mayor/rig/app && go mod tidy", town, "example", r.track.activeBead, r.v) {
					id, path := r.effectiveImplementBead()
					t.Fatalf("setup incorrectly matches canonical verify: id=%q path=%q qa=%q impl=%q track=%q", id, path, r.v.ActivePhaseQAVerifyCommand(), r.verifyCommand("go_implementation"), r.hooks.Track)
				}
				cmd := "cd example/mayor/rig/app && go mod tidy && go test ./..."
				out, err := r.runShellCommand(cmd, town, "", os.Environ())
				if err == nil || !strings.Contains(string(out), "needs repair") {
					t.Fatalf("expected failing test, got %v: %s", err, out)
				}
				r.track.lastVerifyOutput = string(out)
				var combined strings.Builder
				r.afterCommand(cmd, err, town, "", os.Environ(), &combined)
				if r.track.verifyOK || !r.track.hadCmdFailure {
					t.Fatalf("failed tests became green after auto-verify: %+v", r.track)
				}
				if !strings.Contains(r.track.lastVerifyOutput, "needs repair") {
					t.Fatalf("lost failed test diagnostics: %q", r.track.lastVerifyOutput)
				}
				if !r.implementBeadCloseArtifactsReady() {
					t.Fatal("fixture must reach the redundant-edit verification guard")
				}
				if err := r.rejectRedundantImplementEditAfterVerify("app/internal/store/store.go"); err != nil {
					t.Fatalf("repair edit must remain allowed: %v", err)
				}
				fixed := "package store\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) { if Value() != \"ok\" { t.Fatal(\"wrong value\") } }\n"
				if err := os.WriteFile(filepath.Join(moduleDir, "internal/store/store_test.go"), []byte(fixed), 0644); err != nil {
					t.Fatal(err)
				}
				out, err = r.runShellCommand(cmd, town, "", os.Environ())
				if err != nil {
					t.Fatalf("repaired test failed: %v: %s", err, out)
				}
				r.afterCommand(cmd, err, town, "", os.Environ(), &combined)
				if !r.track.verifyOK || r.track.hadCmdFailure {
					t.Fatalf("canonical green verify must unblock completion: %+v", r.track)
				}
				if err := r.rejectRedundantImplementEditAfterVerify("app/internal/store/store.go"); err == nil {
					t.Fatal("a real green verify must still prevent redundant edits")
				}
			})
		}
	}
}

func TestRecordAutoVerifySuccessSetupTrack(t *testing.T) {
	r := newStateRunner(&orchestrator.Task{
		Hooks: orchestrator.StateHooks{Track: "project_setup"},
	}, t.TempDir(), "example")
	r.track.hadCmdFailure = true
	r.recordAutoVerifySuccess("go mod tidy", "setup complete", true)
	if !r.track.verifyOK || r.track.hadCmdFailure || r.track.lastVerifyOutput != "setup complete" {
		t.Fatalf("setup hooks must still certify setup: %+v", r.track)
	}
}
