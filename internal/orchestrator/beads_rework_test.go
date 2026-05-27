package orchestrator

import (
	"strings"
	"testing"
)

func TestReopenClosedImplementBeadsForIDs(t *testing.T) {
	dir := t.TempDir()
	rig := "rig"
	v := WorkflowValidation{
		LayoutRoot:        "app",
		BeadTitleContains: "Implement app/",
		RequiredFiles:     []string{"app/web/index.html", "app/web/app.js"},
	}

	var reopened []string
	prevList := ListImplementBeadsByStatusHook
	ListImplementBeadsByStatusHook = func(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error) {
		if status != "closed" {
			return nil, nil
		}
		return []PlanBead{
			{ID: "xy-a3c", Title: "Implement app/web/index.html per architecture"},
			{ID: "xy-0l6", Title: "Implement app/web/app.js per architecture"},
			{ID: "xy-e4d", Title: "Implement app/internal/api/handlers.go"},
		}, nil
	}
	defer func() { ListImplementBeadsByStatusHook = prevList }()

	prevUpdate := bdUpdateImplementBeadStatusHook
	bdUpdateImplementBeadStatusHook = func(townRoot, rig, id, status string) error {
		if status == "open" {
			reopened = append(reopened, id)
		}
		return nil
	}
	defer func() { bdUpdateImplementBeadStatusHook = prevUpdate }()

	got, err := reopenClosedImplementBeadsForIDs(dir, rig, v, []string{"xy-a3c", "xy-0l6"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || reopened[0] != "xy-a3c" || reopened[1] != "xy-0l6" {
		t.Fatalf("reopened=%v", reopened)
	}
}

func TestQAFailureRequiresImplementationRework_domMismatch(t *testing.T) {
	summary := "Beads te-0l6 and te-a3c have inconsistent DOM element IDs, causing the UI to break"
	if !qaFailureRequiresImplementationRework(summary) {
		t.Fatal("DOM/UI mismatch should trigger implementation rework")
	}
}

func TestExtractKnownRigBeadIDsFromSummary_usesRigPrefix(t *testing.T) {
	known := map[string]bool{"te-0l6": true, "te-a3c": true}
	summary := `app.js (bead te-0l6) references id "link-list" but index.html (te-a3c) defines "links"`
	got := ExtractKnownRigBeadIDsFromSummary(summary, "te", known)
	if len(got) != 2 {
		t.Fatalf("got %v want te-0l6 and te-a3c", got)
	}
	if strings.Contains(strings.Join(got, ","), "link-list") {
		t.Fatalf("must not treat link-list as bead id: %v", got)
	}
}
