package specprofile

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

// mockLLMHandler returns an OpenAI-style chat completion whose message content
// is the provided raw string (simulating whatever the model chose to emit).
func mockLLMHandler(t *testing.T, content string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("mock proxy: bad request body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// The system summary must reach the model for the assignment prompt.
		var hasSystemSummary bool
		var hasPhaseIDs, hasFiles bool
		for _, m := range req.Messages {
			if strings.Contains(m.Content, "SYSTEM SUMMARY:") && strings.Contains(m.Content, "Hello World API") && strings.Contains(m.Content, "port 8080") {
				hasSystemSummary = true
			}
			if strings.Contains(m.Content, "PHASES:") {
				hasPhaseIDs = true
			}
			if strings.Contains(m.Content, "FILES (must all be assigned exactly once)") {
				hasFiles = true
			}
		}
		if !hasSystemSummary || !hasPhaseIDs || !hasFiles {
			t.Errorf("mock proxy: prompt missing required sections (summary=%v phases=%v files=%v)",
				hasSystemSummary, hasPhaseIDs, hasFiles)
		}

		resp := map[string]interface{}{
			"choices": []map[string]interface{}{
				{
					"message":       map[string]interface{}{"role": "assistant", "content": content},
					"finish_reason": "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}
}

// runLLMAssignment spins up a mock proxy returning the given response content
// and runs the full LLM phase-assignment pipeline against it.
func runLLMAssignment(t *testing.T, townRoot string, content string, phases []orchestrator.DeliveryPhase, files []string) ([]orchestrator.DeliveryPhase, bool) {
	t.Helper()
	srv := httptest.NewServer(mockLLMHandler(t, content))
	defer srv.Close()

	t.Setenv("LLM_ENDPOINT", srv.URL)
	t.Setenv("LLM_MODEL", "test/mock")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return assignFilesToPhasesViaLLM(ctx, townRoot, "test-rig", sampleHelloSpec(), phases, files)
}

// sampleHelloSpec is the SPEC used for the mock-based LLM assignment tests. Its
// File Layout / Phases sections mirror the real helloapi rig SPEC format.
func sampleHelloSpec() string {
	return `# SPEC: Hello World API

## Overview
A minimal Go HTTP service that listens on port 8080 and exposes a single endpoint GET /hello. The endpoint returns a JSON payload {"message":"Hello, World!"}. The server must be built using only the Go standard library (no third-party frameworks). A unit test covering the handler is required.

## Phases
1. **Scaffold Phase**
   - Create project directory helloapi.
   - Initialise a Go module.
2. **Implementation Phase**
   - Implement handler.go and main.go.
3. **Testing Phase**
   - Write handler_test.go.

## File Layout
helloapi/go.mod
helloapi/main.go
helloapi/handler.go
helloapi/handler_test.go
`
}

func helloPhaseFixtures() ([]orchestrator.DeliveryPhase, []string) {
	phases := []orchestrator.DeliveryPhase{
		{ID: "scaffold-phase", Title: "Scaffold Phase", RequiredFiles: []string{}, SpecFocus: "Scaffold Phase"},
		{ID: "implementation-phase", Title: "Implementation Phase", RequiredFiles: []string{}, SpecFocus: "Implementation Phase"},
		{ID: "testing-phase", Title: "Testing Phase", RequiredFiles: []string{}, SpecFocus: "Testing Phase"},
	}
	files := []string{
		"helloapi/go.mod",
		"helloapi/main.go",
		"helloapi/handler.go",
		"helloapi/handler_test.go",
	}
	return phases, files
}

func requireFullCoverage(t *testing.T, phases []orchestrator.DeliveryPhase, files []string) {
	t.Helper()
	count := map[string]int{}
	for _, p := range phases {
		for _, f := range p.RequiredFiles {
			count[f]++
		}
	}
	for _, f := range files {
		if count[f] != 1 {
			t.Errorf("file %q assigned %d times (want exactly 1)", f, count[f])
		}
	}
	if len(count) != len(files) {
		t.Errorf("got %d distinct files, want %d", len(count), len(files))
	}
}

// TestAssignFilesToPhasesViaLLM_VariedResponses verifies that the LLM phase
// assignment pipeline tolerates response variability — differing JSON spacing,
// key ordering, markdown fences, think tags, and trailing prose — while still
// achieving the goal: every file assigned to exactly one phase.
func TestAssignFilesToPhasesViaLLM_VariedResponses(t *testing.T) {
	phases, files := helloPhaseFixtures()

	validMappings := []string{
		`{"scaffold-phase":["helloapi/go.mod"],"implementation-phase":["helloapi/main.go","helloapi/handler.go"],"testing-phase":["helloapi/handler_test.go"]}`,
		// Different key order + whitespace
		`{  "testing-phase" : [ "helloapi/handler_test.go" ] , "implementation-phase" : ["helloapi/handler.go","helloapi/main.go"] , "scaffold-phase" : [ "helloapi/go.mod" ] }`,
		// Wrapped in a markdown code fence
		"```json\n{\"implementation-phase\":[\"helloapi/main.go\",\"helloapi/handler.go\"],\"testing-phase\":[\"helloapi/handler_test.go\"],\"scaffold-phase\":[\"helloapi/go.mod\"]}\n```",
		// Fenced with "json" label plus surrounding prose
		"Here is the assignment:\n```\n{\"scaffold-phase\":[\"helloapi/go.mod\"],\"implementation-phase\":[\"helloapi/handler.go\",\"helloapi/main.go\"],\"testing-phase\":[\"helloapi/handler_test.go\"]}\n```\nThat should cover everything.",
		// With a think block prefix
		"Sure, let me think...\n{\"implementation-phase\":[\"helloapi/main.go\",\"helloapi/handler.go\"],\"scaffold-phase\":[\"helloapi/go.mod\"],\"testing-phase\":[\"helloapi/handler_test.go\"]}",
		// Files in different order within a phase
		`{"testing-phase":["helloapi/handler_test.go"],"implementation-phase":["helloapi/handler.go","helloapi/main.go"],"scaffold-phase":["helloapi/go.mod"]}`,
	}

	for i, content := range validMappings {
		t.Run(fmt.Sprintf("variation-%d", i), func(t *testing.T) {
			assigned, ok := runLLMAssignment(t, "/tmp/gt-town", content, phases, files)
			if !ok {
				t.Fatalf("expected valid assignment to be accepted, got ok=false")
			}
			requireFullCoverage(t, assigned, files)
			// Phase order must be preserved
			for i, p := range assigned {
				if p.ID != phases[i].ID {
					t.Errorf("phase order changed: index %d want %q got %q", i, phases[i].ID, p.ID)
				}
			}
		})
	}
}

// TestAssignFilesToPhasesViaLLM_RejectsHallucinations verifies the pipeline
// rejects LLM responses that hallucinate files, omit files, or use unknown
// phase IDs — those must fail validation rather than silently corrupting the
// profile.
func TestAssignFilesToPhasesViaLLM_RejectsHallucinations(t *testing.T) {
	phases, files := helloPhaseFixtures()

	badMappings := []string{
		// Hallucinated file not in the parser's set
		`{"scaffold-phase":["helloapi/go.mod"],"implementation-phase":["helloapi/main.go","helloapi/handler.go","helloapi/handler.go.bak"],"testing-phase":["helloapi/handler_test.go"]}`,
		// Omits a file entirely
		`{"scaffold-phase":["helloapi/go.mod"],"implementation-phase":["helloapi/main.go"],"testing-phase":["helloapi/handler_test.go"]}`,
		// Assigns a file to two phases
		`{"scaffold-phase":["helloapi/go.mod"],"implementation-phase":["helloapi/main.go","helloapi/handler.go"],"testing-phase":["helloapi/handler.go","helloapi/handler_test.go"]}`,
		// Unknown phase IDs (files silently dropped by lookup)
		`{"bogus-phase":["helloapi/go.mod"],"wrong-phase":["helloapi/main.go"],"other-phase":["helloapi/handler.go","helloapi/handler_test.go"]}`,
		// Not valid JSON at all
		`scaffold-phase: helloapi/go.mod`,
		// Empty response
		``,
	}

	for i, content := range badMappings {
		t.Run(fmt.Sprintf("reject-%d", i), func(t *testing.T) {
			assigned, ok := runLLMAssignment(t, "/tmp/gt-town", content, phases, files)
			if ok {
				t.Errorf("expected invalid assignment to be rejected, got ok=true")
			}
			// Even on rejection, no phase may gain files that don't belong.
			for _, p := range assigned {
				for _, f := range p.RequiredFiles {
					found := false
					for _, want := range files {
						if f == want {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("rejected result contains hallucinated file %q in phase %q", f, p.ID)
					}
				}
			}
		})
	}
}
