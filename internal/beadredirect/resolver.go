package beadredirect

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveBeadsDir returns the actual beads directory, following any redirect.
func ResolveBeadsDir(workDir string) string {
	if filepath.Base(workDir) == ".beads" {
		workDir = filepath.Dir(workDir)
	}
	beadsDir := filepath.Join(workDir, ".beads")
	redirectPath := filepath.Join(beadsDir, "redirect")

	data, err := os.ReadFile(redirectPath)
	if err != nil {
		return beadsDir
	}

	redirectTarget := strings.TrimSpace(string(data))
	if redirectTarget == "" {
		return beadsDir
	}

	var resolved string
	if filepath.IsAbs(redirectTarget) {
		resolved = filepath.Clean(redirectTarget)
	} else {
		resolved = filepath.Clean(filepath.Join(workDir, redirectTarget))
	}

	if resolved == beadsDir {
		fmt.Fprintf(os.Stderr, "Warning: circular redirect detected in %s, ignoring redirect and removing stale file\n", redirectPath)
		_ = os.Remove(redirectPath)
		return beadsDir
	}

	return ResolveBeadsDirWithDepth(resolved, 3)
}

// ResolveBeadsDirWithDepth follows redirect chains with a depth limit.
func ResolveBeadsDirWithDepth(beadsDir string, maxDepth int) string {
	if maxDepth <= 0 {
		fmt.Fprintf(os.Stderr, "Warning: redirect chain too deep at %s, stopping\n", beadsDir)
		return beadsDir
	}

	redirectPath := filepath.Join(beadsDir, "redirect")
	data, err := os.ReadFile(redirectPath)
	if err != nil {
		return beadsDir
	}

	redirectTarget := strings.TrimSpace(string(data))
	if redirectTarget == "" {
		return beadsDir
	}

	workDir := filepath.Dir(beadsDir)
	var resolved string
	if filepath.IsAbs(redirectTarget) {
		resolved = filepath.Clean(redirectTarget)
	} else {
		resolved = filepath.Clean(filepath.Join(workDir, redirectTarget))
	}

	if resolved == beadsDir {
		fmt.Fprintf(os.Stderr, "Warning: circular redirect detected in %s, stopping and removing stale file\n", redirectPath)
		_ = os.Remove(redirectPath)
		return beadsDir
	}

	return ResolveBeadsDirWithDepth(resolved, maxDepth-1)
}
