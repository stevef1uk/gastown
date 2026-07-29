package orchestrator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	smokeServerHeadingRE = regexp.MustCompile(`(?im)^##\s+.*\bruntime\s+smoke\s+server\b`)
	pythonServerSegmentRE = regexp.MustCompile(
		`(?i)(?:^|&&\s*)(` +
			`(?:\./)?\.venv/bin/python3\s+-m\s+(?:uvicorn|hypercorn)\s+[^\s&;|]+(?:\s+[^\s&;|]+)*|` +
			`python3?\s+-m\s+(?:uvicorn|hypercorn)\s+[^\s&;|]+(?:\s+[^\s&;|]+)*|` +
			`uvicorn\s+[^\s&;|]+(?:\s+[^\s&;|]+)*|` +
			`gunicorn\s+[^\s&;|]+(?:\s+[^\s&;|]+)*|` +
			`flask\s+run(?:\s+[^\s&;|]+)*` +
			`)`,
	)
)

// WorkflowNeedsRuntimeSmoke reports profiles that run doc-derived HTTP smoke during implementation verify.
func WorkflowNeedsRuntimeSmoke(townRoot, rig string, v WorkflowValidation) bool {
	// Only run runtime smoke in the final delivery phase — earlier phases haven't
	// wired up the server yet.
	if v.HasPhasedDelivery() && !v.IsFinalDeliveryPhase() {
		return false
	}
	if workflowHasGoWebAndServer(v) {
		return true
	}
	if townRoot == "" || rig == "" {
		return false
	}
	return pythonWorkflowNeedsRuntimeSmoke(townRoot, rig, v)
}

// pythonWorkflowHasServerEntry reports required_files suggest a long-running HTTP server (not library-only).
func pythonWorkflowHasServerEntry(v WorkflowValidation) bool {
	if !WorkflowUsesPython(v) {
		return false
	}
	for _, f := range v.RequiredFilesForSmokeScope() {
		lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(f)))
		switch {
		case strings.HasSuffix(lower, "app.py"),
			strings.HasSuffix(lower, "main.py"),
			strings.Contains(lower, "/api/"),
			strings.Contains(lower, "wsgi"),
			strings.Contains(lower, "asgi"),
			strings.HasSuffix(lower, "server.py"):
			return true
		}
	}
	return false
}

func pythonWorkflowNeedsRuntimeSmoke(townRoot, rig string, v WorkflowValidation) bool {
	if !pythonWorkflowHasServerEntry(v) {
		return false
	}
	spec, err := LoadAPISmokeSpecFromRig(townRoot, rig, v)
	if err != nil {
		return false
	}
	return specHasRuntimeSmokeProbes(spec)
}

func specHasRuntimeSmokeProbes(spec APISmokeSpec) bool {
	if APISmokeHasHTTPAPI(spec) || len(spec.StaticAssets) > 0 || smokeHasNonRootGETProbes(spec) {
		return true
	}
	return spec.hasRootGET()
}

var uvicornServerRE = regexp.MustCompile(`(?i)\buvicorn\s+\S+:\S+`)

// IsDevServerSmokeCommand reports agent CMDs that start a local HTTP server (Go or Python).
// Uses module:app syntax to distinguish running a server from pip install / python -c imports.
func IsDevServerSmokeCommand(cmd string) bool {
	lower := strings.ToLower(strings.TrimSpace(cmd))
	if strings.Contains(lower, "go run") && strings.Contains(lower, "cmd/server") {
		return true
	}
	if strings.Contains(lower, "pytest") {
		return false
	}
	if strings.Contains(lower, "pip ") || strings.Contains(lower, "pip3") || strings.Contains(lower, "python -c") || strings.Contains(lower, "python3 -c") {
		return false
	}
	return uvicornServerRE.MatchString(lower) ||
		strings.Contains(lower, "gunicorn") ||
		strings.Contains(lower, "flask run") ||
		strings.Contains(lower, "hypercorn")
}

func parseSmokeServerSection(text string) string {
	loc := smokeServerHeadingRE.FindStringIndex(text)
	if loc == nil {
		return ""
	}
	rest := text[loc[1]:]
	if i := strings.Index(rest, "\n## "); i >= 0 {
		rest = rest[:i]
	}
	for _, line := range strings.Split(rest, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if m := smokeResetBulletRE.FindStringSubmatch(line); len(m) >= 2 {
			line = strings.TrimSpace(m[1])
		}
		if strings.HasPrefix(strings.ToLower(line), "smoke_reset:") {
			continue
		}
		return trimSmokeServerCommand(line)
	}
	return ""
}

func trimSmokeServerCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return ""
	}
	if i := strings.Index(cmd, "&"); i > 0 {
		cmd = strings.TrimSpace(cmd[:i])
	}
	for _, sep := range []string{"&&", "||", "; curl", " curl ", "`", "\n", `"`} {
		if i := strings.Index(strings.ToLower(cmd), sep); i > 0 {
			cmd = strings.TrimSpace(cmd[:i])
		}
	}
	cmd = stripTrailingParenthesizedComment(cmd)
	return cmd
}

func stripTrailingParenthesizedComment(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	idx := strings.LastIndex(cmd, " (")
	if idx < 0 || !strings.HasSuffix(cmd, ")") {
		return cmd
	}
	comment := strings.TrimSpace(cmd[idx+2 : len(cmd)-1])
	if comment == "" {
		return cmd
	}
	if strings.ContainsAny(comment, ";&|$<>`\n") {
		return cmd
	}
	if !strings.ContainsAny(comment, " \t") {
		return cmd
	}
	return strings.TrimSpace(cmd[:idx])
}

func extractPythonServerStartFromQA(v WorkflowValidation) string {
	for _, src := range []string{v.QAVerifyCommand, v.ActivePhaseQAVerifyCommand()} {
		if cmd := ExtractPythonServerStartFromText(src); cmd != "" {
			return cmd
		}
	}
	return ""
}

func ExtractPythonServerStartFromText(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if m := pythonServerSegmentRE.FindStringSubmatch(" " + strings.ReplaceAll(text, "&&", " && ")); len(m) >= 2 {
		return trimSmokeServerCommand(m[1])
	}
	for _, part := range strings.Split(text, "&&") {
		part = strings.TrimSpace(part)
		pl := strings.ToLower(part)
		if strings.Contains(pl, "pytest") && !strings.Contains(pl, "uvicorn") {
			continue
		}
		if strings.Contains(pl, "uvicorn") || strings.Contains(pl, "gunicorn") ||
			strings.Contains(pl, "flask run") || strings.Contains(pl, "hypercorn") {
			return trimSmokeServerCommand(part)
		}
	}
	return ""
}

// ImplementationPythonModuleOK runs profile pytest/unittest before HTTP smoke (Python rigs).
func ImplementationPythonModuleOK(rigDir string, v WorkflowValidation) error {
	if !WorkflowUsesPython(v) {
		return nil
	}
	verify := strings.TrimSpace(PythonVerifyCommand(v))
	if verify == "" {
		return nil
	}
	cmd := exec.Command("/bin/bash", "-c", verify)
	cmd.Dir = rigDir
	cmd.Env = os.Environ()
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		text := strings.TrimSpace(string(out))
		if PythonVerifyNoTestsOK(text) {
			return nil
		}
		if text == "" {
			text = runErr.Error()
		}
		return fmt.Errorf("python module verify failed: %w\n%s", runErr, text)
	}
	return nil
}

// PythonVerifyNoTestsOK reports whether a failed pytest run is acceptable because
// the test file simply hasn't been written yet (test bead not yet active). This is
// not a code failure — it just means "no tests collected / no tests ran".
func PythonVerifyNoTestsOK(text string) bool {
	lower := strings.ToLower(text)
	if !strings.Contains(lower, "no tests ran") && !strings.Contains(lower, "collected 0 items") {
		return false
	}
	for _, pat := range pythonVenvCorruptionPatterns {
		if strings.Contains(lower, strings.ToLower(pat)) {
			return false
		}
	}
	for _, pat := range []string{"syntaxerror", "nameerror", "indentationerror",
		"typeerror", "attributeerror", "keyerror", "valueerror", "recursionerror"} {
		if strings.Contains(lower, pat) {
			return false
		}
	}
	return true
}

// PythonTestsAllPassed reports whether a failed pytest run is a false positive:
// all collected tests PASSED but the exit code is non-zero (e.g. due to
// DeprecationWarning from a dependency). Only true when every collected
// test passed and none failed.
func PythonTestsAllPassed(text string) bool {
	lower := strings.ToLower(text)
	if strings.Contains(lower, "failed") {
		return false
	}
	if !strings.Contains(lower, "passed") {
		return false
	}
	if strings.Contains(lower, "collected 0 items") || strings.Contains(lower, "no tests ran") {
		return false
	}
	for _, pat := range pythonVenvCorruptionPatterns {
		if strings.Contains(lower, strings.ToLower(pat)) {
			return false
		}
	}
	for _, pat := range []string{"syntaxerror", "nameerror", "indentationerror",
		"typeerror", "attributeerror", "keyerror", "valueerror", "recursionerror",
		"error:", "traceback"} {
		if strings.Contains(lower, pat) {
			return false
		}
	}
	return true
}

