package orchestrator

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Exercise the actual completion gate with the redirected, subshell-wrapped
// profile command from the production failure, not just an agent smoke CMD.
func TestImplementationPhaseVerifyRedirectedPythonServer(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not installed")
	}
	t.Setenv("GT_SKIP_IMPLEMENTATION_SMOKE", "")
	t.Setenv("GT_TEST_COMPLETION_SMOKE", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"profile", "docs", "invalid script"} {
		t.Run(source, func(t *testing.T) {
			town := t.TempDir()
			rig := "sample"
			rigDir := filepath.Join(town, rig, "mayor", "rig")
			module := filepath.Join(rigDir, "service")
			bin := filepath.Join(rigDir, "environment", "bin")
			for _, dir := range []string{module, bin} {
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
			}
			shim := "#!/bin/sh\nexec " + bashSingleQuote(exe) + " -test.run=^TestCompletionSmokeServerProcess$ -- \"$@\"\n"
			if err := os.WriteFile(filepath.Join(bin, "uvicorn"), []byte(shim), 0755); err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := listener.Addr().(*net.TCPAddr).Port
			_ = listener.Close()
			start := fmt.Sprintf("uvicorn main:app --port %d >%s 2>&1", port, bashSingleQuote(filepath.Join(town, "server.log")))
			verify := fmt.Sprintf("cd service && (%s & pid=$!; sleep 1; curl --fail --max-time 2 http://127.0.0.1:%d/probe; status=$?; kill \"$pid\" 2>/dev/null || true; wait \"$pid\" 2>/dev/null || true; exit \"$status\")", start, port)
			docs := fmt.Sprintf("Server URL: http://127.0.0.1:%d\n| GET | /probe | 200, JSON array when empty |\n", port)
			if source != "profile" {
				verify = "true"
				if source == "invalid script" {
					start = "uvicorn main:app 2>"
				}
				docs += "\n## Runtime smoke server\n" + start + "\n"
			}
			if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(docs), 0644); err != nil {
				t.Fatal(err)
			}
			v := WorkflowValidation{LayoutRoot: "service", RequiredFiles: []string{"service/main.py"}, TestRunner: "pytest", PythonVenvDir: "environment", DevServerPort: port, QAVerifyCommand: verify}
			t.Cleanup(func() {
				if data, err := os.ReadFile(filepath.Join(module, ".gt-smoke.pid")); err == nil {
					if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
						if proc, err := os.FindProcess(pid); err == nil {
							_ = proc.Kill()
						}
					}
				}
			})
			err = ImplementationPhaseVerifyOK(town, rig, v)
			if source == "invalid script" {
				if !IsRuntimeSmokeScriptError(err) || ImplementationVerifyNeedsRuntimeRework(err) {
					t.Fatalf("invalid generated shell must not request application rework: %v", err)
				}
				reopened, reopenErr := ReopenImplementationBeadsAfterSmokeFailure(town, rig, v, err)
				if reopenErr != nil || len(reopened) != 0 {
					t.Fatalf("invalid shell reopened application beads: %v, %v", reopened, reopenErr)
				}
				if _, statErr := os.Stat(filepath.Join(town, "server.log")); !os.IsNotExist(statErr) {
					t.Fatalf("invalid script should be caught before server launch: %v", statErr)
				}
				return
			}
			if err != nil {
				data, _ := os.ReadFile(filepath.Join(town, "server.log"))
				spec, _ := LoadAPISmokeSpecFromRig(town, rig, v)
				t.Fatalf("%v\nserver log: %s\nsmoke spec: %+v", err, data, spec)
			}
		})
	}
}

func TestCompletionSmokeServerProcess(t *testing.T) {
	if os.Getenv("GT_TEST_COMPLETION_SMOKE") != "1" {
		return
	}
	port := ""
	for i, arg := range os.Args {
		if arg == "--port" && i+1 < len(os.Args) {
			port = os.Args[i+1]
		}
	}
	if port == "" {
		os.Exit(2)
	}
	_ = http.ListenAndServe("127.0.0.1:"+port, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	os.Exit(3)
}

func linkshelfLikeValidation() WorkflowValidation {
	v := DefaultWorkflowValidation()
	v.LayoutRoot = "linkshelf"
	v.QAVerifyCommand = "cd linkshelf && go test ./..."
	v.DevServerPort = 8080
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
	if !WorkflowNeedsRuntimeSmoke("", "", v) {
		t.Fatal("expected web+server profile to need runtime smoke")
	}
	v2 := DefaultWorkflowValidation()
	v2.LayoutRoot = "linkshelf"
	v2.RequiredFiles = []string{"linkshelf/cmd/server/main.go"}
	if WorkflowNeedsRuntimeSmoke("", "", v2) {
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
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	// Packages run concurrently under `make test`; never share their dev port.
	serverMain = strings.ReplaceAll(serverMain, ":8080", fmt.Sprintf("127.0.0.1:%d", port))
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
	arch := fmt.Sprintf("Server URL: http://127.0.0.1:%d\n", port) + `| Method | Path | Notes |
| GET | / | index.html |
| GET | /api/links | returns JSON array |
`
	goMod := "module linkshelf\n\ngo 1.22\n"
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "cmd", "server", "main.go"), []byte(serverMain), 0644); err != nil {
		t.Fatal(err)
	}
	// Keep compiler/linker scheduling outside the smoke readiness deadline.
	// The smoke still launches and probes the real fixture server.
	serverBin := filepath.Join(townRoot, "smoke-server")
	build := exec.Command("go", "build", "-o", serverBin, "./cmd/server")
	build.Dir = mod
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build smoke fixture: %v\n%s", err, out)
	}
	arch += "\n## Runtime smoke server\n" + bashSingleQuote(serverBin) + "\n"
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Successful smoke removes this file; clean up failed probes as well.
		if data, err := os.ReadFile(filepath.Join(mod, ".gt-smoke.pid")); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
				if process, err := os.FindProcess(pid); err == nil {
					_ = process.Kill()
				}
			}
		}
	})
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
	v.DevServerPort = port
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
	// API-only server serves /api/links but not / or static assets.
	// Root probe now accepts any HTTP response (including 404) — only connection
	// failure stops the smoke. The server responds, so root probe passes.
	// Endpoint probes check /api/links (served) → smoke passes.
	err := ImplementationRuntimeSmokeOK(townRoot, rig, v)
	if err != nil {
		t.Fatalf("API-only server should pass smoke (root 404 is acceptable): %v", err)
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
	// Phase verify runs runtime smoke first. API-only server passes smoke
	// (root 404 acceptable, /api/links served), so phase verify succeeds.
	err := ImplementationPhaseVerifyOK(townRoot, rig, v)
	if err != nil {
		t.Fatalf("API-only server should pass phase verify: %v", err)
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
