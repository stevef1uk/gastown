package cmd

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	// Drop inherited Gas Town session env so tests do not hand off, sling, or bind
	// to the developer's real town (e.g. GT_ROOT=~/gt, GT_ROLE=mayor).
	for _, e := range os.Environ() {
		idx := strings.IndexByte(e, '=')
		if idx > 0 && strings.HasPrefix(e[:idx], "GT_") {
			_ = os.Unsetenv(e[:idx])
		}
	}

	// macOS kills unsigned dev builds in persistentPreRun; subprocesses must use
	// a test-built gt (see buildGTBinary in test_helpers_test.go).
	if runtime.GOOS == "darwin" {
		if bin, err := buildGTBinary(); err == nil {
			_ = os.Setenv("GT_BINARY", bin)
		}
	}

	os.Exit(m.Run())
}
