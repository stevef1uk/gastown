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

func TestHallucinatedTestPlanRequirements(t *testing.T) {
	specDoc := `# SPEC

### REQ-1
Requirement: GET /ping returns 200

## HTTP API
- GET /ping → 200 JSON {"message": "pong"}
`
	archDoc := `# Architecture

### REQ-1
Ownership: handlers.go
`

	t.Run("no hallucination", func(t *testing.T) {
		plan := `### REQ-1
Requirement: GET /ping returns 200
Level: unit
Test file: handlers_test.go
`
		hallucinated := HallucinatedTestPlanRequirements(plan, specDoc, archDoc)
		if len(hallucinated) != 0 {
			t.Errorf("expected no hallucinations, got %v", hallucinated)
		}
	})

	t.Run("hallucinated requirement", func(t *testing.T) {
		plan := `### REQ-1
Requirement: GET /ping returns 200
Level: unit
Test file: handlers_test.go

### REQ-2
Requirement: Health endpoint
Level: unit
Test file: health_test.go

### REQ-3
Requirement: Logger middleware
Level: unit
Test file: logger_test.go
`
		hallucinated := HallucinatedTestPlanRequirements(plan, specDoc, archDoc)
		if len(hallucinated) != 2 {
			t.Fatalf("expected 2 hallucinations, got %d: %v", len(hallucinated), hallucinated)
		}
		if hallucinated[0] != "REQ-2" {
			t.Errorf("hallucination[0] = %q, want REQ-2", hallucinated[0])
		}
		if hallucinated[1] != "REQ-3" {
			t.Errorf("hallucination[1] = %q, want REQ-3", hallucinated[1])
		}
	})

	t.Run("empty plan", func(t *testing.T) {
		hallucinated := HallucinatedTestPlanRequirements("", specDoc, archDoc)
		if len(hallucinated) != 0 {
			t.Errorf("expected no hallucinations for empty plan, got %v", hallucinated)
		}
	})

	t.Run("route-based SPEC without requirement IDs", func(t *testing.T) {
		// SPEC like testgt3/pingapp that uses ## HTTP API with route tables
		// but no ### <id> headings
		routeSpec := `# Link Shelf – MVP spec

## HTTP API

| Method | Path | Success | Error |
|--------|------|---------|-------|
| GET | /api/links | 200, JSON array | — |
| POST | /api/links | 201, JSON link | 400 |
| DELETE | /api/links/{id} | 204 | 404 |
`
		plan := `### REQ-1
Requirement: GET /api/links returns 200
Level: unit
Test file: handlers_test.go
`
		hallucinated := HallucinatedTestPlanRequirements(plan, routeSpec, "")
		if len(hallucinated) != 1 {
			t.Fatalf("expected 1 hallucination (REQ-1 not in route table), got %d: %v", len(hallucinated), hallucinated)
		}

		// Plan using route-style IDs should pass
		routePlan := `### GET /api/links
Requirement: GET /api/links returns 200
Level: unit
Test file: handlers_test.go

### POST /api/links
Requirement: POST /api/links creates link
Level: unit
Test file: handlers_test.go
`
		hallucinated = HallucinatedTestPlanRequirements(routePlan, routeSpec, "")
		if len(hallucinated) != 0 {
			t.Errorf("route-based plan should have 0 hallucinations, got %d: %v", len(hallucinated), hallucinated)
		}
	})
}
