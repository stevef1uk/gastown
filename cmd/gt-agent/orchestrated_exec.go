package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

// needsOrchestratedScriptFile reports commands that must not be passed to sh -c as one line.
func needsOrchestratedScriptFile(cmd string) bool {
	return strings.Contains(cmd, "\n") || strings.Contains(cmd, "<<")
}

// prepareOrchestratedScript turns a model CMD block into a bash script body.
func prepareOrchestratedScript(cmd string) string {
	body := unwrapBashLcMultiline(strings.TrimSpace(cmd))
	body = strings.ReplaceAll(body, `\$`, "$")
	body = normalizeHeredocDelimiters(body)
	body = filterHallucinatedScriptLines(body)
	body = scrubOrphanHeredocDelimiterLines(body)
	body = stripMarkdownFencesInHeredocScripts(body)
	return scrubOrphanBashLcQuoteLines(body)
}

// stripMarkdownFencesInHeredocScripts removes ```lang / ``` wrappers inside heredoc bodies.
func stripMarkdownFencesInHeredocScripts(script string) string {
	lines := strings.Split(script, "\n")
	var out []string
	inBody := false
	var term string
	var bodyLines []string
	flushBody := func() {
		if len(bodyLines) == 0 {
			return
		}
		stripped := sanitizeNativeFileContent(strings.Join(bodyLines, "\n"))
		out = append(out, strings.Split(stripped, "\n")...)
		bodyLines = nil
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if inBody {
			if trimmed == term {
				flushBody()
				out = append(out, line)
				inBody = false
				term = ""
				continue
			}
			bodyLines = append(bodyLines, line)
			continue
		}
		out = append(out, line)
		if t := detectHeredocTerm(line); t != "" {
			inBody = true
			term = t
			bodyLines = nil
		}
	}
	if inBody {
		flushBody()
	}
	return strings.Join(out, "\n")
}

