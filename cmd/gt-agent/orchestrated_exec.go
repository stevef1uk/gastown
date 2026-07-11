package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// truncateCmdForLog returns a shortened, single-line representation of a command for logging.
func truncateCmdForLog(cmd string, maxLen int) string {
	cmd = strings.Join(strings.Fields(cmd), " ")
	if len(cmd) <= maxLen {
		return cmd
	}
	return cmd[:maxLen-3] + "..."
}

// prepareOrchestratedScript turns a model CMD block into a bash script body.
func prepareOrchestratedScript(cmd string) string {
	body := unwrapBashLcMultiline(strings.TrimSpace(cmd))
	body = strings.ReplaceAll(body, `\$`, "$")
	body = normalizeHeredocDelimiters(body)
	body = rewriteMultilinePythonCToHeredoc(body)
	body = filterHallucinatedScriptLines(body)
	body = scrubOrphanHeredocDelimiterLines(body)
	body = stripMarkdownFencesInHeredocScripts(body)
	return scrubOrphanBashLcQuoteLines(body)
}

func rewriteMultilinePythonCToHeredoc(cmd string) string {
	for _, quote := range []string{"\"", "'"} {
		re := regexp.MustCompile(`(?ms)(python3?\s+)-c\s*` + regexp.QuoteMeta(quote) + `\n([\s\S]*?)\n[ \t]*` + regexp.QuoteMeta(quote))
		cmd = re.ReplaceAllString(cmd, `$1- <<'PY'
$2
PY`)
	}
	return cmd
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
		if looksLikeMarkdownStepLine(t) {
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
	if orchestrator.IsPythonImportCheckCommand(cmd) {
		return true
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

// isLayoutRelativeShellCommand reports shell file checks and mkdir/cat paths that use layout_root
// without cd into mayor/rig (orchestrated cwd is town root).
func isLayoutRelativeShellCommand(cmd, rig, layout string) bool {
	layout = strings.Trim(strings.TrimSpace(layout), "/")
	if layout == "" {
		return false
	}
	if commandHasMayorRigCD(cmd, rig) {
		return false
	}
	lower := strings.ToLower(cmd)
	layoutLower := strings.ToLower(layout)
	if commandHasLayoutCD(cmd, layout) {
		for _, tool := range []string{"test -f", "test -d", "test -e", "mkdir", "wc -c", "wc -l", "ls ", "cat ", "head ", "tail ", "rm ", "touch "} {
			if strings.Contains(lower, tool) {
				return true
			}
		}
		return strings.Contains(lower, "&&")
	}
	for _, prefix := range []string{layoutLower + "/", "./" + layoutLower + "/"} {
		if strings.Contains(lower, prefix) {
			for _, tool := range []string{"test -f", "test -d", "test -e", "mkdir", "wc -c", "wc -l"} {
				if strings.Contains(lower, tool) {
					return true
				}
			}
		}
	}
	return false
}

// rewriteUnittestToWorkdir prepends cd into mayor/rig (and layout_root for Go modules) when omitted.
func rewriteUnittestToWorkdir(cmd, rig string, v orchestrator.WorkflowValidation) (string, bool) {
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if !isToolchainExecutionCommand(cmd) && !isLayoutRelativeShellCommand(cmd, rig, layout) {
		return cmd, false
	}
	// Profile-relative "cd linkshelf && go run …" smoke must not get a second prefix; bare
	// "go run ./cmd/server" from town root still needs cd into layout_root (see goLayout test).
	if isDevServerSmokeCommand(cmd) && (commandHasMayorRigCD(cmd, rig) || commandHasLayoutCD(cmd, layout)) {
		return cmd, false
	}
	// Rewriting cd for a command that creates a symlink with a relative target
	// would break path resolution (e.g. ln -sfn ../../web web).
	if hasSymlinkWithRelativeTarget(cmd) {
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
	// Strip source .venv/bin/activate && so pip runs in venv context.
	if strings.Contains(cmd, ".venv/bin/activate") && strings.Contains(cmd, "pip install") {
		cmd = regexp.MustCompile(`(\.|source)\s+\S*venv/\S*activate\s*&&\s*`).ReplaceAllString(cmd, "")
		changed = true
	}
	// In project_setup, force pip install to use the venv python so packages
	// land in .venv/ and the verify (import pytest) succeeds.
	// Don't rewrite pip install --upgrade pip (targets system pip, not venv).
	if orchestrator.WorkflowUsesPython(v) &&
		strings.Contains(strings.ToLower(cmd), "pip install") &&
		!strings.Contains(strings.ToLower(cmd), "--upgrade pip") &&
		!strings.Contains(cmd, ".venv/") && !strings.Contains(cmd, "venv/") {
		if venvPy := v.PythonVenvRelDir() + "/bin/python3"; !strings.Contains(cmd, venvPy) {
			cmd = strings.Replace(cmd, "python3 -m pip", venvPy+" -m pip", 1)
			changed = true
		}
	}
	mayorRig := rigMayorRigPath(rig)
	layoutShell := isLayoutRelativeShellCommand(cmd, rig, layout)
	// Python venv lives under mayor/rig only — never cd into layout_root for pip/pytest/compileall.
	workPath := mayorRig
	if orchestrator.WorkflowUsesGo(v) || (layout != "" && layoutShell) {
		workPath = orchestrator.GoModuleWorkPathRelative(mayorRig, layout)
	}
	if !commandHasMayorRigCD(cmd, rig) {
		if !commandHasLayoutCD(cmd, layout) {
			cmd = "cd " + workPath + " && " + stripLeadingCDDot(strings.TrimSpace(cmd))
			changed = true
		} else if layout != "" && layout != "." {
			// Profile verify uses bare "cd layout" (mayor/rig-relative); orchestrated cwd is town root.
			rest := stripRedundantLayoutCD(stripFirstCDPrefix(cmd), workPath, layout)
			cmd = "cd " + workPath + " && " + rest
			changed = true
		}
	} else if !orchestrator.WorkflowUsesGo(v) && layout != "" && layout != "." &&
		commandHasMayorRigCD(cmd, rig) && commandHasLayoutCD(cmd, layout) {
		// Python: cd path includes layout subdir but .venv lives at mayor/rig only.
		// Strip layout cd so paths resolve correctly (no double defender/defender/...).
		if !strings.Contains(strings.ToLower(cmd), "-r ") {
			rest := stripRedundantLayoutCD(stripFirstCDPrefix(cmd), mayorRig, layout)
			rest = stripCDLayoutPrefix(rest, layout)
			rest = adjustPytestPathsAfterLayoutStrip(rest, layout)
			cmd = "cd " + mayorRig + " && " + rest
			changed = true
		}
	} else if orchestrator.WorkflowUsesGo(v) && layout != "" && layout != "." &&
		commandHasMayorRigCD(cmd, rig) && !commandHasLayoutCD(cmd, layout) {
		// Already under mayor/rig but not in layout module dir — one cd to module root.
		rest := stripRedundantLayoutCD(stripFirstCDPrefix(cmd), workPath, layout)
		rest = stripMayorRigCDCmd(rest, rig)
		cmd = "cd " + workPath + " && " + rest
		changed = true
	} else if orchestrator.WorkflowUsesGo(v) && layout != "" && layout != "." &&
		commandHasMayorRigCD(cmd, rig) && commandHasLayoutCD(cmd, layout) {
		// Go: command has both "cd mayor/rig && cd layout" — strip both and
		// replace with single cd to the module root to avoid broken relative paths.
		rest := stripCDLayoutPrefix(stripFirstCDPrefix(cmd), layout)
		rest = stripMayorRigCDCmd(rest, rig)
		cmd = "cd " + workPath + " && " + strings.TrimSpace(rest)
		changed = true
	}
	if orchestrator.WorkflowUsesGo(v) {
		normalized := orchestrator.NormalizeGoLayoutPackagePaths(cmd, workPath, layout)
		if normalized != cmd {
			cmd = normalized
			changed = true
		}
	}
	if layout != "" && layout != "." {
		cmd = normalizeRigPrefixShellPaths(cmd, rig, layout)
		normalized := normalizeLayoutShellPaths(cmd, layout)
		if normalized != cmd {
			cmd = normalized
			changed = true
		}
	}
	return cmd, changed
}

// normalizeLayoutShellPaths strips redundant layout_root/ prefixes after cd into the module workdir.
func normalizeLayoutShellPaths(cmd, layout string) string {
	layout = strings.Trim(strings.TrimSpace(layout), "/")
	if layout == "" {
		return cmd
	}
	for _, needle := range []string{layout + "/", "./" + layout + "/"} {
		for _, tool := range []string{"mkdir -p ", "test -f ", "test -d ", "test -e ", "wc -c ", "wc -l ", "cat ", "head ", "tail "} {
			cmd = strings.ReplaceAll(cmd, tool+needle, tool)
		}
	}
	return cmd
}

// normalizeRigPrefixShellPaths strips `rig/mayor/rig/` prefixes from file paths
// when the workdir has been set to `rig/mayor/rig/layout/`. Without this, commands like
// `wc -l testgt3/mayor/rig/linkshelf/web/index.html` resolve double-nested under that workdir.
func normalizeRigPrefixShellPaths(cmd, rig, layout string) string {
	rig = strings.TrimSpace(rig)
	layout = strings.Trim(filepath.ToSlash(strings.TrimSpace(layout)), "/")
	if rig == "" || layout == "" {
		return cmd
	}
	prefix := rig + "/mayor/rig/"
	for _, tool := range []string{"ls -la ", "ls -l ", "ls ", "cat ", "wc -l ", "wc -c ", "head ", "tail ", "test -f ", "test -d ", "test -e "} {
		cmd = strings.ReplaceAll(cmd, tool+prefix, tool)
	}
	return cmd
}

// stripLeadingCDDot removes a leading "cd . &&" when mayor/rig workdir is prepended separately.
func isUvicornCommand(cmd string) bool {
	// Only treat as uvicorn if it appears as a command (uvicorn, python3 -m uvicorn),
	// not in echo "uvicorn[standard]", cat output, or string literals.
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "uvicorn") &&
		!strings.Contains(lower, "echo") &&
		!strings.Contains(lower, "cat ") &&
		!strings.Contains(lower, ">>") &&
		!strings.Contains(lower, "'uvicorn") &&
		!strings.Contains(lower, "\"uvicorn")
}

func stripCDLayoutPrefix(cmd, layout string) string {
	layout = strings.Trim(strings.TrimSpace(layout), "/")
	if layout == "" {
		return cmd
	}
	lower := strings.ToLower(cmd)
	for _, pat := range []string{
		"cd " + layout + " && ",
		"cd ./" + layout + " && ",
		"cd " + layout + "/ && ",
	} {
		if idx := strings.Index(lower, pat); idx >= 0 {
			return strings.TrimSpace(cmd[:idx] + cmd[idx+len(pat):])
		}
	}
	// cd layout/subdir/something && — strip to just the remaining command.
	for _, prefix := range []string{"cd " + layout + "/"} {
		if idx := strings.Index(lower, prefix); idx >= 0 {
			rest := cmd[idx+len(prefix):]
			if sIdx := strings.Index(rest, " && "); sIdx >= 0 {
				return strings.TrimSpace(cmd[:idx] + rest[sIdx+4:])
			}
		}
	}
	return cmd
}

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

// stripRedundantLayoutCD drops embedded "cd layout" when workPath already ends in layout.
func stripRedundantLayoutCD(cmd, workPath, layout string) string {
	layout = strings.Trim(filepath.ToSlash(strings.TrimSpace(layout)), "/")
	wp := strings.Trim(filepath.ToSlash(strings.TrimSpace(workPath)), "/")
	if layout == "" || wp == "" || !strings.HasSuffix(wp, layout) {
		return strings.TrimSpace(cmd)
	}
	for _, pat := range []string{
		"&& cd " + layout + " && ",
		"&& cd ./" + layout + " && ",
	} {
		cmd = strings.ReplaceAll(cmd, pat, "&& ")
	}
	return strings.TrimSpace(cmd)
}

// stripMayorRigCDCmd removes non-leading "&& cd <rig>/mayor/rig" from the command body
// so a subsequent prepended cd into the layout subdir does not chain two relative cd's.
func stripMayorRigCDCmd(cmd, rig string) string {
	mayorRig := strings.ToLower(rigMayorRigPath(rig))
	if mayorRig == "" {
		return cmd
	}
	// Match "&& cd <mayor/rig>" optionally followed by " && ".
	pat := "&& cd " + mayorRig
	for {
		lower := strings.ToLower(cmd)
		idx := strings.Index(lower, pat)
		if idx < 0 {
			break
		}
		after := cmd[idx+len(pat):]
		if strings.HasPrefix(after, " && ") {
			cmd = cmd[:idx] + " && " + after[4:]
		} else {
			cmd = cmd[:idx] + after
		}
	}
	return strings.TrimSpace(cmd)
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
		strings.Contains(lower, "/"+layoutLower+" &&")
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

// commandHasRigPathContext reports whether cmd already references the rig tree (full paths or cd into mayor/rig).
func commandHasRigPathContext(cmd, rig string) bool {
	if commandHasMayorRigCD(cmd, rig) {
		return true
	}
	rigLower := strings.ToLower(strings.TrimSpace(rig))
	if rigLower == "" {
		return false
	}
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, rigLower+"/") || strings.Contains(lower, "$gt_root/"+rigLower)
}

// rewriteQALayoutVerifyCommand fixes common QA agent mistakes: cd layout from town root,
// or cd layout && cd mayor/rig before go mod/test verify.
func rewriteQALayoutVerifyCommand(cmd, rig string, v orchestrator.WorkflowValidation) (string, bool) {
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	if layout == "" || layout == "." {
		return cmd, false
	}
	mayorRig := rigMayorRigPath(rig)
	modulePath := orchestrator.GoModuleWorkPathRelative(mayorRig, layout)
	trimmed := strings.TrimSpace(cmd)
	lower := strings.ToLower(trimmed)

	badPrefix := "cd " + layout + " && cd " + mayorRig + " && "
	if strings.HasPrefix(lower, strings.ToLower(badPrefix)) {
		rest := strings.TrimSpace(trimmed[len(badPrefix):])
		return "cd " + modulePath + " && " + rest, true
	}
	goodPrefix := "cd " + layout + " && "
	if strings.HasPrefix(lower, strings.ToLower(goodPrefix)) && !commandHasMayorRigCD(trimmed, rig) {
		rest := strings.TrimSpace(trimmed[len(goodPrefix):])
		return "cd " + modulePath + " && " + rest, true
	}
	return cmd, false
}

// rewriteQAMayorRigPrefix prepends mayor/rig (and BEADS_DIR for bd) when QA uses bare relative paths.
// gt-agent cwd is town root; cd in one CMD line does not persist to the next.
func rewriteQAMayorRigPrefix(cmd, rig string) (string, bool) {
	trimmed := strings.TrimSpace(cmd)
	if trimmed == "" || commandHasRigPathContext(trimmed, rig) {
		return cmd, false
	}
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "export ") && !strings.Contains(lower, "bd ") {
		return cmd, false
	}
	if strings.HasPrefix(lower, "cd ") {
		return cmd, false
	}
	needsPrefix := false
	for _, p := range []string{"cat ", "head ", "tail ", "test ", "find ", "wc ", "ls ", "stat ", "grep ", "bd ", "bd\t"} {
		if strings.HasPrefix(lower, p) {
			needsPrefix = true
			break
		}
	}
	if !needsPrefix {
		return cmd, false
	}
	mayorPath := rigMayorRigPath(rig)
	prefix := "cd " + mayorPath + " && "
	if strings.Contains(lower, "bd ") || strings.Contains(lower, "bd\t") {
		prefix = "export BEADS_DIR=$GT_ROOT/" + rig + "/.beads && " + prefix
	}
	return prefix + trimmed, true
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
	q := "'" + strings.ReplaceAll(titleContains, "'", `'"'"'`) + "'"
	out := strings.TrimSpace(cmd)
	hasStatus := strings.Contains(lower, "--status=open") || strings.Contains(lower, "--status=in_progress") ||
		strings.Contains(lower, "--status=closed") || strings.Contains(lower, "--status open")
	if !hasStatus {
		// Bare `bd list` only shows open issues (often role beads). Show implement beads across statuses.
		openList := injectBdListStatusFlags(out, "open,in_progress")
		closedList := injectBdListStatusFlags(out, "closed")
		script := fmt.Sprintf(
			`(echo '=== open/in_progress implement ==='; ( %s ) | grep -Fi %s || true; echo '=== closed implement ==='; ( %s ) | grep -Fi %s | head -30 || true)`,
			openList, q, closedList, q,
		)
		return script, true
	}
	if strings.Contains(lower, "--status=open") && !strings.Contains(lower, "--status=in_progress") {
		out = strings.Replace(out, "--status=open", "--status=open,in_progress", 1)
		out = strings.Replace(out, "--status open", "--status open,in_progress", 1)
	}
	return out + " | grep -Fi " + q + " || true", true
}

// injectBdListStatusFlags adds --status/--flat/--limit=0 to a bd list command that lacks --status.
func injectBdListStatusFlags(cmd, status string) string {
	lower := strings.ToLower(cmd)
	if strings.Contains(lower, "--status=") || strings.Contains(lower, "--status ") {
		return cmd
	}
	re := regexp.MustCompile(`(?i)\bbd\s+list\b`)
	return re.ReplaceAllString(cmd, "bd list --status="+status+" --flat --limit=0")
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

// rewriteBdCloseWithAutoCommit appends --dolt-auto-commit=on to bd close commands
// so the close persists immediately (BD_DOLT_AUTO_COMMIT env var is not recognized by bd).
func rewriteBdCloseWithAutoCommit(cmd string) string {
	lower := strings.ToLower(cmd)
	idx := strings.Index(lower, "bd close")
	if idx < 0 {
		return cmd
	}
	// Already has --dolt-auto-commit
	if strings.Contains(lower, "--dolt-auto-commit") {
		return cmd
	}
	// Find the end of the bd close command (next && or end of string)
	rest := cmd[idx:]
	endIdx := strings.Index(rest, "&&")
	var insertPoint int
	if endIdx >= 0 {
		insertPoint = idx + endIdx
	} else {
		insertPoint = len(cmd)
	}
	// Insert --dolt-auto-commit=on before any trailing && or at end
	return cmd[:insertPoint] + " --dolt-auto-commit=on" + cmd[insertPoint:]
}

// rewriteBdStripBeadsDir strips "export BEADS_DIR=... &&" from bd commands AND
// prepends "unset BEADS_DIR &&" so the env var (set by beads_dir: true in YAML)
// doesn't redirect bd to a broken Dolt database. bd must auto-discover from cwd.
var beadsDirExportRE = regexp.MustCompile(`(?i)\s*(?:export\s+)?BEADS_DIR=\S+\s*(?:&&|;)\s*`)

func rewriteBdStripBeadsDir(cmd string) (string, bool) {
	lower := strings.ToLower(cmd)
	if !strings.Contains(lower, "bd ") {
		return cmd, false
	}
	changed := false
	// Strip "export BEADS_DIR=... &&" from the command text
	stripped := beadsDirExportRE.ReplaceAllString(cmd, "")
	if stripped != cmd {
		changed = true
		cmd = stripped
	}
	// Also unset the BEADS_DIR env var (set by beads_dir: true in rig-flow.yaml)
	// so bd auto-discovers .beads from cwd. Insert "unset BEADS_DIR && " at the
	// very beginning of the command (before any cd).
	if !strings.Contains(strings.ToLower(cmd), "unset beads_dir") {
		cmd = "unset BEADS_DIR && " + cmd
		changed = true
	}
	return cmd, changed
}

var (
	goSmokeStripPkillRE   = regexp.MustCompile(`(?i)\s*&&\s*pkill\s+-f\s+[^&|;]+`)
	goSmokeStripBuildRE   = regexp.MustCompile(`(?i)go\s+build\s+\./\.\.\.\s*&&\s*`)
	goSmokeStripTidyRE    = regexp.MustCompile(`(?i)go\s+mod\s+tidy\s*&&\s*`)
	goSmokeServerBgRE     = regexp.MustCompile(`(?i)(go\s+run\s+(?:\.\/)?cmd/server[^\s&;]*)\s*&`)
	goSmokeWorkDirRE      = regexp.MustCompile(`(?i)cd\s+([^\s&;]+linkshelf[^\s&;]*)`)
	goSmokeLocalhostRE    = regexp.MustCompile(`(?i)(?:localhost|127\.0\.0\.1):(\d{2,5})`)
	pythonSmokeJobCtrlRE  = regexp.MustCompile(`(?i)\b(kill|wait)\b((?:\s+-[^ \t;|]+)*)\s+%[0-9]+\b`)
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
	lower := strings.ToLower(cmd)
	if orchestrator.WorkflowUsesPython(v) && strings.Contains(lower, "uvicorn") {
		return normalizePythonDevServerSmoke(cmd), true
	}
	if !orchestrator.WorkflowUsesGo(v) {
		return cmd, false
	}
	out := cmd
	changed := false
	if goSmokeStripPkillRE.MatchString(out) {
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

func normalizePythonDevServerSmoke(cmd string) string {
	for _, bad := range []string{"--stdout", "--no-access-log"} {
		cmd = strings.ReplaceAll(cmd, " "+bad+" ", " ")
		cmd = strings.ReplaceAll(cmd, " "+bad, "")
	}
	// Convert shell job-control references such as `kill %1` / `wait %1` into
	// pid-based shutdowns before rewriting the command.
	cmd = pythonSmokeJobCtrlRE.ReplaceAllString(cmd, `${1}${2} $$_uvpid`)
	cmd = strings.ReplaceAll(cmd, "kill  $$_uvpid", "kill $$_uvpid")
	cmd = strings.ReplaceAll(cmd, "wait  $$_uvpid", "wait $$_uvpid")
	// &> /dev/null is bash-only; replace with POSIX redirect.
	cmd = strings.ReplaceAll(cmd, "&> /dev/null", ">/dev/null 2>&1")
	cmd = strings.ReplaceAll(cmd, "--host 127.0.0.1", "--host 127.0.0.1")
	if !strings.Contains(cmd, "--max-time") {
		for _, pair := range []struct{ old, new string }{
			{"curl -sf ", "curl -sf --max-time 10 "},
			{"curl -sSf ", "curl -sSf --max-time 10 "},
			{"curl -s ", "curl -s --max-time 10 "},
		} {
			if strings.Contains(cmd, pair.old) {
				cmd = strings.ReplaceAll(cmd, pair.old, pair.new)
			}
		}
	}
	// Uvicorn without curl: background + redirect so shell doesn't hang, then kill.
	// Skip if uvicorn appears only in echo/cat/string context (not as a command).
	if strings.Contains(cmd, "uvicorn") && isUvicornCommand(cmd) && !strings.Contains(cmd, "curl") {
		// Replace only standalone & (not &&) with redirect.
		cmd = regexp.MustCompile(`([^&]|^)\s&\s`).ReplaceAllString(cmd, "${1} >/dev/null 2>&1 & ")
		cmd = strings.TrimSpace(cmd) + " _uvpid=$!; sleep 1; kill $_uvpid 2>/dev/null || true; wait $_uvpid 2>/dev/null || true"
	}
	// block and curl has a running server to hit.
	if strings.Contains(cmd, "uvicorn") && isUvicornCommand(cmd) && strings.Contains(cmd, "curl") {
		// Strip any prior broken _uvpid/kill so we can rewrite cleanly.
		cmd = strings.ReplaceAll(cmd, " & _uvpid=$! ", " & ")
		cmd = regexp.MustCompile(`\s*;\s*kill\s+\$?_uvpid\b[^;]*`).ReplaceAllString(cmd, "")
		cmd = regexp.MustCompile(`\s*;\s*wait\s+\$?_uvpid\b[^;]*`).ReplaceAllString(cmd, "")
		// Find the & that backgrounds uvicorn. It may be followed by space, ), ;, or end-of-cmd.
		// Match across newlines because LLM-generated smoke commands often split the shell body.
		bgRE := regexp.MustCompile(`(?ms)\buvicorn\b.*?\s&(?:\s|[);]|$)`)
		if loc := bgRE.FindStringIndex(cmd); loc != nil {
			ampIdx := loc[1] - 1
			for ampIdx > 0 && cmd[ampIdx] != '&' {
				ampIdx--
			}
			rest := strings.TrimSpace(cmd[ampIdx+1:])
			rest = strings.TrimLeft(rest, " );\t&")
			rest = strings.TrimSpace(rest)
			cmd = cmd[:ampIdx] + " >/dev/null 2>&1 & _uvpid=$!"
			if !strings.Contains(strings.ToLower(rest), "sleep ") {
				cmd += "; sleep 2"
			}
			cmd += " && " + rest
			cmd = strings.TrimRight(cmd, " ;&") + " ; kill $_uvpid 2>/dev/null || true; wait $_uvpid 2>/dev/null || true"
		}
	}
	if strings.Contains(cmd, "uvicorn") && strings.Contains(cmd, "kill $_uvpid") && !strings.Contains(cmd, "_uvpid=$!") {
		cmd = strings.ReplaceAll(cmd, " & ", " >/dev/null 2>&1 & _uvpid=$! ")
		cmd = strings.ReplaceAll(cmd, "&\n", ">/dev/null 2>&1 & _uvpid=$!\n")
		cmd = strings.ReplaceAll(cmd, "&\r\n", ">/dev/null 2>&1 & _uvpid=$!\r\n")
		cmd = strings.ReplaceAll(cmd, "&;", ">/dev/null 2>&1 & _uvpid=$!;")
	}
	return cmd
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
	if townRoot != "" && rig != "" && !orchestrator.WorkflowNeedsQARuntimeSmoke(townRoot, rig, v) &&
		!orchestrator.WorkflowNeedsRuntimeSmoke(townRoot, rig, v) {
		return cmd, false
	}
	workDir := smokeWorkDirFromCommand(cmd)
	if workDir == "" {
		return cmd, false
	}
	spec, _ := orchestrator.LoadAPISmokeSpecFromRig(townRoot, rig, v)
	if spec.ServerStart == "" {
		if orchestrator.WorkflowUsesPython(v) {
			spec.ServerStart = orchestrator.ExtractPythonServerStartFromText(cmd)
		} else {
			spec.ServerStart = "go run ./cmd/server"
		}
	}
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
	// Python smoke: uvicorn/gunicorn + curl.
	if (strings.Contains(lower, "uvicorn") || strings.Contains(lower, "gunicorn")) &&
		strings.Contains(lower, "curl") {
		return 30 * time.Second
	}
	// Python test suite.
	if strings.Contains(lower, "pytest") || strings.Contains(lower, "python3 -m unittest") {
		return 2 * time.Minute
	}
	// Bare server start (no curl) — safety cap, shouldn't happen after guards.
	// Go compile can be slow; give it more time.
	if strings.Contains(lower, "go run") && !strings.Contains(lower, "curl") {
		return 2 * time.Minute
	}
	if strings.Contains(lower, "uvicorn") || strings.Contains(lower, "gunicorn") {
		return 15 * time.Second
	}
	// Any other long-running command.
	if strings.Contains(lower, "pip install") || strings.Contains(lower, "go mod") {
		return 2 * time.Minute
	}
	return 60 * time.Second
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
	cmdStart := time.Now()
	logCmd := truncateCmdForLog(cmd, 120)
	orchestratedPrintf("[gt-agent] exec start: %s\n", logCmd)

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
		orchestratedPrintf("[gt-agent] exec done: %s (duration=%s)\n", logCmd, time.Since(cmdStart).Round(time.Millisecond))
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
	orchestratedPrintf("[gt-agent] exec done: %s (duration=%s)\n", logCmd, time.Since(cmdStart).Round(time.Millisecond))
	if err != nil && ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("%w (script exceeded %s)", err, commandTimeoutDur(cmd, cmdTimeoutSec))
	}
	return out, err
}

// adjustPytestPathsAfterLayoutStrip prepends the layout root to bare .py file
// arguments in pytest commands when a cd into the layout subdirectory was stripped.
// E.g. "pytest -v test_main.py" → "pytest -v layout/test_main.py"
func adjustPytestPathsAfterLayoutStrip(cmd, layout string) string {
	lower := strings.ToLower(cmd)
	if !strings.Contains(lower, "pytest") || layout == "" || layout == "." {
		return cmd
	}
	tokens := strings.Fields(cmd)
	changed := false
	for i, tok := range tokens {
		if strings.HasSuffix(tok, ".py") && !strings.Contains(tok, "/") && !strings.Contains(tok, "\\") {
			tokens[i] = layout + "/" + tok
			changed = true
		}
	}
	if !changed {
		return cmd
	}
	return strings.Join(tokens, " ")
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
