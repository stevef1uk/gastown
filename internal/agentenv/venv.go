package agentenv

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const DefaultPythonVenvDir = ".venv"

// VenvPython returns the python3 binary inside a venv (linux/mac: .venv/bin/python3).
func VenvPython(workDir, venvRel string) string {
	return filepath.Join(workDir, venvRel, "bin", "python3")
}

// EnsureVenv creates workDir/venvRel when missing using hostPython (-m venv).
func EnsureVenv(workDir, venvRel, hostPython string) (venvPython string, created bool, err error) {
	if venvRel == "" {
		return "", false, fmt.Errorf("empty venv dir")
	}
	venvPython = VenvPython(workDir, venvRel)
	if isExecutable(venvPython) {
		return venvPython, false, nil
	}
	host := strings.TrimSpace(hostPython)
	if host == "" {
		host = "python3"
	}
	venvPath := filepath.Join(workDir, venvRel)
	cmd := exec.Command(host, "-m", "venv", venvPath)
	cmd.Dir = workDir
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		return "", false, fmt.Errorf("%w: %s", runErr, strings.TrimSpace(string(out)))
	}
	if !isExecutable(venvPython) {
		return "", true, fmt.Errorf("venv created but %s not executable", venvPython)
	}
	return venvPython, true, nil
}

// ActivateRigVenvIfExists sets VIRTUAL_ENV/PATH/GT_PYTHON3 when workDir/venvRel already exists.
// It does not create a venv (use WithRigVenv in project_setup).
func ActivateRigVenvIfExists(env []string, workDir, venvRel string) []string {
	venvPy := VenvPython(workDir, venvRel)
	if !isExecutable(venvPy) {
		return env
	}
	venvPath, err := filepath.Abs(filepath.Join(workDir, venvRel))
	if err != nil {
		venvPath = filepath.Join(workDir, venvRel)
	}
	env = withEnvKey(env, "VIRTUAL_ENV", venvPath)
	env = prependPathDir(env, filepath.Join(venvPath, "bin"))
	env = withEnvKey(env, "GT_PYTHON3", venvPy)
	return env
}

// WithRigVenv ensures a project venv and returns env with VIRTUAL_ENV, PATH, and GT_PYTHON3 set.
func WithRigVenv(env []string, workDir, venvRel string) ([]string, string, bool, error) {
	env = EnsurePATH(env)
	host := ResolvePython3(env)
	venvPy, created, err := EnsureVenv(workDir, venvRel, host)
	if err != nil {
		return env, host, false, err
	}
	venvPath, err := filepath.Abs(filepath.Join(workDir, venvRel))
	if err != nil {
		venvPath = filepath.Join(workDir, venvRel)
	}
	env = withEnvKey(env, "VIRTUAL_ENV", venvPath)
	env = prependPathDir(env, filepath.Join(venvPath, "bin"))
	env = withEnvKey(env, "GT_PYTHON3", venvPy)
	return env, venvPy, created, nil
}

func prependPathDir(env []string, dir string) []string {
	cur := pathFromEnv(env)
	if cur == "" {
		return withEnvKey(env, "PATH", dir)
	}
	return withEnvKey(env, "PATH", dir+string(os.PathListSeparator)+cur)
}