// scrubOrphanBashLcQuoteLines drops stray wrapper quote lines left after unwrapBashLcMultiline
// (e.g. bash -lc 'cat <<'EOF' … EOF' leaves a lone ' line that breaks bash -e scripts).
func scrubOrphanBashLcQuoteLines(body string) string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if t == `'` || t == `"` {
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// scrubOrphanHeredocDelimiterLines drops stray EOF/EOT/END lines the model emits after a closed heredoc.
func scrubOrphanHeredocDelimiterLines(body string) string {
	var out []string
	inHeredoc := false
	var term string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if inHeredoc {
			out = append(out, line)
			if trimmed == term {
				inHeredoc = false
				term = ""
			}
			continue
		}
		if t := detectHeredocTerm(line); t != "" {
			inHeredoc = true
			term = t
			out = append(out, line)
			continue
		}
		if isStandaloneHeredocDelimiter(trimmed) {
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func isStandaloneHeredocDelimiter(s string) bool {
	t := strings.TrimSpace(s)
	switch strings.ToUpper(t) {
	case "EOF", "EOT", "END":
		return true
	}
	if isNativeEditEndMarker(t) || isOrchestratedNativeToolLine(t) {
		return true
	}
	return false
}

// benignPlanningShellNoise reports harmless planner mistakes (e.g. a lone EOF CMD after heredoc).
func benignPlanningShellNoise(cmd string, cmdErr error) bool {
	if cmdErr == nil {
		return false
	}
	var exitErr *exec.ExitError
	if !errors.As(cmdErr, &exitErr) || exitErr.ExitCode() != 127 {
		return false
	}
	t := strings.TrimSpace(cmd)
	return isStandaloneHeredocDelimiter(t)
}

// filterHallucinatedScriptLines drops model junk glued onto shell scripts.
func filterHallucinatedScriptLines(body string) string {
	var kept []string
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if isOrchestratedNativeToolLine(line) {
			continue
		}
		if strings.Contains(strings.ToUpper(t), "[TOOL_CALLS]") {
			continue
		}
		if looksLikeHallucinatedShellOutput(t) {
			continue
		}
		if outcomeJSONLeadingColonRE.MatchString(t) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// unwrapBashLcMultiline strips bash -lc '...' / "..." wrappers from multiline agent commands.
// Multiline heredocs often omit the closing wrapper quote; only strip a closing quote when
// it is the final character (or a lone quote line at the end).
func unwrapBashLcMultiline(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	const prefix = "bash -lc "
	if !strings.HasPrefix(cmd, prefix) {
		return cmd
	}
	inner := strings.TrimSpace(cmd[len(prefix):])
	if len(inner) == 0 {
		return inner
	}
	q := inner[0]
	if q != '\'' && q != '"' {
		return inner
	}
	inner = inner[1:]
	inner = strings.TrimSpace(inner)
	if len(inner) > 0 && inner[len(inner)-1] == q {
		inner = strings.TrimSpace(inner[:len(inner)-1])
	}
	lines := strings.Split(inner, "\n")
	for len(lines) > 0 {
		last := strings.TrimSpace(lines[len(lines)-1])
		if last == string(q) {
			lines = lines[:len(lines)-1]
			continue
		}
		break
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func bashLcHeredocEOFMarker() string {
	// bash -lc quoting trick for a single-quoted EOF delimiter: <<'"'"'EOF'"'"'
	bashSingleQuote := string([]byte{'\'', '"', '\'', '"', '\''})
	return "<<" + bashSingleQuote + "EOF" + bashSingleQuote
}

func normalizeHeredocDelimiters(body string) string {
	plain := "<<" + "'" + "EOF" + "'"
	return strings.ReplaceAll(body, bashLcHeredocEOFMarker(), plain)
}

// isToolchainExecutionCommand reports shell commands that run pip/pytest/unittest (not file writes).
func isToolchainExecutionCommand(cmd string) bool {
	lower := strings.ToLower(cmd)
	if strings.Contains(lower, "<<") {
		return false
	}
	if strings.Contains(lower, "cat >") || strings.Contains(lower, "cat>>") {
		return false
	}
	if strings.Contains(lower, "-m pip") || strings.Contains(lower, "pip install") || strings.Contains(lower, "pip3 install") {
		return true
	}
	if strings.Contains(lower, "-m compileall") || strings.Contains(lower, "compileall") {
		return true
	}
	if strings.Contains(lower, "-m pytest") || strings.Contains(lower, "-m unittest") {
		return true
	}
	if strings.Contains(lower, "unittest") {
		return true
	}
	if strings.Contains(lower, "pytest") {
		return strings.Contains(lower, "cd ") || strings.Contains(lower, " -q") ||
			strings.Contains(lower, " -v") || strings.Contains(lower, " -k")
	}
	if strings.Contains(lower, "go test") || strings.Contains(lower, "go run") ||
		strings.Contains(lower, "go build") || strings.Contains(lower, "go vet") ||
		strings.Contains(lower, "go mod") {
		return true
	}
	return false
}

// isGoDevServerSmokeCommand reports go run ./cmd/server (with or without curl).
func isGoDevServerSmokeCommand(cmd string) bool {
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "go run") && strings.Contains(lower, "cmd/server")
}

// isDevServerSmokeCommand reports Go or Python local server CMDs that gt-agent can rewrite to doc-derived curls.
func isDevServerSmokeCommand(cmd string) bool {
	return orchestrator.IsDevServerSmokeCommand(cmd)
}

// wrapStrictBashSmoke prefixes agent-invented go run+curl chains with set -euo pipefail
// so a failed probe (e.g. GET / 404) cannot be masked by a later passing curl (GT-VERIFY-003).
func wrapStrictBashSmoke(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return cmd
	}
	lower := strings.ToLower(cmd)
	if strings.HasPrefix(lower, "set -e") {
		return cmd
	}
	return "set -euo pipefail; " + cmd
}

// writesRequirementsFile reports commands that create/overwrite requirements.txt (heredoc or redirect).
func writesRequirementsFile(cmd string) bool {
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "requirements.txt") &&
		(strings.Contains(lower, "<<") || strings.Contains(lower, "cat >") || strings.Contains(lower, "cat>>"))
}

// rewriteUnittestToWorkdir prepends cd into mayor/rig (and layout_root for Go modules) when omitted.
func rewriteUnittestToWorkdir(cmd, rig string, v orchestrator.WorkflowValidation) (string, bool) {
	if !isToolchainExecutionCommand(cmd) {
		return cmd, false
	}
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	// Profile-relative "cd linkshelf && go run …" smoke must not get a second prefix; bare
	// "go run ./cmd/server" from town root still needs cd into layout_root (see goLayout test).
	if isDevServerSmokeCommand(cmd) && (commandHasMayorRigCD(cmd, rig) || commandHasLayoutCD(cmd, layout)) {
		return cmd, false
	}
	changed := false
	if !orchestrator.IsPythonImportCheckCommand(cmd) {
		if fixed := orchestrator.NormalizePytestCommand(cmd); fixed != cmd {
			cmd = fixed
			changed = true
		}
	}
	if fixed := orchestrator.NormalizePipCommand(cmd); fixed != cmd {
		cmd = fixed
		changed = true
	}
	mayorRig := rigMayorRigPath(rig)
	// Python venv lives under mayor/rig only — never cd into layout_root for pip/pytest/compileall.
	workPath := mayorRig
	if orchestrator.WorkflowUsesGo(v) {
		workPath = orchestrator.GoModuleWorkPathRelative(mayorRig, layout)
	}
	if !commandHasMayorRigCD(cmd, rig) {
		if !commandHasLayoutCD(cmd, layout) {
			cmd = "cd " + workPath + " && " + stripLeadingCDDot(strings.TrimSpace(cmd))
			changed = true
		} else if layout != "" && layout != "." {
			// Profile verify uses bare "cd layout" (mayor/rig-relative); orchestrated cwd is town root.
			rest := stripFirstCDPrefix(cmd)
			cmd = "cd " + workPath + " && " + rest
			changed = true
		}
	} else if orchestrator.WorkflowUsesGo(v) && layout != "" && layout != "." &&
		commandHasMayorRigCD(cmd, rig) && !commandHasLayoutCD(cmd, layout) {
		// Already under mayor/rig but not in layout module dir — one cd to module root.
		rest := stripFirstCDPrefix(cmd)
		cmd = "cd " + workPath + " && " + rest
		changed = true
	}
	return cmd, changed
}

// stripLeadingCDDot removes a leading "cd . &&" when mayor/rig workdir is prepended separately.
func stripLeadingCDDot(cmd string) string {
	trimmed := strings.TrimSpace(cmd)
	lower := strings.ToLower(trimmed)
	for _, p := range []string{"cd . && ", "cd ./ && "} {
		if strings.HasPrefix(lower, p) {
			return strings.TrimSpace(trimmed[len(p):])
		}
	}
	return trimmed
}

// stripFirstCDPrefix removes a leading "cd <path> &&" so rewrite can replace with one module cd.
func stripFirstCDPrefix(cmd string) string {
	trimmed := strings.TrimSpace(cmd)
	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, "cd ") {
		return trimmed
	}
	if idx := strings.Index(lower, " && "); idx >= 0 {
		return strings.TrimSpace(trimmed[idx+4:])
	}
	return ""
}

func commandHasLayoutCD(cmd, layout string) bool {
	layout = strings.Trim(strings.TrimSpace(layout), "/")
	if layout == "" {
		return false
	}
	lower := strings.ToLower(cmd)
	layoutLower := strings.ToLower(layout)
	return strings.Contains(lower, "cd "+layoutLower) ||
		strings.Contains(lower, "cd ./"+layoutLower) ||
		strings.Contains(lower, "/"+layoutLower+"/") ||
		strings.Contains(lower, "/"+layoutLower+" &&") ||
		strings.Contains(lower, "/"+layoutLower+" ")
}

// commandHasMayorRigCD reports whether cmd already cds into the rig mayor/rig worktree.
func commandHasMayorRigCD(cmd, rig string) bool {
	lower := strings.ToLower(cmd)
	if !strings.Contains(lower, "cd ") {
		return false
	}
	work := strings.ToLower(rigMayorRigPath(rig))
	if strings.Contains(lower, "cd "+work) {
		return true
	}
	// Model may use ~/gt/<rig>/mayor/rig or $GT_ROOT/<rig>/mayor/rig.
	if strings.Contains(lower, "/mayor/rig") {
		rigLower := strings.ToLower(strings.TrimSpace(rig))
		if rigLower != "" && strings.Contains(lower, rigLower+"/mayor/rig") {
			return true
		}
	}
	return false
}

// rewriteBdListImplementScope scopes bd list to implement beads and includes in_progress work.
func rewriteBdListImplementScope(cmd, titleContains string) (string, bool) {
	titleContains = strings.TrimSpace(titleContains)
	if titleContains == "" {
		return cmd, false
	}
	lower := strings.ToLower(cmd)
	if !strings.Contains(lower, "bd list") || strings.Contains(lower, "grep") {
		return cmd, false
	}
	if !strings.Contains(lower, "--status=open") && !strings.Contains(lower, "--status=in_progress") &&
		!strings.Contains(lower, "--status=closed") {
		return cmd, false
	}
	out := strings.TrimSpace(cmd)
	if strings.Contains(lower, "--status=open") && !strings.Contains(lower, "--status=in_progress") {
		out = strings.Replace(out, "--status=open", "--status=open,in_progress", 1)
		out = strings.Replace(out, "--status open", "--status open,in_progress", 1)
	}
	q := "'" + strings.ReplaceAll(titleContains, "'", `'"'"'`) + "'"
	return out + " | grep -Fi " + q + " || true", true
}

// isScopedImplementBdListGrep reports bd list output filtered to implement bead titles.
func isScopedImplementBdListGrep(cmd string) bool {
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "bd list") && strings.Contains(lower, "grep -fi")
}

// isScopedImplementBdListEmpty is true when scoped grep found no matching beads (exit 1).
func isScopedImplementBdListEmpty(cmd string, cmdErr error) bool {
	if cmdErr == nil || !isScopedImplementBdListGrep(cmd) {
		return false
	}
	var exitErr *exec.ExitError
	if !errors.As(cmdErr, &exitErr) || exitErr.ExitCode() != 1 {
		return false
	}
	return true
}

// orchestratedWritesGoUnderLayout reports heredoc/redirect commands that write .go under layout_root.
func orchestratedWritesGoUnderLayout(cmd string, v orchestrator.WorkflowValidation) bool {
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" {
		return false
	}
	lower := strings.ToLower(cmd)
	if !strings.Contains(lower, ".go") {
		return false
	}
	if !strings.Contains(lower, "<<") && !strings.Contains(lower, "cat >") && !strings.Contains(lower, "cat>>") {
		return false
	}
	return strings.Contains(lower, layout)
}

