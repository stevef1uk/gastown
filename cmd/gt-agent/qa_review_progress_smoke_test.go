package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func writeLinkshelfSmokeFingerprintFixture(t *testing.T, townRoot, rig string, indexBody string) {
	t.Helper()
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	writeLinkshelfArchitecture(t, rigDir, true)
	webDir := filepath.Join(rigDir, "linkshelf", "web")
	serverDir := filepath.Join(rigDir, "linkshelf", "cmd", "server")
	if err := os.MkdirAll(filepath.Join(rigDir, "linkshelf", "internal", "api"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(webDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(serverDir, 0755); err != nil {
		t.Fatal(err)
	}
	indexHTML := indexBody + "\n" + strings.Repeat("<!-- qa fixture padding -->\n<div class=\"panel\"></div>\n", 30)
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte(indexHTML), 0644); err != nil {
		t.Fatal(err)
	}
	appJS := strings.Repeat("document.addEventListener('DOMContentLoaded', function() { console.log('loaded'); });\n", 20)
	if err := os.WriteFile(filepath.Join(webDir, "app.js"), []byte(appJS), 0644); err != nil {
		t.Fatal(err)
	}
	mainGo := "package main\n\nimport \"net/http\"\n\nfunc main() {\n\thttp.HandleFunc(\"/\", func(w http.ResponseWriter, r *http.Request) {})\n\t_ = http.ListenAndServe(\":8080\", nil)\n}\n"
	if err := os.WriteFile(filepath.Join(serverDir, "main.go"), []byte(mainGo), 0644); err != nil {
		t.Fatal(err)
	}
	handlers := "package api\n\n" + strings.Repeat("// handler wiring for QA fixture\nfunc handlerN() {}\n", 8) + "func Routes() {}\n\nfunc Mount() {}\n"
	if err := os.WriteFile(filepath.Join(rigDir, "linkshelf", "internal", "api", "handlers.go"), []byte(handlers), 0644); err != nil {
		t.Fatal(err)
	}
}

func linkshelfSmokeValidationProfile() orchestrator.WorkflowValidation {
	v := linkshelfWebProfile()
	v.RequiredFiles = []string{
		"linkshelf/web/index.html",
		"linkshelf/web/app.js",
		"linkshelf/cmd/server/main.go",
		"linkshelf/internal/api/handlers.go",
	}
	return v
}

func TestQAReviewProgress_invalidateStaleRuntimeSmokeOnFileChange(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	v := linkshelfWebProfile()
	writeLinkshelfSmokeFingerprintFixture(t, dir, rig, "<html></html>")

	p := newQAReviewProgress("wf-fp", "qa_review", rig)
	p.mark(qaMilestoneRuntimeSmoke)
	p.SmokeSourceFingerprint = orchestrator.QASmokeSourceFingerprint(dir, rig, v)
	if err := saveQAReviewProgress(dir, rig, p); err != nil {
		t.Fatal(err)
	}

	writeLinkshelfSmokeFingerprintFixture(t, dir, rig, "<html><body>changed</body></html>")
	loaded := loadQAReviewProgress(dir, rig, "wf-fp", "qa_review")
	if loaded == nil {
		t.Fatal("expected progress file")
	}
	loaded.invalidateStaleRuntimeSmoke(dir, rig, v)
	if loaded.done(qaMilestoneRuntimeSmoke) {
		t.Fatal("runtime_smoke milestone should clear when handler/web fingerprint changes")
	}
}

func TestQAReviewProgress_staleProgressCannotAllPassWithoutSmoke(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	v := linkshelfSmokeValidationProfile()
	writeLinkshelfSmokeFingerprintFixture(t, dir, rig, "<html></html>")

	p := newQAReviewProgress("wf-stale", "qa_review", rig)
	p.mark(qaMilestoneRuntimeSmoke)
	p.mark(qaMilestoneClosedBeads)
	p.SmokeSourceFingerprint = orchestrator.QASmokeSourceFingerprint(dir, rig, v)
	if err := saveQAReviewProgress(dir, rig, p); err != nil {
		t.Fatal(err)
	}

	if err := validateQAArtifacts(dir, rig, "all_passed", false, true, true, false, false, v); err == nil || !strings.Contains(err.Error(), "this gt-agent session") {
		t.Fatalf("expected smoke required this session, got %v", err)
	}
}
