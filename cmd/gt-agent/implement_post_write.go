package main

import (
	"fmt"
	"strings"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

// tidyGoFileAfterNativeWrite runs goimports on relPath when available (fixes unused imports after partial EDITs).
func (r *stateRunner) tidyGoFileAfterNativeWrite(relPath string, combined *strings.Builder) {
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
	ran, err := orchestrator.RunGoimportsOnFile(mayorDir, relPath)
	if !ran {
		return
	}
	if err != nil {
		combined.WriteString(fmt.Sprintf("goimports %s: %v\n\n", relPath, err))
		return
	}
	orchestratedPrintf("[gt-agent] goimports -w %s\n", relPath)
	combined.WriteString(fmt.Sprintf("Ran goimports on %s (unused imports / formatting)\n\n", relPath))
}

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
	if err := orchestrator.ValidateHTTPHandlerBeadPrerequisites(mayorDir, relPath, r.v); err != nil {
		r.track.hadCmdFailure = true
		r.track.verifyOK = false
		combined.WriteString(err.Error() + "\n\n")
		orchestratedFprintfStderr("[gt-agent] %s\n", err)
		return
	}
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
	if err != nil && orchestrator.GoCompileOutputHasUnusedImport(string(out)) {
		if r.tryGoimportsForCompileFailure(mayorDir, string(out), combined) {
			orchestratedPrintf("[gt-agent] retrying verify after goimports package tidy\n")
			out, err = r.runShellCommand(verifyCmd, workDir, sessionName, cmdEnv)
		}
	}
	outStr := string(out)
	if err != nil || orchestrator.GoToolOutputMatchedNoPackages(outStr) {
		if err == nil {
			err = fmt.Errorf("go matched no packages (no .go sources in target path)")
		}
		r.track.hadCmdFailure = true
		r.track.verifyOK = false
		combined.WriteString(fmt.Sprintf("Post-write verify: %s\nError: %v\nOutput: %s\n\n", verifyCmd, err, outStr))
		if r.hooks.AppendGoCompileContext {
			appendGoCompileSourceContext(combined, r.townRoot, r.rig, mayorDir, r.v.LayoutRoot,
				relPath, r.v, verifyCmd, outStr)
			r.noteImplementationVerifyFailure(verifyCmd, outStr)
		}
		return
	}
	r.track.verifyOK = true
	r.track.hadCmdFailure = false
	r.persistImplementationProgress(verifyCmd)
	combined.WriteString(fmt.Sprintf("Post-write verify: %s\n%s", verifyCmd, formatSuccessCommandOutput(out)))
}

// runPostWriteHTTPContract validates cross-file HTTP routing after handler/web writes (GT-VERIFY-007).
func (r *stateRunner) runPostWriteHTTPContract(relPath string, combined *strings.Builder) {
	if !strings.EqualFold(strings.TrimSpace(r.hooks.Track), "implementation") {
		return
	}
	relPath = orchestrator.NormalizeBeadPathForLayout(relPath, r.v.LayoutRoot)
	if !orchestrator.IsHTTPContractRelevantPath(relPath) {
		return
	}
	if err := orchestrator.ValidateHTTPContract(r.townRoot, r.rig, r.v); err != nil {
		r.track.hadCmdFailure = true
		r.track.verifyOK = false
		msg := fmt.Sprintf("HTTP contract check failed after %s: %v\nReconcile handlers.go with web/index.html per architecture.md before bd close.\n\n", relPath, err)
		combined.WriteString(msg)
		orchestratedFprintfStderr("[gt-agent] %s", msg)
		errStr := err.Error()
		if strings.Contains(errStr, "RequestURI") || strings.Contains(errStr, "ServeMux") {
			if hint := orchestrator.FormatHandlerTraversalRedirectHint(r.townRoot, r.rig, relPath, r.v); hint != "" {
				combined.WriteString(hint + "\n\n")
			}
		}
	}
}
