package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateImplementWritePath_rejectsWhenQueueGreen(t *testing.T) {
	dir := t.TempDir()
	rig := "rig"
	layout := "app"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	moduleDir := filepath.Join(rigDir, layout)
	storeDir := filepath.Join(moduleDir, "internal", "store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		rel, body string
	}{
		{layout + "/go.mod", "module app\n\ngo 1.22\n"},
		{layout + "/internal/store/store.go", "package store\n\nvar DB interface{}\n"},
	} {
		path := filepath.Join(rigDir, filepath.FromSlash(item.rel))
		if err := os.WriteFile(path, []byte(item.body), 0644); err != nil {
			t.Fatal(err)
		}
	}

	v := WorkflowValidation{
		LayoutRoot:        layout,
		BeadTitleContains: "Implement",
		QAVerifyCommand:   "cd app && go test ./...",
		RequiredFiles: []string{
			layout + "/internal/store/store.go",
			layout + "/cmd/server/main.go",
		},
		MinImplementationFileBytes: 1,
		MinSubstantiveLines:        1,
	}

	prev := ListImplementBeadsByStatusHook
	defer func() { ListImplementBeadsByStatusHook = prev }()
	ListImplementBeadsByStatusHook = func(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error) {
		return nil, nil
	}

	if !ImplementationQueueGreen(dir, rig, v) {
		t.Skip("module tests not green in temp rig (go toolchain)")
	}
	err := ValidateImplementWritePath(dir, rig, "", layout+"/cmd/server/main.go", v, false, "", nil)
	if err == nil {
		t.Fatal("expected reject when queue green")
	}
	if !strings.Contains(err.Error(), "success") || !strings.Contains(err.Error(), "EDIT/WRITE") {
		t.Fatalf("err=%v", err)
	}
}
