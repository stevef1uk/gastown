package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestQASmokeSourceFingerprint_changesWhenWebChanges(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "testrig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	webDir := filepath.Join(rigDir, "linkshelf", "web")
	if err := os.MkdirAll(webDir, 0755); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(webDir, "index.html")
	if err := os.WriteFile(index, []byte("<html></html>"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot: "linkshelf",
		RequiredFiles: []string{
			"linkshelf/web/index.html",
			"linkshelf/internal/api/handlers.go",
		},
	}
	fp1 := QASmokeSourceFingerprint(dir, rig, v)
	if err := os.WriteFile(index, []byte("<html><body>changed</body></html>"), 0644); err != nil {
		t.Fatal(err)
	}
	fp2 := QASmokeSourceFingerprint(dir, rig, v)
	if fp1 == fp2 {
		t.Fatalf("fingerprint should change after edit: %q", fp1)
	}
}
