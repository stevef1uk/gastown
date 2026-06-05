package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestRunPostNativeWriteFrontendVerify_setsVerifyOK(t *testing.T) {
	dir := t.TempDir()
	rig := "testgt3"
	rigDir := rigMayorRigDir(dir, rig)
	webDir := filepath.Join(rigDir, "linkshelf", "web")
	if err := os.MkdirAll(webDir, 0755); err != nil {
		t.Fatal(err)
	}
	html := `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Linkshelf</title>
<link rel="stylesheet" href="/static/style.css">
</head>
<body>
<h1>Linkshelf</h1>
<form id="add-link-form">
<input type="text" id="title" placeholder="Title" required>
<input type="url" id="url" placeholder="URL" required>
<button type="submit">Add</button>
</form>
<ul id="links"></ul>
<script src="/static/app.js"></script>
</body>
</html>`
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte(html), 0644); err != nil {
		t.Fatal(err)
	}
	v := orchestrator.WorkflowValidation{
		LayoutRoot: "linkshelf",
		RequiredFiles: []string{
			"linkshelf/web/index.html",
		},
		MinImplementationFileBytes: 80,
	}.WithDefaults()
	task := &orchestrator.Task{
		State: "implementation",
		Hooks: orchestrator.StateHooks{Track: "implementation"},
		Validation: v,
	}
	r := newStateRunner(task, dir, rig)
	r.track.activeBead = "te-yzq"
	var combined strings.Builder
	r.runPostNativeWriteFrontendVerify("linkshelf/web/index.html", &combined)
	if !r.track.verifyOK {
		t.Fatal("expected verifyOK after frontend artifact check")
	}
	if !strings.Contains(combined.String(), "Frontend artifact OK") {
		t.Fatalf("feedback: %s", combined.String())
	}
}

func TestRejectInventedBdVerifyCommand(t *testing.T) {
	v := orchestrator.WorkflowValidation{
		LayoutRoot:      "linkshelf",
		TestRunner:      "go",
		QAVerifyCommand: "cd linkshelf && go test ./...",
		RequiredFiles:     []string{"linkshelf/internal/store/schema.go"},
	}
	err := rejectInventedBdVerifyCommand("cd testgt3/mayor/rig && bd verify te-bol", t.TempDir(), "testgt3", "te-bol", v)
	if err == nil {
		t.Fatal("expected reject")
	}
	if !strings.Contains(err.Error(), "no verify subcommand") {
		t.Fatalf("err=%v", err)
	}
}
