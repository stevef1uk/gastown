package orchestrator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSmokeServerExtractionPreservesRedirects(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ input, want string }{
		{
			`cd service && (uvicorn main:app --port 8080 >/tmp/server.log 2>&1 & pid=$!; sleep 1; curl --fail http://localhost:8080/probe; status=$?; kill "$pid"; wait "$pid"; exit "$status")`,
			`uvicorn main:app --port 8080 >/tmp/server.log 2>&1`,
		},
		{
			`cd service && (../environments/check/bin/python3 -m uvicorn main:app --port 8080 2>&1 & pid=$!; curl localhost:8080/probe)`,
			`../environments/check/bin/python3 -m uvicorn main:app --port 8080 2>&1`,
		},
		{
			`uvicorn main:app --log-config "logging & tracing.yaml" >"/tmp/server & error.log" 2>&1 && curl localhost:8080/probe`,
			`uvicorn main:app --log-config "logging & tracing.yaml" >"/tmp/server & error.log" 2>&1`,
		},
		{
			`uvicorn main:app &>/tmp/server.log & curl localhost:8080/probe`,
			`uvicorn main:app &>/tmp/server.log`,
		},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got := ExtractPythonServerStartFromText(tc.input)
			if got != tc.want {
				t.Fatalf("server start = %q, want %q", got, tc.want)
			}
			assertSmokeShellSyntax(t, got)
		})
	}
	// The same trimmer handles documented Go/Node server starts as well.
	for _, start := range []string{`go run ./cmd/server 2>&1`, `npm run dev -- --host "127.0.0.1" 2>&1`} {
		docs := "## Runtime smoke server\n" + start + " &\n"
		if got := parseSmokeServerSection(docs); got != start {
			t.Fatalf("documented server start = %q, want %q", got, start)
		}
		assertSmokeShellSyntax(t, parseSmokeServerSection(docs))
	}
}

func assertSmokeShellSyntax(t *testing.T, start string) {
	t.Helper()
	script := BuildRuntimeSmokeShell(t.TempDir(), APISmokeSpec{ServerStart: start, Port: 8080, GETPaths: []string{"/probe"}})
	if out, err := exec.Command("/bin/bash", "-n", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("generated invalid shell: %v\n%s\n%s", err, out, script)
	}
}

func TestImplementationPythonModuleUsesRigVenv(t *testing.T) {
	for _, status := range []int{0, 23} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			rigDir := t.TempDir()
			venv := "environments/checks"
			bin := filepath.Join(rigDir, venv, "bin")
			for _, dir := range []string{bin, filepath.Join(rigDir, "service", "api")} {
				if err := os.MkdirAll(dir, 0755); err != nil {
					t.Fatal(err)
				}
			}
			shim := fmt.Sprintf("#!/bin/sh\nprintf 'configured verifier invoked\\n' >&2\nexit %d\n", status)
			if err := os.WriteFile(filepath.Join(bin, "uvicorn"), []byte(shim), 0755); err != nil {
				t.Fatal(err)
			}
			v := WorkflowValidation{
				LayoutRoot: "service/api", RequiredFiles: []string{"service/api/main.py"}, PythonVenvDir: venv,
				QAVerifyCommand: "cd service/api && uvicorn main:app --port 8080",
			}
			err := ImplementationPythonModuleOK(rigDir, v)
			if status == 0 && err != nil {
				t.Fatal(err)
			}
			if status != 0 {
				if err == nil {
					t.Fatal("failed configured verifier was accepted")
				}
				for _, want := range []string{"exit status 23", "configured verifier invoked", "cwd=" + rigDir, v.QAVerifyCommand} {
					if !strings.Contains(err.Error(), want) {
						t.Fatalf("missing diagnostic %q: %v", want, err)
					}
				}
			}
		})
	}
}

func TestWorkflowNeedsRuntimeSmoke_pythonWithAPI(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rig := "rig"
	rigDir := filepath.Join(dir, rig, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	arch := "| GET | /api/items | JSON array when empty |\n"
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:      "backend",
		QAVerifyCommand: "python3 -m pytest -q",
		RequiredFiles:   []string{"backend/app.py"},
		PythonVenvDir:   ".venv",
	}
	if !WorkflowNeedsRuntimeSmoke(dir, rig, v) {
		t.Fatal("expected python HTTP rig to need runtime smoke")
	}
}

