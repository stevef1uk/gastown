package main

import (
	"fmt"
	"strings"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

// runPostNativeWriteVerify runs compile/test for the edited package immediately after WRITE/EDIT.
func (r *stateRunner) runPostNativeWriteVerify(relPath string, sessionName string, cmdEnv []string, combined *strings.Builder) {
	if !strings.EqualFold(strings.TrimSpace(r.hooks.Track), "implementation") {
		return
	}
	if !orchestrator.WorkflowUsesGo(r.v) {
		return
	}
	relPath = orchestrator.NormalizeBeadPathForLayout(relPath, r.v.LayoutRoot)
	if relPath == "" || !strings.HasSuffix(relPath, ".go") {
		return
	}
	mayorDir := rigMayorRigDir(r.townRoot, r.rig)
	verifyCmd := orchestrator.GoCompileVerifyCommandForBead(r.v, mayorDir, relPath)
	if verifyCmd == "" {
		return
	}
	if fixed, ok := rewriteUnittestToWorkdir(verifyCmd, r.rig, r.v); ok {
		verifyCmd = fixed
	}
	workDir := r.workDir()
	orchestratedPrintf("[gt-agent] post-write verify: %s\n", verifyCmd)
	out, err := r.runShellCommand(verifyCmd, workDir, sessionName, cmdEnv)
	if err != nil {
		r.track.hadCmdFailure = true
		r.track.verifyOK = false
		combined.WriteString(fmt.Sprintf("Post-write verify: %s\nError: %v\nOutput: %s\n\n", verifyCmd, err, string(out)))
		if r.hooks.AppendGoCompileContext {
			appendGoCompileSourceContext(combined, r.townRoot, r.rig, mayorDir, r.v.LayoutRoot,
				relPath, r.v, verifyCmd, string(out))
			r.noteImplementationVerifyFailure(verifyCmd, string(out))
		}
		return
	}
	r.track.verifyOK = true
	r.track.hadCmdFailure = false
	r.persistImplementationProgress(verifyCmd)
	combined.WriteString(fmt.Sprintf("Post-write verify: %s\n%s", verifyCmd, formatSuccessCommandOutput(out)))
}
