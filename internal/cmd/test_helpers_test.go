package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// buildGT builds the gt binary and returns its path.
// It caches the build across tests in the same run.
var cachedGTBinary string

func buildGTBinary() (string, error) {
	if cachedGTBinary != "" {
		if _, err := os.Stat(cachedGTBinary); err == nil {
			return cachedGTBinary, nil
		}
		cachedGTBinary = ""
	}

	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}

	projectRoot := wd
	for {
		if _, err := os.Stat(filepath.Join(projectRoot, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(projectRoot)
		if parent == projectRoot {
			return "", fmt.Errorf("could not find project root (go.mod)")
		}
		projectRoot = parent
	}

	tmpDir := os.TempDir()
	binaryName := "gt-integration-test"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	tmpBinary := filepath.Join(tmpDir, binaryName)
	ldflags := "-X github.com/steveyegge/gastown/internal/cmd.BuiltProperly=1"
	cmd := exec.Command("go", "build", "-ldflags", ldflags, "-o", tmpBinary, "./cmd/gt")
	cmd.Dir = projectRoot
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build gt: %w\n%s", err, output)
	}

	cachedGTBinary = tmpBinary
	return tmpBinary, nil
}

func buildGT(t *testing.T) string {
	t.Helper()
	bin, err := buildGTBinary()
	if err != nil {
		t.Fatal(err)
	}
	return bin
}
