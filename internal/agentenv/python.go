package agentenv

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ResolvePython3 picks a Python interpreter for orchestrated subprocesses.
// Prefers GT_PYTHON3, then ~/.pyenv/shims/python3, then the first python3 on PATH.
func ResolvePython3(env []string) string {
	if v := envVal(env, "GT_PYTHON3"); v != "" {
		return v
	}
	env = EnsurePATH(env)
	if home := homeDir(env); home != "" {
		pyenv := filepath.Join(home, ".pyenv", "shims", "python3")
		if isExecutable(pyenv) {
			return pyenv
		}
	}
	if p, err := lookPathInEnv(env, "python3"); err == nil {
		return p
	}
	return "python3"
}

// WithPython3 adds GT_PYTHON3 to env using ResolvePython3.
func WithPython3(env []string) []string {
	py := ResolvePython3(env)
	return withEnvKey(env, "GT_PYTHON3", py)
}

// RewritePython3InCommand substitutes bare python3 invocations with the resolved interpreter path.
// Heredoc bodies (e.g. cat > requirements.txt <<'EOF') are left unchanged so file contents are not corrupted.
func RewritePython3InCommand(cmd, python string) string {
	if python == "" || python == "python3" {
		return cmd
	}
	if strings.Contains(cmd, "<<") {
		return rewritePython3OutsideHeredocs(cmd, python)
	}
	return rewritePython3InShell(cmd, python)
}

func rewritePython3InShell(cmd, python string) string {
	if strings.Contains(cmd, python) {
		return cmd
	}
	quoted := shellQuote(python)
	re := regexp.MustCompile(`(?i)(^|[;&|]\s*|\s+)python3\b`)
	return re.ReplaceAllString(cmd, `${1}`+quoted)
}

var heredocStartRE = regexp.MustCompile(`<<\s*['"]?([A-Za-z0-9_]+)['"]?`)

func rewritePython3OutsideHeredocs(cmd, python string) string {
	lines := strings.Split(cmd, "\n")
	out := make([]string, 0, len(lines))
	inHeredoc := false
	var delim string
	for _, line := range lines {
		if !inHeredoc {
			if m := heredocStartRE.FindStringSubmatch(line); len(m) > 1 {
				delim = m[1]
				inHeredoc = true
				out = append(out, rewritePython3InShell(line, python))
				continue
			}
			out = append(out, rewritePython3InShell(line, python))
			continue
		}
		if strings.TrimSpace(line) == delim {
			inHeredoc = false
			delim = ""
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func homeDir(env []string) string {
	if h := envVal(env, "HOME"); h != "" {
		return h
	}
	home, _ := os.UserHomeDir()
	return home
}

func envVal(env []string, key string) string {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return strings.TrimPrefix(e, prefix)
		}
	}
	return os.Getenv(key)
}

func lookPathInEnv(env []string, name string) (string, error) {
	path := pathFromEnv(env)
	for _, dir := range strings.Split(path, string(os.PathListSeparator)) {
		if dir == "" {
			continue
		}
		full := filepath.Join(dir, name)
		if isExecutable(full) {
			return full, nil
		}
	}
	return "", os.ErrNotExist
}

func isExecutable(path string) bool {
	st, err := os.Stat(path)
	if err != nil || st.IsDir() {
		return false
	}
	return st.Mode()&0111 != 0
}

func shellQuote(s string) string {
	if s == "" || strings.ContainsAny(s, " \t\n'\"$\\") {
		return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
	}
	return s
}
