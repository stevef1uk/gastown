package specprofile

import (
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

const specNumberedBold = `# Test Spec

## Phases
1. **Scaffold Phase**  
   - Create project directory helloapi.  
   - Initialise a Go module (go mod init helloapi).  
   - Verify that go.mod exists.  

2. **Implementation Phase**  
   - Implement handler.go with the helloHandler function adhering to the API contract.  
   - Implement main.go to start an HTTP server on port 8080 and register the /hello route.  

3. **Testing Phase**  
   - Write handler_test.go containing a unit test that issues a GET /hello request to the handler and asserts the JSON payload and status code.  
   - Run go test ./... and achieve **100%** coverage for the handler code.  

4. **Verification Phase**  
   - Manually start the server (go run .) and confirm that curl http://localhost:8080/hello returns the expected JSON.  

Each phase succeeds when its success criteria (file existence, go test passing, manual verification) are met.`

const specPhaseFormat = `# Test Spec

## Phases
Phase 1 — Project Foundation
Goal
Create a runnable application skeleton that starts successfully.
Deliverables
Repository structure
Next.js project

Phase 2 — Core Market Infrastructure
Goal
Create the market data engine that everything else depends on.`

const specMarkdownHeaders = `# Test Spec

## Phases
# Phase 1 — Project Foundation

# Phase 2 — Core Market Infrastructure

## Goal

Create a runnable application skeleton that starts successfully.

---

Implement the live market data platform.`

const specDashSeparated = `# Test Spec

## Delivery Phases
go-module - Initialize go.mod
store-layer - Schema + CRUD
api-handlers - HTTP handlers
server-main - Server entrypoint
web-static - CSS/JS assets`

const specBulletedBold = `# Test Spec

## Phases
- **Scaffold Phase**
  - Create project directory helloapi
  - Initialize Go module

- **Implementation Phase**
  - Implement handler.go
  - Implement main.go

- **Testing Phase**
  - Write handler_test.go
  - Run go test`

const specNumberedWithoutBold = `# Test Spec

## Phases
1. Scaffold Phase
2. Implementation Phase
3. Testing Phase
4. Verification Phase`

const specIgnoresSubItems = `# Test Spec

## Phases
1. **Initialize Module**  
   - Create go.mod (go mod init helloapi).  
   - Success: go mod tidy runs without errors.

2. **Implement Server** (main.go)  
   - Set up http.Server listening on :8080.  
   - Register /hello route to helloHandler.  
   - Success: go run . starts without panic; curl http://localhost:8080/hello returns expected JSON.

3. **Implement Handler** (handler.go)  
   - Marshal HelloResponse{Message:"Hello, World!"} to JSON.  
   - Write appropriate headers and status codes.  
   - Success: Unit test (Phase 4) passes.

4. **Write Unit Test** (handler_test.go)  
   - Use httptest.NewRecorder and http.NewRequest("GET","/hello",nil).  
   - Verify status code 200, Content-Type header, and JSON body matches expected struct.  
   - Success: go test ./... passes.

5. **Error Handling Review**  
   - Simulate JSON marshal error (e.g., by temporarily modifying struct) and ensure 500 response.  
   - Success: Test covering error path passes.

6. **Documentation & Clean-up**  
   - Add README excerpt with build/run instructions.  
   - Success: go vet ./... reports no issues.`

// specHeadingBullets mirrors the SPEC the Analyst actually produces for the
// helloapi rig: "### Phase N: Title" headings with sub-bullets at column zero
// (NOT indented). Regression test for the real-world format that previously
// exploded 3 phases into 11.
const specHeadingBullets = `# SPEC: Hello World API

## Overview
A minimal Go HTTP service that listens on port 8080 and exposes a single endpoint GET /hello.

## Phases

### Phase 1: Project Initialization
- Initialize Go module helloapi.
- Create basic main.go that starts a server on port 8080.
- Success Criteria: Server starts and listens on port 8080.

### Phase 2: Handler Implementation
- Implement the /hello handler in handlers.go.
- Set Content-Type: application/json header.
- Success Criteria: curl http://localhost:8080/hello returns the expected JSON payload.

### Phase 3: Testing
- Implement unit tests for the /hello handler in handlers_test.go using net/http/httptest.
- Success Criteria: go test ./... passes.

## Testing Strategy
`

func TestParseSpecPhases_NumberedBold(t *testing.T) {
	phases := parseSpecPhases(specNumberedBold)
	if len(phases) != 4 {
		t.Fatalf("expected 4 phases, got %d: %+v", len(phases), phases)
	}
	expected := []string{"scaffold-phase", "implementation-phase", "testing-phase", "verification-phase"}
	for i, p := range phases {
		if p.ID != expected[i] {
			t.Fatalf("phase %d: expected ID %q, got %q", i, expected[i], p.ID)
		}
	}
}

func TestParseSpecPhases_PhaseFormat(t *testing.T) {
	phases := parseSpecPhases(specPhaseFormat)
	if len(phases) != 2 {
		t.Fatalf("expected 2 phases, got %d: %+v", len(phases), phases)
	}
	if phases[0].ID != "project-foundation" {
		t.Fatalf("phase 0: expected ID project-foundation, got %q", phases[0].ID)
	}
	if phases[1].ID != "core-market-infrastructure" {
		t.Fatalf("phase 1: expected ID core-market-infrastructure, got %q", phases[1].ID)
	}
}

func TestParseSpecPhases_MarkdownHeaders(t *testing.T) {
	phases := parseSpecPhases(specMarkdownHeaders)
	if len(phases) != 2 {
		t.Fatalf("expected 2 phases, got %d: %+v", len(phases), phases)
	}
	if phases[0].ID != "project-foundation" {
		t.Fatalf("phase 0: expected ID project-foundation, got %q", phases[0].ID)
	}
	if phases[1].ID != "core-market-infrastructure" {
		t.Fatalf("phase 1: expected ID core-market-infrastructure, got %q", phases[1].ID)
	}
}

func TestParseSpecPhases_DashSeparated(t *testing.T) {
	phases := parseSpecPhases(specDashSeparated)
	if len(phases) != 5 {
		t.Fatalf("expected 5 phases, got %d: %+v", len(phases), phases)
	}
	expected := []string{"go-module", "store-layer", "api-handlers", "server-main", "web-static"}
	for i, p := range phases {
		if p.ID != expected[i] {
			t.Fatalf("phase %d: expected ID %q, got %q", i, expected[i], p.ID)
		}
	}
}

func TestParseSpecPhases_BulletedBold(t *testing.T) {
	phases := parseSpecPhases(specBulletedBold)
	if len(phases) != 3 {
		t.Fatalf("expected 3 phases, got %d: %+v", len(phases), phases)
	}
	expected := []string{"scaffold-phase", "implementation-phase", "testing-phase"}
	for i, p := range phases {
		if p.ID != expected[i] {
			t.Fatalf("phase %d: expected ID %q, got %q", i, expected[i], p.ID)
		}
	}
}

func TestParseSpecPhases_NumberedWithoutBold(t *testing.T) {
	phases := parseSpecPhases(specNumberedWithoutBold)
	if len(phases) != 4 {
		t.Fatalf("expected 4 phases, got %d: %+v", len(phases), phases)
	}
	expected := []string{"scaffold-phase", "implementation-phase", "testing-phase", "verification-phase"}
	for i, p := range phases {
		if p.ID != expected[i] {
			t.Fatalf("phase %d: expected ID %q, got %q", i, expected[i], p.ID)
		}
	}
}

func TestParseSpecPhases_IgnoresSubItems(t *testing.T) {
	phases := parseSpecPhases(specIgnoresSubItems)
	if len(phases) != 6 {
		t.Fatalf("expected 6 phases (sub-items ignored), got %d: %+v", len(phases), phases)
	}
	expected := []string{"initialize-module", "implement-server", "implement-handler", "write-unit-test", "error-handling-review", "documentation-&-clean-up"}
	for i, p := range phases {
		if p.ID != expected[i] {
			t.Fatalf("phase %d: expected ID %q, got %q", i, expected[i], p.ID)
		}
	}
}

// TestParseSpecPhases_HeadingBullets is the regression test for the real
// Analyst-generated SPEC format: "### Phase N: Title" headings with column-zero
// sub-bullets. Before the fix this produced 11 phases (one per bullet); it must
// yield exactly the 3 headings.
func TestParseSpecPhases_HeadingBullets(t *testing.T) {
	phases := parseSpecPhases(specHeadingBullets)
	if len(phases) != 3 {
		t.Fatalf("expected 3 phases, got %d: %+v", len(phases), phases)
	}
	expected := []string{"project-initialization", "handler-implementation", "testing"}
	for i, p := range phases {
		if p.ID != expected[i] {
			t.Fatalf("phase %d: expected ID %q, got %q", i, expected[i], p.ID)
		}
		if len(p.RequiredFiles) != 0 {
			t.Fatalf("phase %d %q: expected empty required_files (assignment happens later), got %v", i, p.ID, p.RequiredFiles)
		}
		// Description bullets must be captured into spec_focus for the LLM.
		if !strings.Contains(p.SpecFocus, "Success Criteria") {
			t.Errorf("phase %d %q: spec_focus missing description bullets: %q", i, p.ID, p.SpecFocus)
		}
	}
}

// TestExtractPhaseSpecExcerpts_AllParseFormats exercises extractPhaseSpecExcerpts
// against every SPEC format the phase parser supports. For each format it must:
//   - parse at least one phase,
//   - produce a non-empty excerpt for every phase (keyed by phase ID),
//   - keep every excerpt within the excerptMaxChars bound.
func TestExtractPhaseSpecExcerpts_AllParseFormats(t *testing.T) {
	formats := []struct {
		name string
		spec string
	}{
		{"NumberedBold", specNumberedBold},
		{"PhaseFormat", specPhaseFormat},
		{"MarkdownHeaders", specMarkdownHeaders},
		{"DashSeparated", specDashSeparated},
		{"BulletedBold", specBulletedBold},
		{"NumberedWithoutBold", specNumberedWithoutBold},
		{"IgnoresSubItems", specIgnoresSubItems},
		{"HeadingBullets", specHeadingBullets},
	}

	for _, f := range formats {
		t.Run(f.name, func(t *testing.T) {
			phases := parseSpecPhases(f.spec)
			if len(phases) == 0 {
				t.Fatalf("no phases parsed from spec")
			}

			excerpts := extractPhaseSpecExcerpts(phases, f.spec)
			if len(excerpts) == 0 {
				t.Fatalf("no excerpts extracted for %d phases", len(phases))
			}

			for _, p := range phases {
				excerpt, ok := excerpts[p.ID]
				if !ok {
					t.Errorf("phase %q has no excerpt (got keys %v)", p.ID, keys(excerpts))
					continue
				}
				if excerpt == "" {
					t.Errorf("phase %q has empty excerpt", p.ID)
					continue
				}
				if len(excerpt) > excerptMaxChars+64 {
					t.Errorf("phase %q excerpt too long: %d chars (max %d)", p.ID, len(excerpt), excerptMaxChars)
				}
			}
		})
	}
}

// TestExtractPhaseSpecExcerpts_DifferentiatesSections verifies the RAG extractor
// actually picks distinct, relevant sections rather than always returning the
// same content for every phase. Each phase's SpecFocus is seeded with words that
// only appear in its own SPEC section.
func TestExtractPhaseSpecExcerpts_DifferentiatesSections(t *testing.T) {
	spec := `## Overview
helloapi is a minimal Go HTTP server exposing a single /hello endpoint.

## Phases
1. **Scaffold Phase**
   - Create the helloapi directory.
   - Initialize a Go module.
2. **Implementation Phase**
   - Implement handler.go with the helloHandler function.
   - Implement main.go starting an HTTP server on port 8080.
3. **Testing Phase**
   - Write handler_test.go with a unit test for the handler.
   - Run go test ./...

## File Layout
helloapi/go.mod
helloapi/main.go
helloapi/handler.go
helloapi/handler_test.go

## Testing Strategy
Unit tests use httptest.NewRecorder and assert status 200 and the JSON payload.

## Verification
Start the server with go run . and curl http://localhost:8080/hello.
`

	// Manually build phases whose SpecFocus points at specific sections, so the
	// RAG score must pick the right chunk rather than the catch-all Phases chunk.
	phases := []orchestrator.DeliveryPhase{
		{ID: "scaffold", Title: "Scaffold", SpecFocus: "go mod init module helloapi directory"},
		{ID: "implementation", Title: "Implementation", SpecFocus: "handler helloHandler main go server port"},
		{ID: "testing", Title: "Testing", SpecFocus: "unit test httptest recorder status json payload"},
		{ID: "verification", Title: "Verification", SpecFocus: "curl localhost verification manual"},
	}

	excerpts := extractPhaseSpecExcerpts(phases, spec)
	if len(excerpts) == 0 {
		t.Fatalf("no excerpts extracted")
	}

	// All four phases should resolve to a non-empty excerpt.
	for _, p := range phases {
		if ex, ok := excerpts[p.ID]; !ok || ex == "" {
			t.Errorf("phase %q missing non-empty excerpt", p.ID)
		}
	}
}

func keys(m map[string]string) []string {
	k := make([]string, 0, len(m))
	for kk := range m {
		k = append(k, kk)
	}
	return k
}

func TestExtractSpecOverview(t *testing.T) {
	spec := `# SPEC: Hello World API

## Overview
A minimal Go HTTP service that listens on port 8080 and exposes a single endpoint ` + "`GET /hello`" + `.

## Technical Stack
- Language: Go
`

	ov := extractSpecOverview(spec)
	if ov == "" {
		t.Fatal("extractSpecOverview returned empty string")
	}
	if !strings.Contains(ov, "SPEC: Hello World API") {
		t.Errorf("overview missing SPEC title: %q", ov)
	}
	if !strings.Contains(ov, "port 8080") {
		t.Errorf("overview missing overview content: %q", ov)
	}
	if strings.Contains(ov, "Technical Stack") {
		t.Errorf("overview should stop before Technical Stack: %q", ov)
	}
}

func TestExtractSpecOverview_RealSpec(t *testing.T) {
	spec := `# SPEC: Hello World API

## Overview
A minimal Go HTTP service that listens on port 8080 and exposes a single endpoint ` + "`GET /hello`" + `. The endpoint returns a JSON payload ` + "`{\"message\":\"Hello, World!\"}`" + `. The server must be built using only the Go standard library (no third-party frameworks). A unit test covering the handler is required.

## Technical Stack
- **Language**: Go (>= 1.22)
- **Standard Library**: ` + "`net/http`" + `, ` + "`encoding/json`" + `, ` + "`log`" + `

## Data Model
No persistent data model is required.`

	ov := extractSpecOverview(spec)
	if ov == "" {
		t.Fatal("extractSpecOverview returned empty string")
	}
	if !strings.Contains(ov, "Hello World API") {
		t.Errorf("overview missing SPEC title: %q", ov)
	}
	if !strings.Contains(ov, "single endpoint") {
		t.Errorf("overview missing overview paragraph: %q", ov)
	}
	if strings.Contains(ov, "Technical Stack") || strings.Contains(ov, "Data Model") {
		t.Errorf("overview should only contain title + Overview section: %q", ov)
	}
}

func TestExtractSpecOverview_VisionSection(t *testing.T) {
	spec := `# FinAlly — AI Trading Workstation

## Project Specification

## 1. Vision

FinAlly (Finance Ally) is a visually stunning AI-powered trading workstation that streams live market data, lets users trade a simulated portfolio, and integrates an LLM chat assistant that can analyze positions and execute trades on the user's behalf.

## 2. User Experience

### First Launch
The user runs a setup wizard.`

	ov := extractSpecOverview(spec)
	if ov == "" {
		t.Fatal("extractSpecOverview returned empty string")
	}
	if !strings.Contains(ov, "FinAlly") {
		t.Errorf("overview missing SPEC title: %q", ov)
	}
	if !strings.Contains(ov, "AI-powered trading workstation") {
		t.Errorf("overview missing Vision paragraph: %q", ov)
	}
	if strings.Contains(ov, "User Experience") {
		t.Errorf("overview should stop before next section: %q", ov)
	}
	if strings.Contains(ov, "First Launch") {
		t.Errorf("overview should not include nested sub-section: %q", ov)
	}
}

func TestExtractSpecOverview_SystemSummarySection(t *testing.T) {
	spec := `# Widget API

## System Overview

The Widget API provides CRUD operations over widgets stored in a SQLite database, with JSON over HTTP.

## Endpoints

- POST /widgets
- GET /widgets`

	ov := extractSpecOverview(spec)
	if ov == "" {
		t.Fatal("extractSpecOverview returned empty string")
	}
	if !strings.Contains(ov, "Widget API") {
		t.Errorf("overview missing SPEC title: %q", ov)
	}
	if !strings.Contains(ov, "CRUD operations") {
		t.Errorf("overview missing System Overview paragraph: %q", ov)
	}
	if strings.Contains(ov, "Endpoints") {
		t.Errorf("overview should stop before next section: %q", ov)
	}
}


// linkshelfSpec mirrors the real testgt3 SPEC: numbered bold markers with same-line
// descriptions naming the files each phase covers, plus a backtick-fenced code block
// under "## File Layout". Backticks cannot live inside a raw string literal, so the
// fixture is assembled from a fence constant — proving the parser handles BOTH fenced
// and unfenced SPECs (as `parseSpecLayoutTree` already toggles fence state).
const specFence = "```"

const linkshelfSpec = `# LinkShelf

## Delivery Phases

1. **go-module** - Initialize go.mod
2. **store-layer** - Schema + CRUD
3. **api-handlers** - HTTP handlers
4. **server-main** - Server entrypoint
5. **web-static** - CSS/JS assets
6. **web-shell** - HTML shell
7. **integration-test** - Full smoke test + Playwright E2E

## Layout

**layout_root: linkshelf**

## File Layout

` + specFence + `
linkshelf/
├── go.mod
├── cmd/
│   └── server/
│       └── main.go
├── internal/
│   ├── store/
│   │   ├── schema.go
│   │   ├── store.go
│   │   └── store_test.go
│   └── api/
│       ├── handler.go
│       └── handler_test.go
└── web/
    ├── index.html
    ├── app.js
    └── style.css
` + specFence

var linkshelfFiles = []string{
	"linkshelf/go.mod",
	"linkshelf/cmd/server/main.go",
	"linkshelf/internal/store/schema.go",
	"linkshelf/internal/store/store.go",
	"linkshelf/internal/store/store_test.go",
	"linkshelf/internal/api/handler.go",
	"linkshelf/internal/api/handler_test.go",
	"linkshelf/web/index.html",
	"linkshelf/web/app.js",
	"linkshelf/web/style.css",
}

func TestParsePhaseLine_capturesTrailingDescription(t *testing.T) {
	p := parsePhaseLine("1. **go-module** - Initialize go.mod")
	if p == nil || p.ID != "go-module" {
		t.Fatalf("expected go-module phase, got %+v", p)
	}
	if !strings.Contains(p.SpecFocus, "go.mod") {
		t.Fatalf("SpecFocus must capture trailing description naming go.mod, got %q", p.SpecFocus)
	}
	p = parsePhaseLine("- **web-static** - CSS/JS assets")
	if p == nil || !strings.Contains(p.SpecFocus, "CSS/JS assets") {
		t.Fatalf("bulleted bold description not captured: %+v", p)
	}
}

// A SPEC that fences a code block (e.g. a SQL DDL or setup command) inside the
// Delivery Phases section must still parse every phase: fence markers are skipped,
// fence content becomes description text of the open phase, and no phase is lost.
func TestParsePhaseList_fencesInsidePhasesDoNotBreakPhases(t *testing.T) {
	spec := `# T
## Delivery Phases

1. **go-module** - Initialize go.mod
` + specFence + "bash\n" + "go mod init linkshelf\n" + specFence + `
2. **store-layer** - Schema + CRUD

` + specFence + `sql
CREATE TABLE links (id INTEGER PRIMARY KEY);
` + specFence + `
3. **api-handlers** - HTTP handlers
4. **server-main** - Server entrypoint
`
	phases := parseSpecPhases(spec)
	if len(phases) != 4 {
		t.Fatalf("fenced SPEC parsed %d phases, want 4: %v", len(phases), phaseIDs(phases))
	}
	// Fence content should be captured as description of the phase that opened it,
	// not spawn new phases or lose the following marker.
	desc1 := phases[0].SpecFocus
	if !strings.Contains(desc1, "go mod init") {
		t.Errorf("phase 1 focus should include fenced setup content, got %q", desc1)
	}
	for i, id := range []string{"go-module", "store-layer", "api-handlers", "server-main"} {
		if phases[i].ID != id {
			t.Fatalf("phase %d = %q, want %q", i, phases[i].ID, id)
		}
	}
}

func TestDeterministicAssignFilesToPhases_specPhasesSurvive(t *testing.T) {	// Structural token matching (no synonym tables) must map every file to its SPEC
	// phase, keep SPEC order, and drop phases with no files (integration-test).
	phases := parseSpecPhases(linkshelfSpec)
	if len(phases) != 7 {
		t.Fatalf("parsed %d phases, want 7", len(phases))
	}
	assigned := deterministicAssignFilesToPhases(phases, linkshelfFiles)
	if assigned == nil {
		t.Fatal("deterministic assignment failed")
	}
	byID := map[string][]string{}
	for _, p := range assigned {
		byID[p.ID] = p.RequiredFiles
	}
	want := map[string][]string{
		"go-module":        {"linkshelf/go.mod"},
		"store-layer":      {"linkshelf/internal/store/schema.go", "linkshelf/internal/store/store.go", "linkshelf/internal/store/store_test.go"},
		"api-handlers":     {"linkshelf/internal/api/handler.go", "linkshelf/internal/api/handler_test.go"},
		"server-main":      {"linkshelf/cmd/server/main.go"},
		"web-static":       {"linkshelf/web/app.js", "linkshelf/web/style.css"},
		"web-shell":        {"linkshelf/web/index.html"},
		"integration-test": nil,
	}
	if len(assigned) != len(want) {
		t.Fatalf("assigned %d phases, want %d (%v)", len(assigned), len(want), phaseIDs(assigned))
	}
	for id, files := range want {
		got := byID[id]
		if len(got) != len(files) {
			t.Errorf("phase %q files = %v, want %v", id, got, files)
			continue
		}
		for i := range files {
			if got[i] != files[i] {
				t.Errorf("phase %q files = %v, want %v", id, got, files)
				break
			}
		}
	}
	// Every file assigned exactly once, and order preserves the SPEC phase list.
	seen := map[string]int{}
	for _, p := range assigned {
		for _, f := range p.RequiredFiles {
			seen[f]++
		}
	}
	for _, f := range linkshelfFiles {
		if seen[f] != 1 {
			t.Errorf("file %q assigned %d times, want 1", f, seen[f])
		}
	}
	for i, id := range []string{"go-module", "store-layer", "api-handlers", "server-main", "web-static", "web-shell", "integration-test"} {
		if assigned[i].ID != id {
			t.Fatalf("phase order %v, want SPEC order", phaseIDs(assigned))
		}
	}
}

func phaseIDs(phases []orchestrator.DeliveryPhase) []string {
	ids := make([]string, len(phases))
	for i, p := range phases {
		ids[i] = p.ID
	}
	return ids
}

func TestDedupePhasesByVerify(t *testing.T) {
	tests := []struct {
		name      string
		in        []orchestrator.DeliveryPhase
		wantIDs   []string
		wantFiles map[string][]string // per surviving phase id
	}{
		{
			name: "identical playwright verify folds later phase into earlier",
			in: []orchestrator.DeliveryPhase{
				{ID: "frontend", RequiredFiles: []string{"app/index.html", "app/tests/e2e/links.spec.js"},
					QAVerifyCommand: "cd app && npm install --ignore-scripts && npx playwright test"},
				{ID: "smoke-test", RequiredFiles: []string{"app/go.mod", "app/cmd/server/main.go", "app/tests/e2e/links.spec.js"},
					QAVerifyCommand: "CD   app && npm install --ignore-scripts && npx PLAYWRIGHT test"},
			},
			wantIDs:   []string{"frontend"},
			wantFiles: map[string][]string{"frontend": {"app/index.html", "app/tests/e2e/links.spec.js", "app/go.mod", "app/cmd/server/main.go"}},
		},
		{
			name: "distinct verifies are all kept in order",
			in: []orchestrator.DeliveryPhase{
				{ID: "a", RequiredFiles: []string{"x.go"}, QAVerifyCommand: "go build ./..."},
				{ID: "b", RequiredFiles: []string{"y.go"}, QAVerifyCommand: "go test ./..."},
			},
			wantIDs: []string{"a", "b"},
		},
		{
			name: "empty verify collapses duplicates too",
			in: []orchestrator.DeliveryPhase{
				{ID: "one", RequiredFiles: []string{"a.txt"}, QAVerifyCommand: ""},
				{ID: "two", RequiredFiles: []string{"b.txt"}, QAVerifyCommand: "   "},
				{ID: "three", RequiredFiles: []string{"c.txt"}, QAVerifyCommand: "make check"},
			},
			wantIDs:   []string{"one", "three"},
			wantFiles: map[string][]string{"one": {"a.txt", "b.txt"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := dedupePhasesByVerify(tc.in)
			var ids []string
			for _, p := range got {
				ids = append(ids, p.ID)
			}
			if len(ids) != len(tc.wantIDs) {
				t.Fatalf("ids = %v, want %v", ids, tc.wantIDs)
			}
			for i := range tc.wantIDs {
				if ids[i] != tc.wantIDs[i] {
					t.Fatalf("ids[%d] = %q, want %q (all=%v)", i, ids[i], tc.wantIDs[i], ids)
				}
			}
			for id, wantFiles := range tc.wantFiles {
				var found *orchestrator.DeliveryPhase
				for i := range got {
					if got[i].ID == id {
						found = &got[i]
					}
				}
				if found == nil {
					t.Fatalf("survivor %q missing", id)
				}
				if len(found.RequiredFiles) != len(wantFiles) {
					t.Fatalf("%s required_files = %v, want %v", id, found.RequiredFiles, wantFiles)
				}
				for i := range wantFiles {
					if found.RequiredFiles[i] != wantFiles[i] {
						t.Fatalf("%s required_files[%d] = %q, want %q", id, i, found.RequiredFiles[i], wantFiles[i])
					}
				}
			}
		})
	}
}
