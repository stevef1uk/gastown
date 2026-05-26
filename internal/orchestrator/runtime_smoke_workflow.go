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
	for _, f := range append(append([]string(nil), v.RequiredFiles...), v.UnionRequiredFiles()...) {
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

// IsDevServerSmokeCommand reports agent CMDs that start a local HTTP server (Go or Python).
func IsDevServerSmokeCommand(cmd string) bool {
	lower := strings.ToLower(strings.TrimSpace(cmd))
	if strings.Contains(lower, "go run") && strings.Contains(lower, "cmd/server") {
		return true
	}
	if strings.Contains(lower, "pytest") && !strings.Contains(lower, "uvicorn") &&
		!strings.Contains(lower, "gunicorn") && !strings.Contains(lower, "flask run") {
		return false
	}
	return strings.Contains(lower, "uvicorn") ||
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
	for _, sep := range []string{"&&", "||", "; curl", " curl "} {
		if i := strings.Index(strings.ToLower(cmd), sep); i > 0 {
			cmd = strings.TrimSpace(cmd[:i])
		}
	}
	return cmd
}

func extractPythonServerStartFromQA(v WorkflowValidation) string {
	for _, src := range []string{v.QAVerifyCommand, v.ActivePhaseQAVerifyCommand()} {
		if cmd := extractPythonServerStartFromText(src); cmd != "" {
			return cmd
		}
	}
	return ""
}

func extractPythonServerStartFromText(text string) string {
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
		if text == "" {
			text = runErr.Error()
		}
		return fmt.Errorf("python module verify failed: %w\n%s", runErr, text)
	}
	return nil
}
