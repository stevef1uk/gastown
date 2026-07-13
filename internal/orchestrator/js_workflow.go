package orchestrator

import "strings"

// WorkflowUsesNodeJS reports whether the workflow runs npm/yarn tests (frontend/Node).
func WorkflowUsesNodeJS(v WorkflowValidation) bool {
	return v.detectsNodeProject()
}

func (v WorkflowValidation) detectsNodeProject() bool {
	q := strings.ToLower(strings.TrimSpace(v.QAVerifyCommand))
	if strings.Contains(q, "npm ") || strings.Contains(q, "npm/") || strings.Contains(q, "yarn ") || strings.Contains(q, "pnpm ") {
		return true
	}
	for _, f := range v.RequiredFiles {
		lower := strings.ToLower(strings.TrimSpace(f))
		if strings.HasSuffix(lower, ".tsx") || strings.HasSuffix(lower, ".ts") || strings.HasSuffix(lower, ".js") || strings.HasSuffix(lower, ".jsx") {
			return true
		}
	}
	return false
}

// NodeProjectSetupVerifyCommand returns the green check for project_setup only:
// install Node dependencies (npm install) so later npm test can run.
func NodeProjectSetupVerifyCommand(v WorkflowValidation) string {
	base := strings.TrimSpace(v.QAVerifyCommand)
	lower := strings.ToLower(base)
	// Preserve cd prefix from the QA command (e.g. "cd frontend && npm test").
	if strings.Contains(lower, "cd ") && strings.Contains(lower, "npm") {
		parts := strings.SplitN(base, "&&", 2)
		if len(parts) == 2 {
			prefix := strings.TrimSpace(parts[0])
			if strings.HasPrefix(strings.ToLower(prefix), "cd ") {
				return prefix + " && npm install"
			}
		}
	}
	return "npm install"
}
