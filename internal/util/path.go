package util

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

var (
	homeDir     string
	homeDirOnce sync.Once
)

// cachedHomeDir returns the user's home directory, cached after the first call.
func cachedHomeDir() string {
	homeDirOnce.Do(func() {
		homeDir, _ = os.UserHomeDir()
	})
	return homeDir
}

// ExpandHome expands a leading ~/ to the user's home directory.
// Returns the path unchanged if it doesn't start with ~/ or if
// the home directory cannot be determined.
func ExpandHome(path string) string {
	if !strings.HasPrefix(path, "~/") {
		return path
	}
	home := cachedHomeDir()
	if home == "" {
		return path
	}
	return home + path[1:]
}

// CanonicalPath returns an absolute path with symlinks evaluated.
// On macOS this normalizes /var/... and /private/var/... to the same string.
func CanonicalPath(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return abs, nil
	}
	return resolved, nil
}

// PathsEqual reports whether two paths refer to the same location after CanonicalPath.
func PathsEqual(a, b string) bool {
	ca, err := CanonicalPath(a)
	if err != nil {
		ca = a
	}
	cb, err := CanonicalPath(b)
	if err != nil {
		cb = b
	}
	return ca == cb
}
