package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInferDefaultDeliveryPhases_linkshelfProfile(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		LayoutRoot:      "linkshelf",
		TestRunner:      "go",
		QAVerifyCommand: "cd linkshelf && go mod tidy && go test ./...",
		RequiredFiles: []string{
			"linkshelf/go.mod",
			"linkshelf/internal/store/schema.go",
			"linkshelf/internal/store/store.go",
			"linkshelf/internal/api/handlers.go",
			"linkshelf/cmd/server/main.go",
			"linkshelf/web/style.css",
			"linkshelf/web/app.js",
			"linkshelf/web/index.html",
		},
	}
	got := FinalizeDeliveryPhases(v)
	if !got.HasPhasedDelivery() {
		t.Fatal("expected inferred delivery phases")
	}
	if got.ActivePhaseID() != "go-module" {
		t.Fatalf("active phase = %q want go-module", got.ActivePhaseID())
	}
	if len(got.DeliveryPhases) < 4 {
		t.Fatalf("phases = %d", len(got.DeliveryPhases))
	}
	scoped := got.ForActivePhase()
	if len(scoped.RequiredFiles) != 1 || scoped.RequiredFiles[0] != "linkshelf/go.mod" {
		t.Fatalf("scoped = %v", scoped.RequiredFiles)
	}
}

func TestOrderRequiredFilesForImplementation_webIndexAfterAssets(t *testing.T) {
	t.Parallel()
	files := []string{
		"linkshelf/web/index.html",
		"linkshelf/web/app.js",
		"linkshelf/web/style.css",
	}
	got := OrderRequiredFilesForImplementation(files)
	want := []string{
		"linkshelf/web/style.css",
		"linkshelf/web/app.js",
		"linkshelf/web/index.html",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestValidateHTTPContractSplit_defersMissingSiblingStatic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "mockrig"
	v := linkshelfHTTPProfile()
	writeHTTPContractFixture(t, dir, rig,
		`<html><head><script src="/static/app.js"></script></head><body></body></html>`,
		`package api
import "net/http"
func Mount(mux *http.ServeMux, webRoot string) {}
`)
	rigDir := filepath.Join(dir, rig, "mayor", "rig", "linkshelf", "web")
	appJS := filepath.Join(rigDir, "app.js")
	if err := os.Remove(appJS); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	setListImplementBeadsByStatusHook(t, dir, rig, func(_, _ string, _ WorkflowValidation, status string) ([]PlanBead, error) {
		if status == "open" || status == "in_progress" {
			return []PlanBead{
				{ID: "te-js", Title: "Implement linkshelf/web/app.js per architecture"},
			}, nil
		}
		return nil, nil
	})
	blocking, warnings, err := ValidateHTTPContractSplit(dir, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocking) != 0 {
		t.Fatalf("blocking = %v", blocking)
	}
	if len(warnings) == 0 {
		t.Fatal("expected deferred warning for open app.js bead")
	}
	if !strings.Contains(warnings[0], "app.js") {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestStaticRefExistsOnDisk_layoutFallback(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "mockrig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	if err := os.MkdirAll(filepath.Join(rigDir, "linkshelf", "web"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "linkshelf", "web", "style.css"), []byte("body{}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{LayoutRoot: "linkshelf"}
	mapping := WebStaticMapping{StaticURLPrefix: "/static"}
	webRoot := webRootDir(rigDir, "linkshelf/web/index.html", v)
	if !staticRefExistsOnDisk(rigDir, v, mapping, webRoot, "linkshelf/web/index.html", "/static/style.css") {
		t.Fatal("expected layout fallback stat to find style.css")
	}
}