var pythonVenvCorruptionPatterns = []string{
	"ModuleNotFoundError:",
	"ImportError:",
	"No module named",
	"cannot import name",
}

// PipOutputIndicatesBrokenVenv reports whether pip's output shows that the pip
// installation itself is broken (e.g. ModuleNotFoundError in pip internals),
// as opposed to a missing dependency.
func PipOutputIndicatesBrokenVenv(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "modulenotfounderror") && strings.Contains(lower, "pip._internal")
}

// pythonVerifyNeedsVenvRebuild reports whether a phase verify failure looks like
// a corrupted/missing venv rather than a code bug.
func pythonVerifyNeedsVenvRebuild(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, pat := range pythonVenvCorruptionPatterns {
		if strings.Contains(msg, pat) {
			return true
		}
	}
	return false
}

// RecoverPythonVenvAndRetry deletes and recreates the Python venv and pip-installs from
// requirements.txt. Returns a log line and true if recovery succeeded (verify should retry).
func RecoverPythonVenvAndRetry(townRoot, rig string, v WorkflowValidation, originalErr error) (string, bool) {
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	venvPath := filepath.Join(rigDir, strings.Trim(v.PythonVenvRelDir(), "/"))
	reqPath := filepath.Join(rigDir, v.RequirementsFilePath())
	if _, err := os.Stat(reqPath); err != nil {
		return fmt.Sprintf("venv corrupted but %s missing — cannot rebuild", v.RequirementsFilePath()), false
	}
	if err := os.RemoveAll(venvPath); err != nil && !os.IsNotExist(err) {
		return fmt.Sprintf("venv rebuild: cannot remove %s: %v", venvPath, err), false
	}
	// Use the host python3 (not the venv's broken python) to recreate the venv.
	cleanEnv := cleanVenvEnv(os.Environ(), venvPath)
	hostPython := findHostPython3(cleanEnv)
	create := exec.Command("/bin/bash", "-c", hostPython+" -m venv "+shellescape(venvPath))
	create.Dir = rigDir
	create.Env = cleanEnv
	if out, err := create.CombinedOutput(); err != nil {
		return fmt.Sprintf("venv rebuild: %s -m venv failed: %v\n%s", hostPython, err, string(out)), false
	}
	installCmd := PipInstallRequirementsCmd(venvPath+"/bin/pip", reqPath)
	install := exec.Command("/bin/bash", "-c", installCmd)
	install.Dir = rigDir
	install.Env = cleanVenvEnv(os.Environ(), venvPath)
	if out, err := install.CombinedOutput(); err != nil {
		return fmt.Sprintf("venv rebuild: pip install failed: %v\n%s", err, string(out)), false
	}
	return fmt.Sprintf("rebuilt venv (ModuleNotFoundError — pip conflict detected and resolved)"), true
}

// findHostPython3 finds a python3 from the given env's PATH.
// Returns "python3" as fallback.
func findHostPython3(env []string) string {
	path := ""
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			path = e[len("PATH="):]
			break
		}
	}
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			continue
		}
		py := filepath.Join(dir, "python3")
		st, err := os.Stat(py)
		if err == nil && !st.IsDir() && st.Mode()&0111 != 0 {
			return py
		}
	}
	return "python3"
}

// cleanVenvEnv returns a copy of env with VIRTUAL_ENV, GT_PYTHON3 removed and .venv/bin
// directories stripped from PATH, so subprocesses use the host python3.
func cleanVenvEnv(env []string, venvPath string) []string {
	venvBin := filepath.Join(venvPath, "bin") + string(os.PathListSeparator)
	out := make([]string, 0, len(env))
	for _, e := range env {
		if strings.HasPrefix(e, "VIRTUAL_ENV=") || strings.HasPrefix(e, "GT_PYTHON3=") {
			continue
		}
		if strings.HasPrefix(e, "PATH=") {
			cleaned := stripPathEntry(e[len("PATH="):], venvBin)
			out = append(out, "PATH="+cleaned)
			continue
		}
		out = append(out, e)
	}
	return out
}

func stripPathEntry(path, entry string) string {
	dirs := filepath.SplitList(path)
	cleaned := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if d == "" || strings.HasPrefix(d, entry) {
			continue
		}
		cleaned = append(cleaned, d)
	}
	return strings.Join(cleaned, string(os.PathListSeparator))
}

// shellescape wraps a path in single quotes for safe shell use.
func shellescape(s string) string {
	if !strings.ContainsAny(s, " \t\n'\"") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
