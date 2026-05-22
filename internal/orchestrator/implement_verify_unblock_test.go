package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAllowClosedDepFixForVerifyFailure_storeTestBead(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig-store-unblock"
	storeDir := filepath.Join(dir, rig, "mayor", "rig", "linkshelf", "internal", "store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	storeGo := `package store

import "context"

func (s *Store) List(ctx context.Context) ([]Link, error) {
	var links []Link
	return links, nil
}
`
	if err := os.WriteFile(filepath.Join(storeDir, "store.go"), []byte(storeGo), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:        "linkshelf",
		TestRunner:        "go",
		BeadTitleContains: "Implement linkshelf/",
		RequiredFiles: []string{
			"linkshelf/internal/store/store.go",
			"linkshelf/internal/store/store_test.go",
		},
	}
	setListImplementBeadsByStatusHook(t, dir, rig, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		switch status {
		case "closed":
			return []PlanBead{{ID: "te-uam", Title: "Implement linkshelf/internal/store/store.go per architecture"}}, nil
		case "in_progress":
			return []PlanBead{{ID: "te-thd", Title: "Implement linkshelf/internal/store/store_test.go per architecture"}}, nil
		default:
			return nil, nil
		}
	})

	active := "linkshelf/internal/store/store_test.go"
	written := "linkshelf/internal/store/store.go"
	out := "store_test.go:31: List returned nil slice, want empty slice\n"
	if !AllowClosedDepFixForVerifyFailure(dir, rig, active, written, out, v) {
		t.Fatal("expected allow EDIT on closed store.go from test bead")
	}
	if err := ValidateImplementWritePath(dir, rig, "te-thd", written, v, false, out); err != nil {
		t.Fatalf("ValidateImplementWritePath: %v", err)
	}
	hint := FormatNilSliceListUnblockHint(dir, rig, active, v)
	if !strings.Contains(hint, "te-uam") || !strings.Contains(hint, "EDIT:") {
		t.Fatalf("hint = %q", hint)
	}
}
