package agentenv

import "strings"

// UnwrapBashLcSingleLine strips bash -lc '...' wrappers so subprocesses inherit gt-agent PATH
// instead of a login shell that may prefer /usr/bin/python3.
func UnwrapBashLcSingleLine(cmd string) string {
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
	body := inner[1:]
	// bash -lc 'cd … && cmd' | grep … — closing quote is not at end of string
	if idx := strings.IndexByte(body, q); idx >= 0 {
		unwrapped := strings.TrimSpace(body[:idx])
		rest := strings.TrimSpace(body[idx+1:])
		if rest != "" {
			return unwrapped + " " + rest
		}
		return unwrapped
	}
	inner = strings.TrimSpace(body)
	if len(inner) > 0 && inner[len(inner)-1] == q {
		inner = strings.TrimSpace(inner[:len(inner)-1])
	}
	return strings.TrimSpace(inner)
}
