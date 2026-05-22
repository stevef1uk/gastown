package orchestrator

import (
	"strings"
	"testing"
)

func TestCompileErrorPathsIncludingClosedDeps_addsEarlierClosedGoFile(t *testing.T) {
	v := WorkflowValidation{
		BeadTitleContains: "Implement linkshelf/",
		LayoutRoot:        "linkshelf",
		RequiredFiles: []string{
			"linkshelf/internal/store/store.go",
			"linkshelf/internal/api/handlers.go",
			"linkshelf/cmd/server/main.go",
		},
	}
	setListImplementBeadsByStatusHook(t, "", "", func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		switch status {
		case "in_progress":
			return []PlanBead{{ID: "te-main", Title: "Implement linkshelf/cmd/server/main.go per architecture"}}, nil
		case "closed":
			return []PlanBead{{ID: "te-h", Title: "Implement linkshelf/internal/api/handlers.go per architecture"}}, nil
		default:
			return nil, nil
		}
	})

	out := strings.Join(CompileErrorPathsIncludingClosedDeps("", "", "linkshelf/cmd/server/main.go", nil,
		"linkshelf/cmd/server/main.go:42:9: undefined: api.GetLinks", v), ",")
	if !strings.Contains(out, "handlers.go") {
		t.Fatalf("want handlers.go in paths, got %q", out)
	}
}
