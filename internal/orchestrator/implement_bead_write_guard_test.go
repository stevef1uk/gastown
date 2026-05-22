package orchestrator

import "testing"

func TestPathMatchesImplementFile(t *testing.T) {
	t.Parallel()
	cases := []struct {
		written, bead string
		want          bool
	}{
		{"linkshelf/internal/api/handlers.go", "linkshelf/internal/api/handlers.go", true},
		{"linkshelf/cmd/server/main.go", "linkshelf/internal/api/handlers.go", false},
		{"handlers.go", "linkshelf/internal/api/handlers.go", true},
	}
	for _, tc := range cases {
		if got := PathMatchesImplementFile(tc.written, tc.bead); got != tc.want {
			t.Fatalf("PathMatchesImplementFile(%q,%q)=%v want %v", tc.written, tc.bead, got, tc.want)
		}
	}
}

func TestAllowedEarlierImplementDependencyWrite_rejectsClosedPath(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:        "linkshelf",
		BeadTitleContains: "Implement ",
		RequiredFiles: []string{
			"linkshelf/internal/api/handlers.go",
			"linkshelf/cmd/server/main.go",
		},
	}
	active := "linkshelf/cmd/server/main.go"
	written := "linkshelf/internal/api/handlers.go"
	setListImplementBeadsByStatusHook(t, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "closed" {
			return []PlanBead{{ID: "te-h", Title: "Implement linkshelf/internal/api/handlers.go per architecture"}}, nil
		}
		return nil, nil
	})
	if AllowedEarlierImplementDependencyWrite("", "", active, written, v) {
		t.Fatal("expected false when handlers path is closed-only")
	}
}
