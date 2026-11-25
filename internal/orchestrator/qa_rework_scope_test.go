package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAllowedQAReworkWebImplementWrite_crossBead(t *testing.T) {
	dir := t.TempDir()
	rig := "rig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	webDir := filepath.Join(rigDir, "linkshelf", "web")
	if err := os.MkdirAll(webDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<html><ul id=\"links\"></ul></html>\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(webDir, "app.js"), []byte("const linkList = document.getElementById('links');\n"), 0644); err != nil {
		t.Fatal(err)
	}

	v := WorkflowValidation{
		LayoutRoot:        "linkshelf",
		BeadTitleContains: "Implement linkshelf/",
		RequiredFiles: []string{
			"linkshelf/web/index.html",
			"linkshelf/web/app.js",
		},
		MinImplementationFileBytes: 10,
		MinSubstantiveLines:        1,
	}

	prevList := ListImplementBeadsByStatusHook
	ListImplementBeadsByStatusHook = func(townRoot, rig string, v WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "closed" {
			return []PlanBead{
				{ID: "ab-0l6", Title: "Implement linkshelf/web/app.js per architecture"},
			}, nil
		}
		if status == "open" {
			return []PlanBead{
				{ID: "ab-a3c", Title: "Implement linkshelf/web/index.html per architecture"},
			}, nil
		}
		return nil, nil
	}
	defer func() { ListImplementBeadsByStatusHook = prevList }()

	scope := ImplementWriteScope{
		QAReworkFromQAReview: true,
		QACitedBeadIDs: map[string]bool{
			"ab-a3c": true,
			"ab-0l6": true,
		},
	}
	activePath := "linkshelf/web/index.html"
	written := "linkshelf/web/app.js"
	if !AllowedQAReworkWebImplementWrite(dir, rig, "ab-a3c", activePath, written, scope, v) {
		t.Fatal("QA rework should allow editing cited closed app.js while on index bead")
	}
	if err := ValidateImplementWritePath(dir, rig, "ab-a3c", written, v, false, "", &scope); err != nil {
		t.Fatalf("ValidateImplementWritePath: %v", err)
	}
}

func TestAllowedQAReworkWebImplementWrite_citedPathPhasedDelivery(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot:        "taskboard",
		BeadTitleContains: "Implement taskboard/",
		RequiredFiles: []string{
			"taskboard/web/static/app.js",
			"taskboard/web/static/style.css",
			"taskboard/internal/web/app_test.go",
			"taskboard/internal/web/ui_content_test.go",
		},
		DeliveryPhases: []DeliveryPhase{
			{
				ID: "phase-1-app",
				RequiredFiles: []string{
					"taskboard/web/templates/index.html",
				},
			},
			{
				ID: "phase-2-kanban",
				RequiredFiles: []string{
					"taskboard/web/static/app.js",
					"taskboard/web/static/style.css",
					"taskboard/internal/web/app_test.go",
					"taskboard/internal/web/ui_content_test.go",
				},
			},
		},
		ActivePhaseIDField: "phase-2-kanban",
	}

	scope := ImplementWriteScope{
		QAReworkFromQAReview: true,
		QACitedPaths: map[string]bool{
			"taskboard/web/templates/index.html": true,
		},
	}

	if !AllowedQAReworkWebImplementWrite("/town", "tasktracker", "ta-kf2", "taskboard/internal/web/ui_content_test.go", "taskboard/web/templates/index.html", scope, v) {
		t.Fatal("QA rework should allow editing index.html from earlier phase when cited by path in QA findings")
	}
}
