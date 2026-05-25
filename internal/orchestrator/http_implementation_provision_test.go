package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelectHTTPImplementationProfileID_goWebServer(t *testing.T) {
	t.Parallel()
	town := t.TempDir()
	rig := "linkshelf"
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	arch := "| GET | /static/{file} | web/{file} |\n"
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:    "linkshelf",
		QAVerifyCommand: "cd linkshelf && go test ./...",
		RequiredFiles: []string{
			"linkshelf/web/index.html",
			"linkshelf/cmd/server/main.go",
			"linkshelf/internal/api/handlers.go",
		},
	}
	if !NeedsHTTPImplementationProfile(town, rig, v) {
		t.Fatal("expected HTTP profile needed")
	}
	if got := SelectHTTPImplementationProfileID(town, rig, v); got != "go-stdlib-servemux" {
		t.Fatalf("profile: got %q", got)
	}
}

func TestSelectHTTPImplementationProfileID_nonStdlibArch(t *testing.T) {
	t.Parallel()
	town := t.TempDir()
	rig := "app"
	rigDir := filepath.Join(town, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte("uses github.com/go-chi/chi\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{QAVerifyCommand: "go test ./..."}
	if got := SelectHTTPImplementationProfileID(town, rig, v); got != "generic" {
		t.Fatalf("got %q", got)
	}
}

func TestEnsureHTTPImplementationRigConfig_writesOnce(t *testing.T) {
	t.Parallel()
	InvalidateHTTPProfileCacheForTest()
	town := t.TempDir()
	rig := "rig1"
	v := WorkflowValidation{
		QAVerifyCommand: "go test ./...",
		RequiredFiles:   []string{"internal/api/handlers.go"},
	}
	created, err := EnsureHTTPImplementationRigConfig(town, rig, v)
	if err != nil || !created {
		t.Fatalf("first write: created=%v err=%v", created, err)
	}
	path := HTTPImplementationRigConfigPath(town, rig)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var onDisk httpRigConfigFile
	if err := json.Unmarshal(data, &onDisk); err != nil {
		t.Fatal(err)
	}
	if onDisk.Profile != "go-stdlib-servemux" {
		t.Fatalf("profile: %+v", onDisk)
	}
	created2, err := EnsureHTTPImplementationRigConfig(town, rig, v)
	if err != nil || created2 {
		t.Fatalf("second write: created=%v err=%v", created2, err)
	}
}

func TestNeedsHTTPImplementationProfile_goModOnly(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		QAVerifyCommand: "go mod tidy",
		RequiredFiles:   []string{"go.mod"},
	}
	if NeedsHTTPImplementationProfile("", "", v) {
		t.Fatal("go.mod-only rig should not need HTTP profile file")
	}
}

func TestEnsureHTTPImplementationRigConfigLog(t *testing.T) {
	t.Parallel()
	town := t.TempDir()
	rig := "r"
	v := WorkflowValidation{
		QAVerifyCommand: "go test ./...",
		RequiredFiles:   []string{"internal/api/handlers.go"},
	}
	line, err := EnsureHTTPImplementationRigConfigLog(town, rig, v)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "http-implementation.json") || !strings.Contains(line, "go-stdlib-servemux") {
		t.Fatalf("log: %q", line)
	}
}
