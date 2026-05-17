package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

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
	return filterHallucinatedScriptLines(body)
}

// filterHallucinatedScriptLines drops model junk glued onto shell scripts.
func filterHallucinatedScriptLines(body string) string {
	var kept []string
	for _, line := range strings.Split(body, "\n") {
		t := strings.TrimSpace(line)
		if strings.Contains(strings.ToUpper(t), "[TOOL_CALLS]") {
			continue
		}
		if looksLikeHallucinatedShellOutput(t) {
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
	return false
}

// writesRequirementsFile reports commands that create/overwrite requirements.txt (heredoc or redirect).
func writesRequirementsFile(cmd string) bool {
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "requirements.txt") &&
		(strings.Contains(lower, "<<") || strings.Contains(lower, "cat >") || strings.Contains(lower, "cat>>"))
}

// rewriteUnittestToWorkdir prepends cd into mayor/rig when the model omits it (tests/deps need project cwd).
func rewriteUnittestToWorkdir(cmd, rig string) (string, bool) {
	if !isToolchainExecutionCommand(cmd) {
		return cmd, false
	}
	lower := strings.ToLower(cmd)
	_ = lower
	changed := false
	if fixed := orchestrator.NormalizePytestCommand(cmd); fixed != cmd {
		cmd = fixed
		changed = true
	}
	if fixed := orchestrator.NormalizePipCommand(cmd); fixed != cmd {
		cmd = fixed
		changed = true
	}
	if commandHasMayorRigCD(cmd, rig) {
		if changed {
			return cmd, true
		}
		return cmd, false
	}
	work := rigMayorRigPath(rig)
	return "cd " + work + " && " + strings.TrimSpace(cmd), true
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
	lower := strings.ToLower(cmd)
	if !strings.Contains(lower, "bd list") || strings.Contains(lower, "--limit") {
		return cmd, false
	}
	// Insert --limit=0 after "bd list"
	re := strings.Replace(cmd, "bd list", "bd list --limit=0", 1)
	if re == cmd {
		re = strings.Replace(cmd, "BD list", "bd list --limit=0", 1)
	}
	return re, re != cmd
}

func runOrchestratedCommand(cmd, workDir, sessionName string, env []string) ([]byte, error) {
	if sessionName != "" {
		env = append(env, "GT_SESSION="+sessionName)
	}
	if workDir == "" {
		workDir = "."
	}
	if !needsOrchestratedScriptFile(cmd) {
		c := exec.Command("/bin/sh", "-c", cmd)
		c.Env = env
		c.Dir = workDir
		return c.CombinedOutput()
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

	c := exec.Command("/bin/bash", tmpPath)
	c.Env = env
	c.Dir = workDir
	return c.CombinedOutput()
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
