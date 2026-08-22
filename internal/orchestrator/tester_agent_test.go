package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseTestPlanBlocksPhase(t *testing.T) {
	plan := `### req-1
Requirement: Go module initialization
Level: unit
Phase: go-module
Test file: pingapp/go.mod
Bead ID: pw-001

### req-2
Requirement: HTTP server
Level: unit
Phase: core
Test file: pingapp/cmd/server/main_test.go
Bead ID: pw-002

### req-3
Requirement: Web UI
Level: ui
Phase: web
Test file: pingapp/e2e/ping.spec.ts
Bead ID: pw-003
`
	blocks := ParseTestPlanBlocks(plan)
	if len(blocks) != 3 {
		t.Fatalf("expected 3 blocks, got %d", len(blocks))
	}
	if blocks[0].Phase != "go-module" {
		t.Errorf("block 0 phase = %q, want go-module", blocks[0].Phase)
	}
	if blocks[1].Phase != "core" {
		t.Errorf("block 1 phase = %q, want core", blocks[1].Phase)
	}
	if blocks[2].Phase != "web" {
		t.Errorf("block 2 phase = %q, want web", blocks[2].Phase)
	}
}

func TestParseTestPlanBlocksBackwardCompatible(t *testing.T) {
	plan := `### req-1
Requirement: Go module initialization
Level: unit
Test file: pingapp/go.mod
Bead ID: pw-001
`
	blocks := ParseTestPlanBlocks(plan)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[0].Phase != "" {
		t.Errorf("block 0 phase = %q, want empty (backward compatible)", blocks[0].Phase)
	}
}

func TestPlannedTestFilesForPhases(t *testing.T) {
	plan := `### req-1
Requirement: Go module initialization
Level: unit
Phase: go-module
Test file: pingapp/go.mod

### req-2
Requirement: HTTP server
Level: unit
Phase: core
Test file: pingapp/cmd/server/main_test.go

### req-3
Requirement: Web UI
Level: ui
Phase: web
Test file: pingapp/e2e/ping.spec.ts

### req-4
Requirement: Generic test
Level: unit
Test file: pingapp/generic_test.go
`

	// Only core phase
	files := PlannedTestFilesForPhases(plan, []string{"core"})
	if len(files) != 2 {
		t.Fatalf("expected 2 files for core phase, got %d: %v", len(files), files)
	}
	if files[0] != "pingapp/cmd/server/main_test.go" {
		t.Errorf("file 0 = %q, want pingapp/cmd/server/main_test.go", files[0])
	}
	// req-4 has no Phase, so it's always included
	if files[1] != "pingapp/generic_test.go" {
		t.Errorf("file 1 = %q, want pingapp/generic_test.go", files[1])
	}

	// Core + web phases
	files = PlannedTestFilesForPhases(plan, []string{"core", "web"})
	if len(files) != 3 {
		t.Fatalf("expected 3 files for core+web phases, got %d: %v", len(files), files)
	}

	// Empty phase list (backward compatible)
	files = PlannedTestFilesForPhases(plan, nil)
	if len(files) != 4 {
		t.Fatalf("expected 4 files with nil phases, got %d: %v", len(files), files)
	}
}

func TestMissingPlannedTestFilesForPhases(t *testing.T) {
	rigDir := t.TempDir()

	// Create test file for core phase only
	coreDir := filepath.Join(rigDir, "pingapp", "cmd", "server")
	if err := os.MkdirAll(coreDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(coreDir, "main_test.go"), []byte("package main\nimport \"testing\"\nfunc TestMain(t *testing.T) {}"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create generic test file
	if err := os.WriteFile(filepath.Join(rigDir, "pingapp", "generic_test.go"), []byte("package main\nimport \"testing\"\nfunc TestGeneric(t *testing.T) {}"), 0644); err != nil {
		t.Fatal(err)
	}

	plan := `### req-1
Requirement: Go module initialization
Level: unit
Phase: go-module
Test file: pingapp/go.mod

### req-2
Requirement: HTTP server
Level: unit
Phase: core
Test file: pingapp/cmd/server/main_test.go

### req-3
Requirement: Web UI
Level: ui
Phase: web
Test file: pingapp/e2e/ping.spec.ts

### req-4
Requirement: Generic test
Level: unit
Test file: pingapp/generic_test.go
`

	// Only core phase - should check core test and generic (no phase)
	missing := MissingPlannedTestFilesForPhases(rigDir, "", plan, []string{"core"})
	// core test exists, generic_test.go exists -> both present -> 0 missing
	if len(missing) != 0 {
		t.Fatalf("expected 0 missing for core phase, got %d: %v", len(missing), missing)
	}

	// Only web phase - web test missing, generic exists
	missing = MissingPlannedTestFilesForPhases(rigDir, "", plan, []string{"web"})
	if len(missing) != 1 {
		t.Fatalf("expected 1 missing for web phase, got %d: %v", len(missing), missing)
	}
	if missing[0] != "pingapp/e2e/ping.spec.ts" {
		t.Errorf("missing file = %q, want pingapp/e2e/ping.spec.ts", missing[0])
	}

	// go-module phase - go.mod missing, generic exists
	missing = MissingPlannedTestFilesForPhases(rigDir, "", plan, []string{"go-module"})
	if len(missing) != 1 {
		t.Fatalf("expected 1 missing for go-module phase, got %d: %v", len(missing), missing)
	}
	if missing[0] != "pingapp/go.mod" {
		t.Errorf("missing file = %q, want pingapp/go.mod", missing[0])
	}

	// No phase filter - should check all files
	missing = MissingPlannedTestFilesForPhases(rigDir, "", plan, nil)
	if len(missing) != 2 {
		t.Fatalf("expected 2 missing with nil phases (go.mod + ping.spec.ts), got %d: %v", len(missing), missing)
	}
}
