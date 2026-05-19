package cmd

import "os"

// gtExecutable returns the gt binary path for subprocess invocation.
// Tests set GT_BINARY to a make-equivalent build (BuiltProperly=1); production uses "gt" on PATH.
func gtExecutable() string {
	if p := os.Getenv("GT_BINARY"); p != "" {
		return p
	}
	return "gt"
}
