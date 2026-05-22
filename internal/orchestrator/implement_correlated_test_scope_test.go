package orchestrator

import (
	"strings"
	"testing"
)

func TestCorrelatedTestPathForSource_schemaBead(t *testing.T) {
	t.Parallel()
	got := CorrelatedTestPathForSource("linkshelf/internal/store/schema.go", "linkshelf")
	want := "linkshelf/internal/store/schema_test.go"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestValidateImplementReadPath_correlatedGoTest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "mockrig"
	v := DefaultWorkflowValidation()
	v.LayoutRoot = "linkshelf"
	v.QAVerifyCommand = "cd linkshelf && go test ./..."
	v.RequiredFiles = []string{"linkshelf/internal/store/schema.go"}
	ListImplementBeadsByStatusHook = func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "in_progress" {
			return []PlanBead{{
				ID:    "te-phq",
				Title: "Implement linkshelf/internal/store/schema.go per architecture",
			}}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { ListImplementBeadsByStatusHook = nil })

	testPath := "linkshelf/internal/store/schema_test.go"
	if err := ValidateImplementReadPath(dir, rig, "te-phq", testPath, v); err != nil {
		t.Fatalf("read correlated test: %v", err)
	}
	if err := ValidateImplementReadPath(dir, rig, "te-phq", "linkshelf/internal/store/store.go", v); err == nil {
		t.Fatal("expected read reject for store.go while on schema bead")
	}
}

func TestValidateImplementWritePath_correlatedGoTest_table(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	v := DefaultWorkflowValidation()
	v.LayoutRoot = "linkshelf"
	v.QAVerifyCommand = "cd linkshelf && go test ./..."
	v.RequiredFiles = []string{
		"linkshelf/internal/store/schema.go",
		"linkshelf/internal/store/store.go",
	}
	setActive := func(id, title string) {
		ListImplementBeadsByStatusHook = func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
			if status == "open" || status == "in_progress" {
				return []PlanBead{{ID: id, Title: title}}, nil
			}
			return nil, nil
		}
	}
	t.Cleanup(func() { ListImplementBeadsByStatusHook = nil })

	cases := []struct {
		beadID   string
		title    string
		write    string
		full     bool
		wantFail bool
	}{
		{
			beadID: "te-phq",
			title:  "Implement linkshelf/internal/store/schema.go per architecture",
			write:  "linkshelf/internal/store/schema_test.go",
			full:   true,
		},
		{
			beadID: "te-phq",
			title:  "Implement linkshelf/internal/store/schema.go per architecture",
			write:  "linkshelf/internal/store/schema.go",
			full:   false,
		},
		{
			beadID:   "te-phq",
			title:    "Implement linkshelf/internal/store/schema.go per architecture",
			write:    "linkshelf/internal/store/store.go",
			full:     true,
			wantFail: true,
		},
		{
			beadID:   "te-phq",
			title:    "Implement linkshelf/internal/store/schema.go per architecture",
			write:    "linkshelf/internal/store/store_test.go",
			full:     true,
			wantFail: true,
		},
		{
			beadID: "te-store",
			title:  "Implement linkshelf/internal/store/store.go per architecture",
			write:  "linkshelf/internal/store/store_test.go",
			full:   true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.write+"_"+tc.beadID, func(t *testing.T) {
			setActive(tc.beadID, tc.title)
			err := ValidateImplementWritePath(dir, rig, tc.beadID, tc.write, v, tc.full)
			if tc.wantFail {
				if err == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(err.Error(), "write only") {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected reject: %v", err)
			}
		})
	}
}