// rewriteBdListLimit ensures bd list counts are not capped at 50 (beads default).
func rewriteBdListLimit(cmd string) (string, bool) {
	return sanitizeBdListCommand(cmd)
}

var (
	goSmokeStripPkillRE  = regexp.MustCompile(`(?i)\s*&&\s*pkill\s+-f\s+[^&|;]+`)
	goSmokeStripBuildRE  = regexp.MustCompile(`(?i)go\s+build\s+\./\.\.\.\s*&&\s*`)
	goSmokeStripTidyRE   = regexp.MustCompile(`(?i)go\s+mod\s+tidy\s*&&\s*`)
	goSmokeServerBgRE    = regexp.MustCompile(`(?i)(go\s+run\s+(?:\.\/)?cmd/server[^\s&;]*)\s*&`)
	goSmokeWorkDirRE     = regexp.MustCompile(`(?i)cd\s+([^\s&;]+linkshelf[^\s&;]*)`)
	goSmokeLocalhostRE   = regexp.MustCompile(`(?i)(?:localhost|127\.0\.0\.1):(\d{2,5})`)
)

// normalizeGoCommandTypos fixes common model mistakes in go subcommands (e.g. "go build./...").
func normalizeGoCommandTypos(cmd string) (string, bool) {
	repls := []struct{ old, new string }{
		{"go build./", "go build ./"},
		{"go build..", "go build ./"},
		{"go test./", "go test ./"},
		{"go run./", "go run ./"},
		{"go vet./", "go vet ./"},
		{"-count=1./", "-count=1 ./"},
		{"-count=1..", "-count=1 ./"},
	}
	changed := false
	for _, r := range repls {
		if strings.Contains(cmd, r.old) {
			cmd = strings.ReplaceAll(cmd, r.old, r.new)
			changed = true
		}
	}
	return cmd, changed
}

