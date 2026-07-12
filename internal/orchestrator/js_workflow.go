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
