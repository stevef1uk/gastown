package orchestrator

import (
	"regexp"
	"strings"
)

var doubledShellVariable = regexp.MustCompile(`\$\$[A-Za-z_][A-Za-z0-9_]*`)

// NormalizeShellDollarEscapes repairs commands whose shell variables were
// escaped for an extra interpolation layer that is not present at execution.
// A bare $$ remains the shell PID operator; only variable and parameter forms
// are reduced by one dollar sign.
func NormalizeShellDollarEscapes(cmd string) string {
	if !strings.Contains(cmd, "$$") {
		return cmd
	}
	for _, pair := range []struct{ from, to string }{
		{"$$(", "$("},
		{"$${", "${"},
		{"$$!", "$!"},
		{"$$?", "$?"},
		{"$$#", "$#"},
		{"$$@", "$@"},
		{"$$*", "$*"},
	} {
		cmd = strings.ReplaceAll(cmd, pair.from, pair.to)
	}
	return doubledShellVariable.ReplaceAllStringFunc(cmd, func(match string) string {
		return match[1:]
	})
}

func normalizeProfileShellCommands(v WorkflowValidation) WorkflowValidation {
	v.QAVerifyCommand = NormalizeShellDollarEscapes(v.QAVerifyCommand)
	for i := range v.DeliveryPhases {
		v.DeliveryPhases[i].QAVerifyCommand = NormalizeShellDollarEscapes(v.DeliveryPhases[i].QAVerifyCommand)
	}
	return v
}
