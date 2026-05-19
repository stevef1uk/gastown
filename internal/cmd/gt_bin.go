package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// gtExecutable returns the gt command name or path for subprocess invocation.
// Tests set GT_BINARY to a make-equivalent build (BuiltProperly=1); production uses "gt" on PATH.
func gtExecutable() string {
	if p := os.Getenv("GT_BINARY"); p != "" {
		return p
	}
	return "gt"
}

// gtSubprocessPath returns a path suitable for exec.CommandContext(path, "convoy", ...).
// Prefer GT_BINARY (tests), then the running gt binary, then PATH lookup.
// Avoids using os.Executable() during "go test", which points at the *.test binary.
func gtSubprocessPath() string {
	if p := os.Getenv("GT_BINARY"); p != "" {
		return p
	}
	if exe, err := os.Executable(); err == nil {
		base := filepath.Base(exe)
		if base == "gt" || strings.HasPrefix(base, "gt-") {
			return exe
		}
	}
	if p, err := exec.LookPath(gtExecutable()); err == nil {
		return p
	}
	return gtExecutable()
}