// normalizeGoDevServerSmokeCommand fixes polecat-invented server smoke chains (Go or Python).
func normalizeGoDevServerSmokeCommand(cmd, townRoot, rig string, v orchestrator.WorkflowValidation) (string, bool) {
	if !isDevServerSmokeCommand(cmd) {
		return cmd, false
	}
	out := cmd
	changed := false
	if orchestrator.WorkflowUsesGo(v) && goSmokeStripPkillRE.MatchString(out) {
		out = goSmokeStripPkillRE.ReplaceAllString(out, "")
		changed = true
	}
	if orchestrator.WorkflowUsesGo(v) && goSmokeStripBuildRE.MatchString(out) {
		out = goSmokeStripBuildRE.ReplaceAllString(out, "")
		changed = true
	}
	if orchestrator.WorkflowUsesGo(v) && goSmokeStripTidyRE.MatchString(out) {
		out = goSmokeStripTidyRE.ReplaceAllString(out, "")
		changed = true
	}
	if short, ok := simplifyDevServerSmoke(out, townRoot, rig, v); ok {
		return short, true
	}
	if strings.Contains(strings.ToLower(out), "curl ") && !strings.Contains(out, "--max-time") {
		for _, pair := range []struct{ old, new string }{
			{"curl -sf ", "curl -sf --max-time 10 "},
			{"curl -sSf ", "curl -sSf --max-time 10 "},
			{"curl -s ", "curl -s --max-time 10 "},
		} {
			if strings.Contains(out, pair.old) {
				out = strings.ReplaceAll(out, pair.old, pair.new)
				changed = true
			}
		}
	}
	if strings.Contains(out, "sleep 2") {
		out = strings.ReplaceAll(out, "sleep 2", "sleep 4")
		changed = true
	}
	if fixed, ok := ensureGoSmokeShellReturns(out); ok {
		out = fixed
		changed = true
	}
	return strings.TrimSpace(out), changed
}

