package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestPythonServerOnlySmokeUsesRigVenvAndProbes(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		venv   string
		status int
	}{
		{venv: ".venv", status: 200},
		{venv: "environments/test", status: 503},
	} {
		t.Run(fmt.Sprintf("%s/%d", tc.venv, tc.status), func(t *testing.T) {
			root := t.TempDir()
			rigDir := filepath.Join(root, "sample", "mayor", "rig")
			moduleDir := filepath.Join(rigDir, "service", "api")
			venvBin := filepath.Join(rigDir, tc.venv, "bin")
			for _, dir := range []string{moduleDir, venvBin} {
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
			}
			shim := "#!/bin/sh\nexec " + shellQuote(exe) + " -test.run=^TestPythonSmokeServerProcess$ -- \"$@\"\n"
			if err := os.WriteFile(filepath.Join(venvBin, "python3"), []byte(shim), 0755); err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := listener.Addr().(*net.TCPAddr).Port
			_ = listener.Close()
			doc := fmt.Sprintf("Server URL: http://127.0.0.1:%d\n| GET | /probe | 200, JSON array when empty |\n\n## Runtime smoke server\n%s/bin/python3 -m uvicorn server:app --host 127.0.0.1 --port %d\n", port, tc.venv, port)
			if err := os.WriteFile(filepath.Join(rigDir, "SPEC.md"), []byte(doc), 0644); err != nil {
				t.Fatal(err)
			}
			v := orchestrator.WorkflowValidation{
				LayoutRoot: "service/api", RequiredFiles: []string{"service/api/server.py"},
				TestRunner: "pytest", PythonVenvDir: tc.venv, DevServerPort: port,
			}
			input := "cd " + shellQuote(moduleDir) + " && .venv/bin/python3 -m uvicorn wrong.module:app --port " + strconv.Itoa(port)
			script, ok := normalizeGoDevServerSmokeCommand(input, root, "sample", v)
			if !ok || !orchestrator.IsProfileDerivedSmokeCommand(script) {
				t.Fatalf("server-only command did not become doc-derived probes: %s", script)
			}
			pidPath := filepath.Join(rigDir, "helper.pid")
			t.Cleanup(func() {
				if data, err := os.ReadFile(pidPath); err == nil {
					if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
						if proc, err := os.FindProcess(pid); err == nil {
							_ = proc.Kill()
						}
					}
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/bin/bash", "-c", script)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "GT_TEST_PYTHON_SMOKE=1", "GT_TEST_SMOKE_PID="+pidPath,
				"GT_TEST_SMOKE_CWD="+moduleDir, "GT_TEST_SMOKE_PORT="+strconv.Itoa(port), "GT_TEST_SMOKE_STATUS="+strconv.Itoa(tc.status))
			out, runErr := cmd.CombinedOutput()
			if tc.status == 200 && runErr != nil {
				t.Fatalf("smoke failed: %v\n%s\n%s", runErr, out, script)
			}
			if tc.status != 200 && runErr == nil {
				t.Fatalf("smoke hid failed HTTP verification: %s", out)
			}
			if _, err := os.Stat(pidPath); err != nil {
				t.Fatalf("configured rig-level interpreter was not executed: %v\n%s", err, out)
			}
		})
	}
}

func TestPythonSmokeServerProcess(t *testing.T) {
	if os.Getenv("GT_TEST_PYTHON_SMOKE") != "1" {
		return
	}
	cwd, _ := os.Getwd()
	if cwd != os.Getenv("GT_TEST_SMOKE_CWD") || !strings.Contains(strings.Join(os.Args, " "), "-m uvicorn server:app") {
		os.Exit(2)
	}
	if err := os.WriteFile(os.Getenv("GT_TEST_SMOKE_PID"), []byte(strconv.Itoa(os.Getpid())), 0644); err != nil {
		os.Exit(3)
	}
	status, _ := strconv.Atoi(os.Getenv("GT_TEST_SMOKE_STATUS"))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/probe" {
			w.WriteHeader(status)
			if status != http.StatusOK {
				_, _ = w.Write([]byte("unavailable"))
				return
			}
		}
		_, _ = w.Write([]byte("[]"))
	})
	_ = http.ListenAndServe("127.0.0.1:"+os.Getenv("GT_TEST_SMOKE_PORT"), handler)
	os.Exit(4)
}

// Execute the shell: appended cleanup used to mask probe and cd failures.
func TestSelfManagedPythonSmokePreservesStatusAndLogs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		badCD  bool
	}{
		{name: "success"},
		{name: "failed probe", status: 7},
		{name: "failed cd", status: 2, badCD: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, body := range map[string]string{
				"uvicorn": "#!/bin/sh\nprintf 'startup diagnostic\\n' >&2\nexec sleep 30\n",
				"curl":    fmt.Sprintf("#!/bin/sh\nexit %d\n", tc.status),
			} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0755); err != nil {
					t.Fatal(err)
				}
			}
			workdir := dir
			if tc.badCD {
				workdir = filepath.Join(dir, "missing")
			}
			log := filepath.Join(dir, "server.log")
			input := fmt.Sprintf(`cd %s && (uvicorn main:app --port 8080 >%s 2>&1 & pid=$!; for attempt in 1 2 3 4 5 6 7 8 9 10; do test -s %s && break; sleep 0.05; done; curl --fail http://localhost:8080/probe; status=$?; kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; exit "$status")`, shellQuote(workdir), shellQuote(log), shellQuote(log))
			v := orchestrator.WorkflowValidation{TestRunner: "pytest", RequiredFiles: []string{"service/main.py"}}
			script, _ := normalizeGoDevServerSmokeCommand(input, dir, "sample", v)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/bin/sh", "-c", script)
			cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"))
			out, err := cmd.CombinedOutput()
			status := 0
			if err != nil {
				if exit, ok := err.(*exec.ExitError); ok {
					status = exit.ExitCode()
				} else {
					t.Fatal(err)
				}
			}
			wantStatus := tc.status
			if tc.badCD {
				// Preserve this shell's native cd failure status instead of
				// assuming dash's status 2 on macOS's bash-based /bin/sh.
				baseline := exec.CommandContext(ctx, "/bin/sh", "-c", "cd "+shellQuote(workdir))
				baselineErr := baseline.Run()
				exit, ok := baselineErr.(*exec.ExitError)
				if !ok {
					t.Fatalf("expected native cd failure, got %v", baselineErr)
				}
				wantStatus = exit.ExitCode()
			}
			if ctx.Err() != nil || status != wantStatus {
				t.Fatalf("status=%d want=%d, timeout=%v\n%s\n%s", status, wantStatus, ctx.Err(), out, script)
			}
			if !tc.badCD {
				data, err := os.ReadFile(log)
				if err != nil || !strings.Contains(string(data), "startup diagnostic") {
					t.Fatalf("server log lost: %q, %v", data, err)
				}
			}
		})
	}
}
