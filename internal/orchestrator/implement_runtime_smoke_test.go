package orchestrator

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func linkshelfLikeValidation() WorkflowValidation {
	v := DefaultWorkflowValidation()
	v.LayoutRoot = "linkshelf"
	v.QAVerifyCommand = "cd linkshelf && go test ./..."
	v.RequiredFiles = []string{
		"linkshelf/cmd/server/main.go",
		"linkshelf/web/index.html",
		"linkshelf/web/app.js",
		"linkshelf/internal/api/handlers.go",
	}
	return v
}

func TestWorkflowNeedsRuntimeSmoke(t *testing.T) {
	t.Parallel()
	v := linkshelfLikeValidation()
	if !WorkflowNeedsRuntimeSmoke(v) {
		t.Fatal("expected web+server profile to need runtime smoke")
	}
	v2 := DefaultWorkflowValidation()
	v2.LayoutRoot = "linkshelf"
	v2.RequiredFiles = []string{"linkshelf/cmd/server/main.go"}
	if WorkflowNeedsRuntimeSmoke(v2) {
		t.Fatal("expected missing web assets to skip smoke")
	}
}

func requireSmokeTools(t *testing.T) {
	t.Helper()
	requireGoToolchain(t)
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not in PATH")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not in PATH")
	}
}

func writeSmokeFixtureTown(t *testing.T, serverMain string) (townRoot, rig string, v WorkflowValidation) {
	t.Helper()
	townRoot = t.TempDir()
	rig = "testrig"
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	mod := filepath.Join(rigDir, "linkshelf")
	for _, dir := range []string{
		filepath.Join(mod, "cmd", "server"),
		filepath.Join(mod, "web"),
		filepath.Join(mod, "internal", "api"),
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	arch := `| Method | Path | Notes |
| GET | / | index.html |
| GET | /api/links | returns JSON array |
`
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	goMod := "module linkshelf\n\ngo 1.22\n"
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "cmd", "server", "main.go"), []byte(serverMain), 0644); err != nil {
		t.Fatal(err)
	}
	html := `<!DOCTYPE html><html><head><link rel="stylesheet" href="/static/style.css"></head><body><script src="/static/app.js"></script></body></html>`
	for name, body := range map[string]string{
		"index.html": html,
		"app.js":     "console.log('ok');\n",
		"style.css":  "body {}\n",
	} {
		if err := os.WriteFile(filepath.Join(mod, "web", name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(mod, "internal", "api", "handlers_test.go"), []byte(`package api

import "testing"

func TestPlaceholder(t *testing.T) {}
`), 0644); err != nil {
		t.Fatal(err)
	}
	v = linkshelfLikeValidation()
	return townRoot, rig, v
}

const apiOnlyServerMain = `package main

import (
	"net/http"
)

func main() {
	http.HandleFunc("/api/links", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	})
	_ = http.ListenAndServe(":8080", nil)
}
`

const fullSmokeServerMain = `package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	modDir, _ := os.Getwd()
	webDir := filepath.Join(modDir, "web")
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(webDir, "index.html"))
	})
	http.HandleFunc("/static/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/static/")
		http.ServeFile(w, r, filepath.Join(webDir, name))
	})
	http.HandleFunc("/api/links", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	})
	_ = http.ListenAndServe(":8080", nil)
}
`

func TestImplementationRuntimeSmokeOK_failsAPIOnlyServer(t *testing.T) {
	requireSmokeTools(t)
	townRoot, rig, v := writeSmokeFixtureTown(t, apiOnlyServerMain)
	err := ImplementationRuntimeSmokeOK(townRoot, rig, v)
	if err == nil {
		t.Fatal("expected smoke failure when / and static assets are not served")
	}
	if !strings.Contains(err.Error(), "runtime smoke failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestImplementationRuntimeSmokeOK_passesFullServer(t *testing.T) {
	requireSmokeTools(t)
	townRoot, rig, v := writeSmokeFixtureTown(t, fullSmokeServerMain)
	if err := ImplementationRuntimeSmokeOK(townRoot, rig, v); err != nil {
		if GoToolchainMismatch(err, err.Error()) {
			t.Skip("go toolchain mismatch: " + err.Error())
		}
		t.Fatal(err)
	}
}

func TestImplementationPhaseVerifyOK_failsAPIOnlyServer(t *testing.T) {
	requireSmokeTools(t)
	townRoot, rig, v := writeSmokeFixtureTown(t, apiOnlyServerMain)
	err := ImplementationPhaseVerifyOK(townRoot, rig, v)
	if err == nil {
		t.Fatal("expected phase verify failure when runtime smoke fails")
	}
	if !strings.Contains(err.Error(), "runtime smoke failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestImplementationPhaseVerifyOK_skipsSmokeWithEnv(t *testing.T) {
	requireGoToolchain(t)
	t.Setenv("GT_SKIP_IMPLEMENTATION_SMOKE", "1")
	townRoot, rig, v := writeSmokeFixtureTown(t, apiOnlyServerMain)
	if err := ImplementationPhaseVerifyOK(townRoot, rig, v); err != nil {
		if strings.Contains(err.Error(), "command not found") || GoToolchainMismatch(err, err.Error()) {
			t.Skip(err)
		}
		t.Fatal(err)
	}
}
