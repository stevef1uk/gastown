package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/llm"
	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestExecuteOrchestratedTaskDefersMixedToolOutcomes(t *testing.T) {
	for _, tc := range []struct {
		outcome string
		turn    int
		native  bool
	}{
		{outcome: "timeout", turn: 1},
		{outcome: "failure", turn: 4},
		{outcome: "plan_gap", turn: 1},
		{outcome: "architecture_failure", turn: 1},
		{outcome: "success", turn: 1},
		{outcome: "timeout", turn: 1, native: true},
	} {
		t.Run(fmt.Sprintf("%s/turn%d/native%t", tc.outcome, tc.turn, tc.native), func(t *testing.T) {
			root := t.TempDir()
			workDir := filepath.Join(root, "mockrig", "mayor", "rig")
			if err := os.MkdirAll(workDir, 0755); err != nil {
				t.Fatal(err)
			}
			fixture := filepath.Join(workDir, "SPEC.md")
			if err := os.WriteFile(fixture, []byte("verification-evidence\n"), 0644); err != nil {
				t.Fatal(err)
			}
			tool := "CMD: cd mockrig/mayor/rig && cat SPEC.md"
			if tc.native {
				tool = "READ: SPEC.md"
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var req struct {
					Messages []llm.Message `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				response := tool
				if calls == tc.turn {
					response += fmt.Sprintf("\n{\"outcome\":%q,\"summary\":\"premature verdict\"}", tc.outcome)
				} else if calls > tc.turn {
					feedback := req.Messages[len(req.Messages)-1]
					if feedback.Role != "user" || !strings.Contains(feedback.Content, "verification-evidence") || !strings.Contains(feedback.Content, "Outcome ignored") {
						t.Errorf("next turn did not receive tool evidence and deferral feedback: %+v", feedback)
					}
					response = `{"outcome":"success","summary":"reviewed tool results"}`
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{
					"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": response}}},
				})
			}))
			defer server.Close()

			task := &orchestrator.Task{
				WorkflowID:      "wf-outcome-deferral",
				State:           "outcome-deferral",
				Rig:             "mockrig",
				AllowedOutcomes: []string{"success", "failure", "plan_gap", "architecture_failure", "timeout"},
				Hooks: orchestrator.StateHooks{
					MaxCmdTurns:     tc.turn + 1,
					NativeEditTools: tc.native,
				},
			}
			client := llm.NewClient(server.URL, "test-model", "tester", time.Second*5)
			outcome, summary, _, err := executeOrchestratedTask(context.Background(), client, root, "mockrig", "", task, nil)
			if err != nil {
				t.Fatal(err)
			}
			if calls != tc.turn+1 || outcome != "success" || summary != "reviewed tool results" {
				t.Fatalf("calls=%d outcome=%q summary=%q; want next-turn verdict", calls, outcome, summary)
			}
		})
	}
}
