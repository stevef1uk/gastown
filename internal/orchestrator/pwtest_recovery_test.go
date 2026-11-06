package orchestrator

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPWTestProfileRecovery(t *testing.T) {
	town := t.TempDir()
	dir := filepath.Join(town, "sample", "mayor", "rig")
	if err := os.MkdirAll(filepath.Join(dir, rigProfileDir), 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"architecture.md", "SPEC.md"} {
		b, e := os.ReadFile(filepath.Join("testdata", "pwtest_profile_recovery", name))
		if e != nil {
			t.Fatal(e)
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := marshalRigProfileJSON(rigProfileEnvelope{Version: 1, Validation: WorkflowValidation{
		LayoutRoot: "pingapp", RequiredFiles: []string{"pingapp/go.mod", "pingapp/cmd/server/main.go\"'", "pingapp/cmd/server/main_test.go"},
		QAVerifyCommand: "cd pingapp && docker compose build --no-cache && docker image prune -f",
		DeliveryPhases:  []DeliveryPhase{{ID: "required-files", RequiredFiles: []string{"pingapp/go.mod"}}, {ID: "depends-on"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, rigProfileDir, rigProfileFile), raw, 0644); err != nil {
		t.Fatal(err)
	}
	m := NewManager(town)
	inst := &WorkflowInstance{Variables: map[string]string{"rig": "sample"}}
	tpl := &WorkflowTemplate{ID: "rig-flow"}
	if _, err := m.BuildTaskPayload(inst, tpl, State{Role: "qa", Instructions: "Review architecture and profile", Hooks: StateHooks{Artifacts: "design_review"}}); err != nil {
		t.Fatal(err)
	}
	want, _, _ := architectureDeliveryContract(string(mustReadPWTest(t, filepath.Join(dir, "architecture.md"))))
	v, _, e := LoadRawRigWorkflowProfile(town, "sample")
	if e != nil {
		t.Fatal(e)
	}
	if !v.ExplicitDeliveryContract || len(v.DeliveryPhases) != 4 {
		t.Fatalf("bad sync: %+v", v)
	}
	for i := range want {
		if len(want[i].DependsOn) == 0 {
			want[i].DependsOn = nil
		}
	}
	if !reflect.DeepEqual(v.DeliveryPhases, want) {
		t.Fatalf("stale phase ownership survived: %+v", v.DeliveryPhases)
	}
	for _, f := range v.RequiredFiles {
		if f == "pingapp/cmd/server/main_test.go" || f == "pingapp/cmd/server/main.go\"'" {
			t.Fatalf("stale path survived: %s", f)
		}
	}
	if v.QAVerifyCommand != want[len(want)-1].QAVerifyCommand {
		t.Fatalf("top-level command mutated: %s", v.QAVerifyCommand)
	}
	for i, p := range want {
		if v.DeliveryPhases[i].QAVerifyCommand != p.QAVerifyCommand {
			t.Fatalf("phase command mutated: %s", v.DeliveryPhases[i].QAVerifyCommand)
		}
	}
}
func mustReadPWTest(t *testing.T, p string) []byte {
	t.Helper()
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