// ensureGoSmokeShellReturns appends kill/wait so sh -c does not block until go run exits.
// Without this, `go run ./cmd/server & … && curl …` waits on the background server forever.
func ensureGoSmokeShellReturns(cmd string) (string, bool) {
	lower := strings.ToLower(cmd)
	if !strings.Contains(lower, "go run") || !strings.Contains(lower, "cmd/server") ||
		!strings.Contains(lower, "&") || !strings.Contains(lower, "curl") {
		return cmd, false
	}
	if strings.Contains(lower, "_gtsrv=") {
		return cmd, false
	}
	out := goSmokeServerBgRE.ReplaceAllString(cmd, `${1} & _gtsrv=$!;`)
	if out == cmd {
		return cmd, false
	}
	out = strings.ReplaceAll(out, "_gtsrv=$! sleep", "_gtsrv=$!; sleep")
	out = strings.ReplaceAll(out, "_gtsrv=$! && sleep", "_gtsrv=$!; sleep")
	// Do not wait on go run — first compile can take minutes; kill the wrapper only.
	return strings.TrimSpace(out) + `; kill ${_gtsrv} 2>/dev/null`, true
}

// simplifyGoDevServerSmoke is an alias for tests and legacy call sites.
func simplifyGoDevServerSmoke(cmd, townRoot, rig string, v orchestrator.WorkflowValidation) (string, bool) {
	return simplifyDevServerSmoke(cmd, townRoot, rig, v)
}

// simplifyDevServerSmoke replaces agent server CMDs with doc-derived background server + curl probes.
func simplifyDevServerSmoke(cmd, townRoot, rig string, v orchestrator.WorkflowValidation) (string, bool) {
	if !isDevServerSmokeCommand(cmd) {
		return cmd, false
	}
	workDir := smokeWorkDirFromCommand(cmd)
	if workDir == "" {
		return cmd, false
	}
	spec, _ := orchestrator.LoadAPISmokeSpecFromRig(townRoot, rig, v)
	built := orchestrator.BuildRuntimeSmokeShell(workDir, spec)
	if built == "" {
		return cmd, false
	}
	return built, true
}

