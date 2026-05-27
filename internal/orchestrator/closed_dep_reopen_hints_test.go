package orchestrator

import (
	"strings"
	"testing"
)

func TestProductionGoPathsFromRequired(t *testing.T) {
	t.Parallel()
	required := []string{
		"app/go.mod",
		"app/internal/pkg/schema.go",
		"app/internal/pkg/widget.go",
		"app/internal/pkg/widget_test.go",
		"app/cmd/app/main.go",
	}
	got := ProductionGoPathsFromRequired(required)
	want := []string{"app/go.mod", "app/internal/pkg/schema.go", "app/internal/pkg/widget.go", "app/cmd/app/main.go"}
	// go.mod is not .go suffix - should skip
	want = []string{"app/internal/pkg/schema.go", "app/internal/pkg/widget.go", "app/cmd/app/main.go"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestFormatClosedDependencyCompileHints_skipsEarlierClosedWhenTestOnlyErrors(t *testing.T) {
	t.Parallel()
	layout := "app"
	v := WorkflowValidation{
		BeadTitleContains: "Implement " + layout + "/",
		LayoutRoot:        layout,
		RequiredFiles: []string{
			layout + "/internal/pkg/ddl.go",
			layout + "/internal/pkg/widget.go",
			layout + "/internal/pkg/widget_test.go",
		},
	}
	closedID := "bead-ddl"
	setListImplementBeadsByStatusHook(t, "", "", func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "closed" {
			return []PlanBead{{ID: closedID, Title: "Implement " + layout + "/internal/pkg/ddl.go per architecture"}}, nil
		}
		return nil, nil
	})

	out := `internal/pkg/widget_test.go:20:8: undefined: widgetType
FAIL	` + layout + `/internal/pkg [build failed]
FAIL`
	active := layout + "/internal/pkg/widget.go"
	got := FormatClosedDependencyCompileHints("", "", active,
		[]string{layout + "/internal/pkg/ddl.go", layout + "/internal/pkg/widget_test.go"}, out, v)
	if strings.Contains(got, closedID) || strings.Contains(got, "ddl.go") {
		t.Fatalf("should not suggest reopening earlier closed file for test-only errors, got %q", got)
	}
	if hint := FormatSamePackageTestAPIHint(active, "", out, v); hint == "" || !strings.Contains(hint, "Do **not** reopen") {
		t.Fatalf("want same-package test hint, got %q", hint)
	}
}

func TestFormatClosedDependencyCompileHints_keepsCrossPackageUndefined(t *testing.T) {
	layout := "app"
	v := WorkflowValidation{
		BeadTitleContains: "Implement " + layout + "/",
		LayoutRoot:        layout,
		RequiredFiles: []string{
			layout + "/internal/api/handlers.go",
			layout + "/cmd/server/main.go",
		},
	}
	closedID := "bead-api"
	setListImplementBeadsByStatusHook(t, "", "", func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "closed" {
			return []PlanBead{{ID: closedID, Title: "Implement " + layout + "/internal/api/handlers.go per architecture"}}, nil
		}
		return nil, nil
	})

	out := layout + "/cmd/server/main.go:42:9: undefined: api.GetItems"
	got := FormatClosedDependencyCompileHints("", "", layout+"/cmd/server/main.go",
		[]string{layout + "/internal/api/handlers.go"}, out, v)
	if !strings.Contains(got, closedID) {
		t.Fatalf("want handlers reopen hint, got %q", got)
	}
}
