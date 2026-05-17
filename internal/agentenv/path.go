// Package agentenv helpers for subprocess environment (PATH, workdirs).
package agentenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// EnsurePATH returns env with a PATH that can find host tools (python3, pip, pytest, go, gt).
// Existing PATH entries are preserved; standard dirs and discovered tool directories are prepended.
func EnsurePATH(env []string) []string {
	seen := map[string]bool{}
	var parts []string

	addDir := func(dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			return
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			abs = dir
		}
		if seen[abs] {
			return
		}
		seen[abs] = true
		parts = append(parts, abs)
	}

	if home := homeDir(env); home != "" {
		addDir(filepath.Join(home, ".local", "bin"))
		addDir(filepath.Join(home, "go", "bin"))
		addDir(filepath.Join(home, ".pyenv", "shims"))
		addDir(filepath.Join(home, ".pyenv", "bin"))
	}
	for _, name := range []string{"python3", "go", "pip", "pip3", "pytest"} {
		if p, err := exec.LookPath(name); err == nil {
			addDir(filepath.Dir(p))
		}
	}
	for _, dir := range []string{"/usr/local/bin", "/usr/bin", "/bin", "/sbin"} {
		addDir(dir)
	}
	if extra := os.Getenv("GT_PATH_EXTRA"); extra != "" {
		for _, part := range strings.Split(extra, string(os.PathListSeparator)) {
			addDir(part)
		}
	}

	cur := pathFromEnv(env)
	for _, part := range strings.Split(cur, string(os.PathListSeparator)) {
		addDir(part)
	}
	return withEnvKey(env, "PATH", strings.Join(parts, string(os.PathListSeparator)))
}

func pathFromEnv(env []string) string {
	prefix := "PATH="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return strings.TrimPrefix(e, prefix)
		}
	}
	return os.Getenv("PATH")
}

func withEnvKey(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, e := range env {
		if !strings.HasPrefix(e, prefix) {
			out = append(out, e)
		}
	}
	return append(out, prefix+value)
}
