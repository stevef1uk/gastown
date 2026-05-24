package main

import (
	"fmt"
	"strings"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

// responseHasSubstantialFencedGoCode reports markdown ```go blocks that look like real source.
func responseHasSubstantialFencedGoCode(response string) bool {
	lines := strings.Split(response, "\n")
	inGo := false
	var body strings.Builder
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if !inGo {
			if strings.HasPrefix(t, "```") {
				lang := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(t, "```")))
				if lang == "go" || lang == "golang" {
					inGo = true
					continue
				}
			}
			continue
		}
		if t == "```" {
			text := body.String()
			if strings.Contains(text, "package ") &&
				(strings.Contains(text, "func ") || strings.Contains(text, "import (")) {
				return true
			}
			inGo = false
			body.Reset()
			continue
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
	return false
}

// responseHasSubstantiveNativeWriteOrEdit reports parsed WRITE/EDIT ops with real content.
func responseHasSubstantiveNativeWriteOrEdit(response string) bool {
	for _, op := range parseOrchestratedNativeEdits(response) {
		switch op.kind {
		case "write":
			if strings.TrimSpace(op.content) != "" {
				return true
			}
		case "edit":
			if strings.TrimSpace(op.search) != "" && strings.TrimSpace(op.replace) != "" {
				return true
			}
		}
	}
	return false
}

// responseLooksLikeMarkdownTutorialImplementation detects tutorial replies: fenced Go samples
// plus EDIT:/WRITE: stubs but nothing that would apply to disk.
func responseLooksLikeMarkdownTutorialImplementation(response string) bool {
	if !responseHasSubstantialFencedGoCode(response) {
		return false
	}
	lower := strings.ToLower(response)
	if !strings.Contains(lower, "edit:") && !strings.Contains(lower, "write:") {
		return false
	}
	return !responseHasSubstantiveNativeWriteOrEdit(response)
}

// FormatFencedGoWithoutNativeWriteFeedback tells the polecat how to emit real native edits.
func FormatFencedGoWithoutNativeWriteFeedback() string {
	return strings.TrimSpace(`**Rejected:** Your reply included markdown ` + "```go" + ` code samples but no successful **WRITE:** or **EDIT:** was applied to disk.

Use native tools only:
- **WRITE:** linkshelf/path/file.go  
  then the full file body (no ` + "```go" + ` fence)
- **EDIT:** linkshelf/path/file.go  
  <<<<<<< SEARCH  
  exact lines from **Current file on disk**  
  =======  
  replacement  
  >>>>>>> REPLACE

Do not put CMD:/EDIT:/WRITE: only inside tutorial ` + "```bash" + ` blocks without SEARCH/REPLACE content. Run Verify only after the log shows EDIT:/WRITE: ok.`)
}

func (r *stateRunner) validateImplementationFencedCodeGuard(cmd string) error {
	if r == nil || r.task == nil || r.task.State != "implementation" {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(r.hooks.Track), "implementation") {
		return nil
	}
	if r.turnHadSuccessfulNative {
		return nil
	}
	if !responseLooksLikeMarkdownTutorialImplementation(r.turnResponse) {
		return nil
	}
	if isBeadCloseCommand(cmd) && !r.track.verifyOK {
		return fmt.Errorf("apply code with WRITE: or EDIT: before bd close — markdown ```go fences are not written to disk")
	}
	if r.looksLikeImplementVerifyCommand(cmd) {
		return fmt.Errorf("apply code with WRITE: or EDIT: before Verify — markdown ```go fences are not written to disk")
	}
	return nil
}

func (r *stateRunner) looksLikeImplementVerifyCommand(cmd string) bool {
	if isImplementationVerifyCommandOK(cmd, r.townRoot, r.rig, r.track.activeBead, r.v) {
		return true
	}
	if !orchestrator.WorkflowUsesGo(r.v) {
		return false
	}
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "go test") || strings.Contains(lower, "go build")
}
