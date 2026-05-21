package main

import (
	"strings"
	"testing"
)

func TestPreprocessOrchestratedResponse_gluedEndEditCMD(t *testing.T) {
	in := `EDIT: linkshelf/internal/store/store_test.go
<<<<<<< SEARCH
store, err := NewStore()
=======
store, err := New()
>>>>>>> REPLACE
---END EDIT---CMD: cd rig/mayor/rig && go test ./linkshelf/internal/store -count=1 -v`
	got := preprocessOrchestratedResponse(in)
	if !strings.Contains(got, "---END EDIT---\nCMD:") && !strings.Contains(got, ">>>>>>> REPLACE\nCMD:") {
		t.Fatalf("want split END EDIT from CMD, got %q", got)
	}
	cmds := parseOrchestratedCommands(in)
	if len(cmds) < 1 {
		t.Fatalf("cmds = %v", cmds)
	}
	if strings.Contains(cmds[0], "END EDIT") {
		t.Fatalf("first cmd should be shell only: %q", cmds[0])
	}
}

func TestSanitizeBdListCommand_limitGluedWithProse(t *testing.T) {
	cmd := "export BEADS_DIR=x && cd testgt3/mayor/rig && bd list --limit=0We need to output result"
	fixed, changed := sanitizeBdListCommand(cmd)
	if !changed {
		t.Fatal("expected change")
	}
	if strings.Contains(fixed, "0We") {
		t.Fatalf("prose still glued to limit: %q", fixed)
	}
	if !strings.Contains(fixed, "--limit=0") {
		t.Fatalf("want --limit=0: %q", fixed)
	}
}

func TestValidateBdCommandBeadID_rejectsNumericClose(t *testing.T) {
	err := validateBdCommandBeadID("bd close 12", "", "testgt3")
	if err == nil || !strings.Contains(err.Error(), "bare number") {
		t.Fatalf("err = %v", err)
	}
}

func TestIsBdInfrastructureFailure_detectsDoltMissing(t *testing.T) {
	out := `Error: failed to open database: database "testgt3" not found on Dolt server at 127.0.0.1:3307`
	if !isBdInfrastructureFailure(nil, out) {
		t.Fatal("expected dolt missing detection")
	}
}

func TestParseOrchestratedNativeEdits_acceptsEndEditAlias(t *testing.T) {
	in := `EDIT: linkshelf/internal/store/store_test.go
<<<<<<< SEARCH
old
=======
new
---END EDIT---
`
	ops := parseOrchestratedNativeEdits(in)
	if len(ops) != 1 || ops[0].kind != "edit" || ops[0].search != "old" {
		t.Fatalf("ops = %+v", ops)
	}
}
