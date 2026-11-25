package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpecConsistencyEndToEndWorkflow(t *testing.T) {
	townRoot := t.TempDir()

	tpl := loadRigFlowTemplate(t)

	mgr := NewManager(townRoot)
	mgr.LoadTemplate(tpl)

	rigName := "test-spec-rig"
	rigDir := filepath.Join(townRoot, rigName, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create initial inconsistent SPEC.md
	initialSpec := `# System Specification
## 1. Overview
Sample application.

## 2. Backend Service
Backend FastAPI server runs on port 8080:
` + "```" + `
uvicorn main:app --host 0.0.0.0 --port 8080
` + "```" + `

## 3. Frontend Client
Frontend client connects to backend on port 3000:
NEXT_PUBLIC_API_URL=http://localhost:3000
`
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(initialSpec), 0644); err != nil {
		t.Fatal(err)
	}

	wfID, err := mgr.StartWorkflow("rig-flow", map[string]string{"rig": rigName})
	if err != nil {
		t.Fatalf("StartWorkflow: %v", err)
	}

	inst := mgr.instances[wfID]
	if inst.CurrentState != "kickoff" {
		t.Fatalf("initial state = %q, want kickoff", inst.CurrentState)
	}

	// Step 1: Mayor completes kickoff -> transitions to spec_analysis
	next, err := mgr.CompleteTask(wfID, "success", rigName+"/mayor", "SPEC.md validated present", "", nil)
	if err != nil {
		t.Fatalf("CompleteTask kickoff: %v", err)
	}
	if next != "spec_analysis" {
		t.Fatalf("after kickoff next = %q, want spec_analysis", next)
	}

	// Step 2: Analyst completes spec_analysis with findings -> transitions to spec_review
	analystFindings := "Found port mismatch: backend on 8080, frontend connects to 3000. Recommend changing frontend to 8080."
	next, err = mgr.CompleteTask(wfID, "success", rigName+"/analyst", analystFindings, "", nil)
	if err != nil {
		t.Fatalf("CompleteTask spec_analysis: %v", err)
	}
	if next != "spec_review" {
		t.Fatalf("after spec_analysis next = %q, want spec_review", next)
	}

	// Verify analyst findings passed to spec_review
	if pending := mgr.instances[wfID].PendingRework; pending == nil || !strings.Contains(pending.Summary, "Found port mismatch") {
		t.Fatalf("expected pending rework with analyst findings, got: %+v", pending)
	}
	// Verify audit log was recorded on disk
	auditData, err := os.ReadFile(filepath.Join(rigDir, "SPEC_AUDIT.md"))
	if err != nil || !strings.Contains(string(auditData), "Found port mismatch") {
		t.Fatalf("expected SPEC_AUDIT.md with findings, got err=%v content=%s", err, string(auditData))
	}

	// Step 3: QA rejects first attempt (testing rework cycle) -> transitions back to spec_analysis
	qaRejection := "Need exact line numbers and diff patch for the port change."
	next, err = mgr.CompleteTask(wfID, "failure", rigName+"/qa", qaRejection, "", nil)
	if err != nil {
		t.Fatalf("CompleteTask spec_review failure: %v", err)
	}
	if next != "spec_analysis" {
		t.Fatalf("after spec_review failure next = %q, want spec_analysis", next)
	}

	// Verify rework feedback was delivered to analyst
	rework := mgr.instances[wfID].PendingRework
	if rework == nil {
		t.Fatal("expected pending rework feedback for analyst")
	}
	if !strings.Contains(rework.Feedback, "QA rejected SPEC consistency findings") || !strings.Contains(rework.Feedback, "exact line numbers") {
		t.Fatalf("unexpected rework feedback: %s", rework.Feedback)
	}

	// Step 4: Analyst provides precise fix -> transitions to spec_review
	analystFixedFindings := "Line 12: change NEXT_PUBLIC_API_URL=http://localhost:3000 to http://localhost:8080."
	next, err = mgr.CompleteTask(wfID, "success", rigName+"/analyst", analystFixedFindings, "", nil)
	if err != nil {
		t.Fatalf("CompleteTask spec_analysis rework: %v", err)
	}
	if next != "spec_review" {
		t.Fatalf("after spec_analysis rework next = %q, want spec_review", next)
	}

	// QA applies the fix to SPEC.md and generates workflow profile
	patchedSpec := strings.ReplaceAll(initialSpec, "3000", "8080")
	if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(patchedSpec), 0644); err != nil {
		t.Fatal(err)
	}
	gastownDir := filepath.Join(rigDir, ".gastown")
	if err := os.MkdirAll(gastownDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gastownDir, "workflow-profile.json"), []byte(`{"layout_root":"."}`), 0644); err != nil {
		t.Fatal(err)
	}

	// Step 5: QA completes spec_review -> transitions to design!
	next, err = mgr.CompleteTask(wfID, "success", rigName+"/qa", "Verified and patched SPEC.md frontend port to 8080.", "", nil)
	if err != nil {
		t.Fatalf("CompleteTask spec_review success: %v", err)
	}
	if next != "design" {
		t.Fatalf("after spec_review success next = %q, want design", next)
	}

	// Confirm final state is design
	if mgr.instances[wfID].CurrentState != "design" {
		t.Fatalf("final state = %q, want design", mgr.instances[wfID].CurrentState)
	}
}