func smokeWorkDirFromCommand(cmd string) string {
	// Join chained cds before go run (cd testgt3/mayor/rig && cd linkshelf → testgt3/mayor/rig/linkshelf).
	var joined string
	for _, p := range strings.Split(cmd, "&&") {
		p = strings.TrimSpace(p)
		lower := strings.ToLower(p)
		if strings.Contains(lower, "go run") || strings.Contains(lower, "uvicorn") ||
			strings.Contains(lower, "gunicorn") || strings.Contains(lower, "flask run") {
			break
		}
		if !strings.HasPrefix(lower, "cd ") {
			continue
		}
		dir := strings.Trim(strings.TrimSpace(p[3:]), `"'`)
		if dir == "" {
			continue
		}
		if joined == "" {
			joined = dir
			continue
		}
		if strings.HasPrefix(dir, "/") || strings.Contains(dir, "mayor/rig") {
			joined = dir
		} else {
			joined = strings.TrimSuffix(joined, "/") + "/" + strings.TrimPrefix(dir, "/")
		}
	}
	if joined != "" {
		return joined
	}
	if m := goSmokeWorkDirRE.FindStringSubmatch(cmd); len(m) >= 2 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func smokeLocalhostPort(cmd string) int {
	if m := goSmokeLocalhostRE.FindStringSubmatch(cmd); len(m) >= 2 {
		if p, err := strconv.Atoi(m[1]); err == nil && p > 0 && p <= 65535 {
			return p
		}
	}
	return 8080
}

// orchestratedCommandTimeout returns a max runtime for long polecat smoke tests.
func orchestratedCommandTimeout(cmd string) time.Duration {
	lower := strings.ToLower(cmd)
	if strings.Contains(lower, "go run") && strings.Contains(lower, "curl") {
		return 3 * time.Minute
	}
	if strings.Contains(lower, "go build ./...") || strings.Contains(lower, "go test ./...") {
		return 5 * time.Minute
	}
	return 0
}

func commandTimeoutDur(cmd string, overrideSec int) time.Duration {
	if overrideSec > 0 {
		return time.Duration(overrideSec) * time.Second
	}
	return orchestratedCommandTimeout(cmd)
}

// formatSuccessCommandOutput makes successful runs visible when tools print nothing (e.g. go mod tidy).
func formatSuccessCommandOutput(out []byte) string {
	if strings.TrimSpace(string(out)) != "" {
		return string(out)
	}
	return "(exit 0, no output)\n"
}

func runOrchestratedCommand(cmd, workDir, sessionName string, env []string, cmdTimeoutSec int) ([]byte, error) {
	if sessionName != "" {
		env = append(env, "GT_SESSION="+sessionName)
	}
	if workDir == "" {
		workDir = "."
	}
	ctx := context.Background()
	if d := commandTimeoutDur(cmd, cmdTimeoutSec); d > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}
	if !needsOrchestratedScriptFile(cmd) {
		shell, flag := "/bin/sh", "-c"
		if isGoDevServerSmokeCommand(cmd) {
			shell, flag = "/bin/bash", "-c"
			cmd = wrapStrictBashSmoke(cmd)
		}
		c := exec.CommandContext(ctx, shell, flag, cmd)
		c.Env = env
		c.Dir = workDir
		out, err := c.CombinedOutput()
		if err != nil && ctx.Err() == context.DeadlineExceeded {
			return out, fmt.Errorf("%w (command exceeded %s)", err, commandTimeoutDur(cmd, cmdTimeoutSec))
		}
		return out, err
	}

	script := prepareOrchestratedScript(cmd)
	tmp, err := os.CreateTemp("", "gt-agent-orch-*.sh")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := fmt.Fprintf(tmp, "#!/bin/bash\nset -euo pipefail\n%s\n", script); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Chmod(0700); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}

	c := exec.CommandContext(ctx, "/bin/bash", tmpPath)
	c.Env = env
	c.Dir = workDir
	out, err := c.CombinedOutput()
	if err != nil && ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("%w (script exceeded %s)", err, commandTimeoutDur(cmd, cmdTimeoutSec))
	}
	return out, err
}

// orchestratedCommandWorkDir is the subprocess cwd for rig workflow shell commands.
// All rig-flow prompts tell agents to work from town root with paths like {{rig}}/mayor/rig/....
// Using mayor/rig as cwd makes those paths resolve into a nested {{rig}}/mayor/rig/ subtree.
func orchestratedCommandWorkDir(townRoot, rig, taskState string) string {
	_ = taskState
	if rig == "" || townRoot == "" {
		return townRoot
	}
	return townRoot
}
