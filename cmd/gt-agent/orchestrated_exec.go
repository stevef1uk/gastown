package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
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

// rewriteUnittestToWorkdir prepends cd into mayor/rig when the model omits it (tests need project on cwd/sys.path).
func rewriteUnittestToWorkdir(cmd, rig string) (string, bool) {
	lower := strings.ToLower(cmd)
	if !strings.Contains(lower, "unittest") && !strings.Contains(lower, "pytest") {
		return cmd, false
	}
	work := rigMayorRigPath(rig)
	wl := strings.ToLower(work)
	if strings.Contains(lower, "cd "+wl) || strings.Contains(lower, "cd "+strings.ToLower(rig)+"/mayor/rig") {
		return cmd, false
	}
	return "cd " + work + " && " + strings.TrimSpace(cmd), true
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

func runOrchestratedCommand(cmd, townRoot, sessionName string, env []string) ([]byte, error) {
	if sessionName != "" {
		env = append(env, "GT_SESSION="+sessionName)
	}
	if !needsOrchestratedScriptFile(cmd) {
		c := exec.Command("/bin/sh", "-c", cmd)
		c.Env = env
		c.Dir = townRoot
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
	c.Dir = townRoot
	return c.CombinedOutput()
}
