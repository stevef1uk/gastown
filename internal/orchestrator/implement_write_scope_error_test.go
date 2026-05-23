package orchestrator

import (
	"strings"
	"testing"
)

func TestNewImplementWriteScopeError_wrongBeadHint(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "mockrig"
	v := DefaultWorkflowValidation()
	v.LayoutRoot = "linkshelf"
	v.BeadTitleContains = "Implement linkshelf/"
	v.RequiredFiles = []string{
		"linkshelf/internal/store/schema.go",
		"linkshelf/internal/api/handlers_test.go",
	}
	setListImplementBeadsByStatusHook(t, dir, rig, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "open" || status == "in_progress" {
			return []PlanBead{
				{ID: "te-8cz", Title: "Implement linkshelf/internal/store/schema.go per architecture"},
				{ID: "te-rnd", Title: "Implement linkshelf/internal/api/handlers_test.go per architecture"},
			}, nil
		}
		return nil, nil
	})

	err := NewImplementWriteScopeError(dir, rig, "te-8cz", "linkshelf/internal/store/schema.go",
		"linkshelf/internal/api/handlers_test.go", v)
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	for _, want := range []string{"te-rnd", "te-8cz", "handlers_test.go", "bd update te-rnd"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("missing %q in %q", want, msg)
		}
	}
}
