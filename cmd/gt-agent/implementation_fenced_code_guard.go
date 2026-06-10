package main

import (
	"fmt"
	"os"
	"path/filepath"
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
	return strings.TrimSpace(`**Rejected:** no WRITE: or EDIT: applied to disk. Use:**WRITE:** path/file  (then body, no ` + "```go" + ` fence)  or  **EDIT:** path/file  <<<<<<< SEARCH  exact-lines  =======  replacement  >>>>>>> REPLACE`)
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
	if extractAndApplyFencedGoCode(r) {
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

func extractAndApplyFencedGoCode(r *stateRunner) bool {
	if r == nil || r.townRoot == "" || r.rig == "" {
		return false
	}
	beadPath := strings.TrimSpace(r.activeImplementBeadPath())
	if beadPath == "" {
		next, err := orchestrator.NextOpenImplementBead(r.townRoot, r.rig, r.v)
		if err != nil || next == nil {
			return false
		}
		beadPath = orchestrator.NormalizeBeadPathForLayout(
			orchestrator.ExtractPathFromBeadTitle(next.Title, r.v.BeadTitleContains), r.v.LayoutRoot)
	}
	if beadPath == "" {
		return false
	}
	goCode := extractFencedGoCode(r.turnResponse)
	if goCode == "" {
		return false
	}
	rigDir := rigMayorRigDir(r.townRoot, r.rig)
	path := filepath.Join(rigDir, filepath.FromSlash(beadPath))
	if _, err := os.Stat(path); err == nil {
		return false
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return false
	}
	if err := os.WriteFile(path, []byte(goCode), 0644); err != nil {
		return false
	}
	orchestratedPrintf("[gt-agent] auto-extracted ```go code to %s (%d bytes)\n", beadPath, len(goCode))
	r.turnHadSuccessfulNative = true
	return true
}

func extractFencedGoCode(response string) string {
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
			return strings.TrimSpace(body.String()) + "\n"
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
	return ""
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
