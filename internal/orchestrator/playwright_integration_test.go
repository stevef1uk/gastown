package orchestrator

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPlaywrightIntegration_HostNetworking(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Check Docker is available
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}

	// Check playwright-go-test image exists
	if err := checkPlaywrightImage(); err != nil {
		t.Skipf("playwright-go-test:latest image not available: %v", err)
	}

	ctx := context.Background()

	// Ensure port 8080 is free from any leftover processes. KillTCPListenersOnPort
	// targets only the PID listening on 8080 (via lsof), so it never kills by
	// process name and can't clobber unrelated processes like the test runner.
	_, _ = KillTCPListenersOnPort(8080)
	time.Sleep(500 * time.Millisecond)

	// Create temp directory for test rig
	tmpDir, err := os.MkdirTemp("", "playwright-integration-*")
	if err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create minimal Go web server
	serverDir := filepath.Join(tmpDir, "pingapp")
	if err := os.MkdirAll(filepath.Join(serverDir, "cmd", "server"), 0755); err != nil {
		t.Fatalf("mkdir server: %v", err)
	}

	// go.mod
	goMod := `module pingapp

go 1.22
`
	if err := os.WriteFile(filepath.Join(serverDir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	// main.go - simple HTTP server
	mainGo := `package main

import (
	"encoding/json"
	"net/http"
)

func main() {
	http.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"message": "pong"})
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(` + "`" + `<html><body><h1>Hello</h1><button id="btn">Click me</button><script>document.getElementById('btn').onclick=()=>document.getElementById('btn').innerText='Clicked!'</script></body></html>` + "`" + `))
	})
	http.ListenAndServe(":8080", nil)
}
`
	if err := os.WriteFile(filepath.Join(serverDir, "cmd", "server", "main.go"), []byte(mainGo), 0644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	// Create workflow-profile.json for ScaffoldRigIntegrationTemplates to read
	profileDir := filepath.Join(tmpDir, "testrig", "mayor", "rig", ".gastown")
	if err := os.MkdirAll(profileDir, 0755); err != nil {
		t.Fatalf("mkdir profile: %v", err)
	}
	profileJSON := `{
  "version": 1,
  "validation": {
    "layout_root": "pingapp",
    "test_runner": "go",
    "dev_server_port": 8080,
    "delivery_phases": [
      {"id": "go-module", "required_files": ["pingapp/go.mod"]},
      {"id": "core", "required_files": ["pingapp/cmd/server/main.go"]},
      {"id": "integration-test", "required_files": [
        "pingapp/playwright.config.ts",
        "pingapp/e2e/ping.spec.ts",
        "pingapp/package.json",
        "pingapp/docker-compose.yml"
      ]}
    ]
  }
}
`
	if err := os.WriteFile(filepath.Join(profileDir, "workflow-profile.json"), []byte(profileJSON), 0644); err != nil {
		t.Fatalf("write workflow-profile.json: %v", err)
	}

	// Create workflow validation profile for this rig
	_ = WorkflowValidation{
		LayoutRoot:    "pingapp",
		TestRunner:    "go",
		DevServerPort: 8080,
		DeliveryPhases: []DeliveryPhase{
			{ID: "go-module", RequiredFiles: []string{"pingapp/go.mod"}},
			{ID: "core", RequiredFiles: []string{"pingapp/cmd/server/main.go"}},
			{ID: "integration-test", RequiredFiles: []string{
				"pingapp/playwright.config.ts",
				"pingapp/e2e/ping.spec.ts",
				"pingapp/package.json",
				"pingapp/docker-compose.yml",
			}},
		},
	}

	// Scaffold integration templates
	written, err := ScaffoldRigIntegrationTemplates(tmpDir, "testrig")
	if err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	t.Logf("Scaffolded %d files", written)

	// The scaffolded files go to townRoot/rig/mayor/rig/layoutRoot
	serverDir = filepath.Join(tmpDir, "testrig", "mayor", "rig", "pingapp")

	// Verify files exist
	for _, f := range []string{
		"docker-compose.yml",
		"package.json",
		"playwright.config.ts",
	} {
		path := filepath.Join(serverDir, f)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing scaffolded file %s: %v", f, err)
		}
	}

	// Create e2e directory and Playwright test
	if err := os.MkdirAll(filepath.Join(serverDir, "e2e"), 0755); err != nil {
		t.Fatalf("mkdir e2e: %v", err)
	}
	specContent := `import { test, expect } from '@playwright/test';

const baseURL = process.env.BASE_URL || 'http://localhost:8080';

test.describe('Ping App', () => {
  test('GET /ping returns pong', async ({ request }) => {
    const response = await request.get(baseURL + '/ping');
    expect(response.ok()).toBeTruthy();
    const body = await response.json();
    expect(body.message).toBe('pong');
  });

  test('Home page loads', async ({ page }) => {
    await page.goto(baseURL + '/');
    await expect(page.locator('h1')).toHaveText('Hello');
  });
});
`
	if err := os.WriteFile(filepath.Join(serverDir, "e2e", "ping.spec.ts"), []byte(specContent), 0644); err != nil {
		t.Fatalf("write spec: %v", err)
	}

	// Build Go server
	t.Log("Building Go server...")
	cmd := exec.CommandContext(ctx, "go", "build", "-o", filepath.Join(serverDir, "server"), "./cmd/server")
	cmd.Dir = filepath.Join(tmpDir, "pingapp")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	// Start Go server on host
	serverCmd := exec.CommandContext(ctx, filepath.Join(serverDir, "server"))
	serverCmd.Dir = serverDir
	stdout, _ := serverCmd.StdoutPipe()
	stderr, _ := serverCmd.StderrPipe()
	if err := serverCmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer serverCmd.Process.Kill()

	// Wait for server to be ready
	client := &http.Client{Timeout: 2 * time.Second}
	ready := false
	for i := 0; i < 30; i++ {
		resp, err := client.Get("http://localhost:8080/ping")
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			ready = true
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		// Dump logs
		io.Copy(os.Stdout, stdout)
		io.Copy(os.Stderr, stderr)
		t.Fatal("server not ready after 3s")
	}
	t.Log("Server ready on localhost:8080")

	// Run Playwright container via docker compose; it reaches the host Go server
	// through host.docker.internal (host-gateway), portable across Linux/macOS.
	t.Log("Running Playwright tests...")
	dockerCmd := exec.CommandContext(ctx,
		"docker-compose", "up", "--exit-code-from", "playwright",
	)
	dockerCmd.Dir = serverDir
	dockerCmd.Env = append(os.Environ(), "COMPOSE_PROJECT_NAME=playwright-test")
	output, err := dockerCmd.CombinedOutput()
	t.Logf("Playwright output:\n%s", output)
	if err != nil {
		t.Fatalf("Playwright tests failed: %v", err)
	}

	if !strings.Contains(string(output), "passed") {
		t.Fatalf("Tests did not pass")
	}

	t.Log("Playwright integration test PASSED")
}

func checkPlaywrightImage() error {
	cmd := exec.Command("docker", "image", "inspect", "playwright-go-test:latest")
	return cmd.Run()
}