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
	return body
}

// unwrapBashLcMultiline strips bash -lc '...' / "..." wrappers from multiline agent commands.
func unwrapBashLcMultiline(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	const prefix = "bash -lc "
	if !strings.HasPrefix(cmd, prefix) {
		return cmd
	}
	inner := strings.TrimSpace(cmd[len(prefix):])
	if len(inner) < 2 {
		return inner
	}
	q := inner[0]
	if (q == '\'' || q == '"') && inner[len(inner)-1] == q {
		return inner[1 : len(inner)-1]
	}
	return inner
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