func TestDeriveRuntimeSmokeServerStart_python(t *testing.T) {
	t.Parallel()
	docs := "## Runtime smoke server\n.venv/bin/python3 -m uvicorn backend.main:app --host 127.0.0.1 --port 8080\n"
	v := WorkflowValidation{
		QAVerifyCommand: "python3 -m pytest -q",
		PythonVenvDir:   ".venv",
	}
	got := deriveRuntimeSmokeServerStart(v, docs)
	if !strings.Contains(got, "uvicorn") {
		t.Fatalf("got %q", got)
	}
}

func TestDeriveRuntimeSmokeServerStart_pythonFromQA(t *testing.T) {
	t.Parallel()
	v := WorkflowValidation{
		QAVerifyCommand: "cd backend && .venv/bin/python3 -m uvicorn app:app --port 8080 & python3 -m pytest -q",
		PythonVenvDir:   ".venv",
	}
	got := deriveRuntimeSmokeServerStart(v, "")
	if !strings.Contains(got, "uvicorn app:app") {
		t.Fatalf("got %q", got)
	}
}

func TestDeriveRuntimeSmokeServerStart_stripsTrailingParenthesizedDocsComments(t *testing.T) {
	t.Parallel()
	docs := "## Runtime smoke server\nnpm run build (produces static export)\n"
	got := deriveRuntimeSmokeServerStart(WorkflowValidation{}, docs)
	if got != "npm run build" {
		t.Fatalf("got %q", got)
	}
}

func TestPythonTestsAllPassed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		output string
		want   bool
	}{
		{"collected 3 items\ntest_main.py ...\n1 passed, 2 warnings", true},
		{"collected 3 items\ntest_main.py ...\n3 passed", true},
		{"collected 1 item\ntest_main.py F\n1 failed", false},
		{"collected 0 items", false},
		{"ModuleNotFoundError: No module named 'fastapi'", false},
		{"ERROR collecting test_main.py\nimport error", false},
	}
	for _, c := range cases {
		got := PythonTestsAllPassed(c.output)
		if got != c.want {
			t.Fatalf("PythonTestsAllPassed(%q) = %v, want %v", c.output, got, c.want)
		}
	}
}

func TestPythonVerifyNoTestsOK(t *testing.T) {
	t.Parallel()
	cases := []struct {
		output string
		want   bool
	}{
		{"no tests ran", true},
		{"collected 0 items", true},
		{"collected 3 items\ntest_main.py ...\n3 passed", false},
		{"collected 1 item\ntest_main.py F\n1 failed", false},
	}
	for _, c := range cases {
		got := PythonVerifyNoTestsOK(c.output)
		if got != c.want {
			t.Fatalf("PythonVerifyNoTestsOK(%q) = %v, want %v", c.output, got, c.want)
		}
	}
}

func TestIsDevServerSmokeCommand(t *testing.T) {
	t.Parallel()

	// Should match: real server starts
	cases := []string{
		"cd app && uvicorn app:app --port 8080",
		".venv/bin/python -m uvicorn pingapp.main:app --host 127.0.0.1",
		"cd rig && .venv/bin/python3 -m hypercorn app:app",
		"gunicorn myapp:app",
		"flask run",
		"npx vite --host 127.0.0.1",
		"npx next dev",
		"npx http-server web",
	}
	for _, c := range cases {
		if !IsDevServerSmokeCommand(c) {
			t.Fatalf("should match as dev server smoke: %q", c)
		}
	}

	// Should NOT match: pip install, python -c imports, pytest
	noCases := []string{
		"python3 -m pytest -q",
		"pip install uvicorn fastapi pytest",
		".venv/bin/pip3 install --quiet fastapi uvicorn pytest httpx",
		`python3 -c "import fastapi,uvicorn,httpx,pytest;print(fastapi.__version__)"`,
		"cd linkshelf && npx playwright test",
		"npx playwright test --list",
		"npx jest",
		"npx tsc --noEmit",
	}
	for _, c := range noCases {
		if IsDevServerSmokeCommand(c) {
			t.Fatalf("should NOT match as dev server smoke: %q", c)
		}
	}
}
