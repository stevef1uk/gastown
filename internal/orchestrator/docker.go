package orchestrator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// PlaywrightDockerImage is the shared Playwright test-runner image used by
// docker-compose integration-test phases. Built once per machine and reused
// across rigs; created from the embedded Dockerfile.playwright template.
const PlaywrightDockerImage = "playwright-go-test:latest"

// playwrightRunnerDockerfile is the embedded template that builds the Playwright
// test-runner image (base + @playwright/test) whose node_modules are copied into
// the named volume on first compose up.
const playwrightRunnerDockerfile = "town/templates/rig-init/Dockerfile.playwright"

// EnsurePlaywrightDockerImageAsync checks whether the Playwright Docker image
// exists and, when the rig's profile ships docker-compose + Playwright files,
// starts an asynchronous background build if it is missing. It returns promptly
// so workflow start is never blocked on a multi-minute docker build. The build
// process is detached (setsid) so it survives this CLI process exiting.
func EnsurePlaywrightDockerImageAsync(townRoot, rig string) error {
	v, ok, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	needs := false
	for _, p := range v.DeliveryPhases {
		if phaseShipsDockerPlaywright(&p) {
			needs = true
			break
		}
	}
	if !needs {
		return nil
	}
	if dockerImageExists(PlaywrightDockerImage) {
		return nil
	}
	return launchDetachedPlaywrightBuild()
}

// dockerImageExists reports whether a Docker image with the given reference is
// present locally. A short timeout keeps workflow start snappy.
func dockerImageExists(ref string) bool {
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "image", "inspect", ref)
	return cmd.Run() == nil
}

// launchDetachedPlaywrightBuild writes the runner Dockerfile to a temp dir and
// runs `docker build` as a detached background process, logging to the same dir.
// Returns an error only if the build cannot be launched, not if it is still
// running. The process is put in its own session so it survives CLI exit.
func launchDetachedPlaywrightBuild() error {
	dockerBin, err := exec.LookPath("docker")
	if err != nil {
		return fmt.Errorf("docker not found on PATH: %w", err)
	}
	buildDir, err := os.MkdirTemp("", "playwright-build-*")
	if err != nil {
		return fmt.Errorf("create build temp dir: %w", err)
	}
	dfData, err := townAssets.ReadFile(playwrightRunnerDockerfile)
	if err != nil {
		return fmt.Errorf("read embedded Dockerfile.playwright: %w", err)
	}
	dfPath := filepath.Join(buildDir, "Dockerfile")
	if err := os.WriteFile(dfPath, dfData, 0o644); err != nil {
		return fmt.Errorf("write Dockerfile.playwright: %w", err)
	}

	logPath := filepath.Join(buildDir, "build.log")
	logF, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open build log: %w", err)
	}

	cmd := exec.Command(dockerBin, "build", "-t", PlaywrightDockerImage, ".")
	cmd.Dir = buildDir
	cmd.Stdout = logF
	cmd.Stderr = logF
	// Detach: own session/process group so a parent exit doesn't kill it.
	detachCmd(cmd)
	if err := cmd.Start(); err != nil {
		logF.Close()
		return fmt.Errorf("start detached docker build: %w", err)
	}
	// Release the goroutine tracking the process; the OS reparents it to init.
	go func() {
		_ = cmd.Wait()
		logF.Close()
	}()
	// Do not remove buildDir: the running process still needs its Dockerfile.
	return nil
}

// DockerImageStatusLog returns a human summary of the Playwright image state,
// used for diagnostics. Returns "" when the profile doesn't need Playwright.
func DockerImageStatusLog(townRoot, rig string) string {
	v, ok, err := LoadRigWorkflowProfileFile(townRoot, rig)
	if err != nil || !ok {
		return ""
	}
	needs := false
	for _, p := range v.DeliveryPhases {
		if phaseShipsDockerPlaywright(&p) {
			needs = true
			break
		}
	}
	if !needs {
		return ""
	}
	if dockerImageExists(PlaywrightDockerImage) {
		return fmt.Sprintf("Playwright Docker image %s present", PlaywrightDockerImage)
	}
	return fmt.Sprintf("Playwright Docker image %s missing; build launched in background", PlaywrightDockerImage)
}