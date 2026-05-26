package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeMinimalGoModule(t *testing.T, rigDir string) {
	t.Helper()
	layout := filepath.Join(rigDir, "app")
	storeDir := filepath.Join(layout, "internal", "store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(layout, "go.mod"), []byte("module app\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "store.go"), []byte("package store\n\nfunc List() int { return 0 }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "store_test.go"), []byte("package store\n\nimport \"testing\"\n\nfunc TestList(t *testing.T) {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestCloseImplementBeadsWithGreenGoVerify_profileOrder(t *testing.T) {
	dir := t.TempDir()
	rig := "rig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	writeMinimalGoModule(t, rigDir)

	v := WorkflowValidation{
		LayoutRoot:        "app",
		BeadTitleContains: "Implement",
		QAVerifyCommand:   "cd app && go test ./...",
		RequiredFiles: []string{
			"app/go.mod",
			"app/internal/store/store.go",
			"app/internal/store/store_test.go",
		},
		MinImplementationFileBytes: 1,
		MinSubstantiveLines:        1,
	}

	var closed []string
	bdCloseImplementBeadHook = func(_, _, id string) error {
		closed = append(closed, id)
		return nil
	}
	defer func() { bdCloseImplementBeadHook = nil }()

	prev := ListImplementBeadsByStatusHook
	ListImplementBeadsByStatusHook = func(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error) {
		switch status {
		case "open":
			return []PlanBead{
				{ID: "b-store", Title: "Implement app/internal/store/store.go per architecture"},
				{ID: "b-test", Title: "Implement app/internal/store/store_test.go per architecture"},
			}, nil
		default:
			return nil, nil
		}
	}
	defer func() { ListImplementBeadsByStatusHook = prev }()

	eval := newImplementBeadVerifyEvaluator(rigDir, v)
	got, err := CloseImplementBeadsWithGreenGoVerify(dir, rig, v, eval)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("closed=%v want b-store and b-test", got)
	}
	if got[0] != "b-store" || got[1] != "b-test" {
		t.Fatalf("profile order: got %v", got)
	}
}

func TestReopenClosedImplementBeadsOrdered_skipsGreenVerify(t *testing.T) {
	dir := t.TempDir()
	rig := "rig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	writeMinimalGoModule(t, rigDir)

	v := WorkflowValidation{
		LayoutRoot:        "app",
		BeadTitleContains: "Implement",
		QAVerifyCommand:   "cd app && go test ./...",
		RequiredFiles:     []string{"app/internal/store/store.go"},
		MinImplementationFileBytes: 1,
		MinSubstantiveLines:        1,
	}

	prev := ListImplementBeadsByStatusHook
	ListImplementBeadsByStatusHook = func(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "closed" {
			return []PlanBead{{ID: "b1", Title: "Implement app/internal/store/store.go per architecture"}}, nil
		}
		return nil, nil
	}
	defer func() { ListImplementBeadsByStatusHook = prev }()

	eval := newImplementBeadVerifyEvaluator(rigDir, v)
	reopened, err := reopenClosedImplementBeadsOrdered(dir, rig, v, eval)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened) != 0 {
		t.Fatalf("reopened=%v want none when go test passes", reopened)
	}
}

func TestImplementBeadVerifyEvaluator_memoizes(t *testing.T) {
	dir := t.TempDir()
	rigDir := dir
	writeMinimalGoModule(t, rigDir)
	v := WorkflowValidation{
		LayoutRoot:        "app",
		QAVerifyCommand:   "cd app && go test ./...",
		RequiredFiles:     []string{"app/internal/store/store.go"},
		MinImplementationFileBytes: 1,
		MinSubstantiveLines:        1,
	}
	eval := newImplementBeadVerifyEvaluator(rigDir, v)
	p := "app/internal/store/store.go"
	if !eval.GoSatisfied(p) {
		t.Skip("go toolchain required")
	}
	if !eval.GoSatisfied(p) {
		t.Fatal("second call should hit memo")
	}
	if len(eval.memo) != 1 {
		t.Fatalf("memo=%d want 1 verify run per path", len(eval.memo))
	}
}

func TestReconcileImplementBeads_deterministicLogOrder(t *testing.T) {
	dir := t.TempDir()
	rig := "rig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	writeMinimalGoModule(t, rigDir)

	v := WorkflowValidation{
		LayoutRoot:        "app",
		BeadTitleContains: "Implement",
		QAVerifyCommand:   "cd app && go test ./...",
		RequiredFiles:     []string{"app/internal/store/store.go"},
		MinImplementationFileBytes: 1,
		MinSubstantiveLines:        1,
	}

	prev := ListImplementBeadsByStatusHook
	ListImplementBeadsByStatusHook = func(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "open" {
			return []PlanBead{{ID: "b1", Title: "Implement app/internal/store/store.go per architecture"}}, nil
		}
		return nil, nil
	}
	defer func() { ListImplementBeadsByStatusHook = prev }()

	var closed []string
	bdCloseImplementBeadHook = func(_, _, id string) error {
		closed = append(closed, id)
		return nil
	}
	defer func() { bdCloseImplementBeadHook = nil }()

	log, err := ReconcileImplementBeads(dir, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(log, "auto-closed (verify green):") {
		t.Fatalf("close must run before reopen/audit issues: %q", log)
	}
	if len(closed) != 1 || closed[0] != "b1" {
		t.Fatalf("closed=%v", closed)
	}
}
