package orchestrator

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCompleteTask_slowTriadDoesNotBlockTransitionOrStatus(t *testing.T) {
	town := t.TempDir()
	rig := "mockrig"
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	v := WorkflowValidation{
		LayoutRoot: "sample", RequiredFiles: []string{"sample/item.txt"},
		BeadTitleContains: "Implement sample/", MinPlanBytes: 100,
	}
	writeTestRigProfile(t, town, rig, v)
	padding := strings.Repeat("Documented acceptance criteria. ", 12)
	for name, body := range map[string]string{
		"SPEC.md":         "# Specification\n" + padding,
		"architecture.md": "# Architecture\n- sample/item.txt\n" + padding,
		"plan.md":         "# Plan\n## Bead map\n### mo-1: sample/item.txt\n- Scope: original snapshot\n" + padding,
	} {
		if err := os.WriteFile(filepath.Join(rigDir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	setListImplementBeadsByStatusHook(t, town, rig, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "open" {
			return []PlanBead{{ID: "mo-1", Title: "Implement sample/item.txt per architecture"}}, nil
		}
		return nil, nil
	})

	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var requests atomic.Int32
	requestBodies := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		started <- struct{}{}
		<-release
		body, _ := io.ReadAll(r.Body)
		requestBodies <- string(body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{
				"role": "assistant", "content": `{"pass":false,"reason":"snapshot advisory finding"}`,
			}}},
		})
	}))
	defer server.Close()
	defer close(release)
	t.Setenv("GT_JUDGE_ENDPOINT", server.URL)
	t.Setenv("GT_JUDGE_MODEL", "test-judge")
	t.Setenv("GT_SKIP_WORKFLOW_GIT_COMMIT", "1")

	m := NewManager(town)
	m.LoadTemplate(&WorkflowTemplate{
		ID: "rig-flow", InitialState: "planning",
		States: map[string]State{
			"planning":    {Role: "planner", Transitions: map[string]Transition{"success": {To: "plan_review"}}},
			"plan_review": {Role: "qa"},
		},
	})
	id, err := m.StartWorkflow("rig-flow", map[string]string{"rig": rig})
	if err != nil {
		t.Fatal(err)
	}
	completed := make(chan error, 1)
	go func() {
		next, err := m.CompleteTask(id, "success", rig+"/planner", "ready", "", nil)
		if err == nil && next != "plan_review" {
			err = fmt.Errorf("unexpected next state: %s", next)
		}
		completed <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("triad judge did not start")
	}
	select {
	case err := <-completed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("completion blocked on advisory judge")
	}
	status := make(chan error, 1)
	go func() {
		_, err := m.GetWorkflowStatus(id)
		status <- err
	}()
	select {
	case err := <-status:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("status read blocked on advisory judge")
	}
	if err := ValidatePlanningDocAlignment(town, rigDir, v, "plan_review"); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatal("identical in-flight snapshot was submitted twice")
	}
	if err := os.WriteFile(filepath.Join(rigDir, "plan.md"), []byte("changed after transition"), 0644); err != nil {
		t.Fatal(err)
	}

	release <- struct{}{}
	deadline := time.Now().Add(5 * time.Second)
	for {
		body, _ := os.ReadFile(filepath.Join(town, "logs", "town.log"))
		if strings.Contains(string(body), "snapshot advisory finding") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background advisory was not recorded")
		}
		time.Sleep(10 * time.Millisecond)
	}
	server.Close()
	requestBody := <-requestBodies
	if !strings.Contains(requestBody, "original snapshot") || strings.Contains(requestBody, "changed after transition") {
		t.Fatalf("judge did not use the validated document snapshot: %s", requestBody)
	}
	statuses, err := m.GetWorkflowStatus(id)
	if err != nil || len(statuses) != 1 || statuses[0].CurrentState != "plan_review" {
		t.Fatalf("advisory changed workflow status: %+v, %v", statuses, err)
	}
}

func TestValidatePlanningDocAlignment_structuralFailureSkipsTriad(t *testing.T) {
	dir := t.TempDir()
	padding := strings.Repeat("Documented acceptance criteria. ", 12)
	for name, body := range map[string]string{
		"SPEC.md":         "module sample\n" + padding,
		"architecture.md": "module sample\n" + padding,
		"plan.md":         "module github.com/example/sample\n" + padding,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	t.Setenv("GT_JUDGE_ENDPOINT", server.URL)
	err := ValidatePlanningDocAlignment("", dir, WorkflowValidation{LayoutRoot: "sample"}, "planning")
	if err == nil || !strings.Contains(err.Error(), "misaligned") {
		t.Fatalf("expected structural alignment failure, got %v", err)
	}
	if requests.Load() != 0 {
		t.Fatal("structurally invalid documents were sent to the advisory judge")
	}
}
