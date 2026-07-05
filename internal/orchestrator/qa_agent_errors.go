package orchestrator

import "strings"

// IsQAAgentShellError reports QA failures caused by wrong cwd or shell mistakes, not bad implementation.
func IsQAAgentShellError(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	for _, needle := range []string{
		"can't cd", "cannot cd", "can't cd to", "cannot cd to",
		"cd: can't", "cd: no such file",
		"exit status 127", "command not found",
	} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	if strings.Contains(lower, "exit status 2") &&
		(strings.Contains(lower, "cd") || strings.Contains(lower, "can't")) {
		return true
	}
	return false
}

// CombineQAReworkText merges QA failure summary and session feedback for bead-ID extraction.
func CombineQAReworkText(summary, feedback string) string {
	return strings.TrimSpace(summary + "\n" + feedback)
}
