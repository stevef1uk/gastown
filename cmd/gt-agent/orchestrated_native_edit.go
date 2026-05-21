package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

const (
	nativeEditSearchMarker  = orchestrator.NativeEditSearchMarker
	nativeEditReplaceMarker = orchestrator.NativeEditReplaceMarker
	nativeEditEndMarker     = orchestrator.NativeEditEndMarker
	nativeEditWriteEnd      = orchestrator.NativeEditWriteEnd
	nativeReadMaxBytes      = 48_000
	nativeWriteMaxBytes     = 512_000
)

type nativeEditOp struct {
	kind    string // read, edit, write
	path    string
	search  string
	replace string
	content string
}

// parseOrchestratedNativeEdits extracts READ:/EDIT:/WRITE: blocks from an LLM response.
func parseOrchestratedNativeEdits(response string) []nativeEditOp {
	filtered := stripOutcomeLinesForCmdParse(response)
	var ops []nativeEditOp
	lines := strings.Split(filtered, "\n")
	var i int
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		upper := strings.ToUpper(trimmed)
		switch {
		case strings.HasPrefix(upper, "READ:"):
			path := strings.TrimSpace(trimmed[len("READ:"):])
			if path != "" {
				ops = append(ops, nativeEditOp{kind: "read", path: path})
			}
			i++
		case strings.HasPrefix(upper, "EDIT:"):
			path := strings.TrimSpace(trimmed[len("EDIT:"):])
			i++
			search, replace, next, ok := parseNativeEditSearchReplace(lines, i)
			if !ok || path == "" {
				i = next
				continue
			}
			ops = append(ops, nativeEditOp{kind: "edit", path: path, search: search, replace: replace})
			i = next
		case strings.HasPrefix(upper, "WRITE:"):
			path := strings.TrimSpace(trimmed[len("WRITE:"):])
			i++
			content, next := parseNativeWriteBody(lines, i)
			if path != "" {
				ops = append(ops, nativeEditOp{kind: "write", path: path, content: content})
			}
			i = next
		default:
			i++
		}
	}
	return ops
}

func parseNativeEditSearchReplace(lines []string, start int) (search, replace string, next int, ok bool) {
	mode := ""
	var searchLines, replaceLines []string
	for i := start; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		switch t {
		case nativeEditSearchMarker:
			mode = "search"
			continue
		case nativeEditReplaceMarker:
			if mode != "search" {
				return "", "", i + 1, false
			}
			mode = "replace"
			continue
		case nativeEditEndMarker:
			if mode != "replace" {
				return "", "", i + 1, false
			}
			search = strings.TrimRight(strings.Join(searchLines, "\n"), "\n")
			replace = strings.TrimRight(strings.Join(replaceLines, "\n"), "\n")
			return search, replace, i + 1, search != ""
		default:
			switch mode {
			case "search":
				searchLines = append(searchLines, lines[i])
			case "replace":
				replaceLines = append(replaceLines, lines[i])
			}
		}
	}
	return "", "", len(lines), false
}

func parseNativeWriteBody(lines []string, start int) (content string, next int) {
	var body []string
	for i := start; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == nativeEditWriteEnd {
			return strings.Join(body, "\n"), i + 1
		}
		body = append(body, lines[i])
	}
	return strings.Join(body, "\n"), len(lines)
}

// processOrchestratedTools runs native READ/EDIT/WRITE and CMD lines from one LLM response.
func (r *stateRunner) processOrchestratedTools(response, sessionName string, combined *strings.Builder) (hadNative bool, cmdCount int) {
	if r.hooks.NativeEditTools {
		ops := parseOrchestratedNativeEdits(response)
		editDir := r.mayorRigWorkDir()
		cmdEnv := r.commandEnv(os.Environ())
		hadNative = r.executeNativeEdits(ops, editDir, sessionName, cmdEnv, combined)
	}
	cmdBlocks := parseOrchestratedCommands(response)
	cmdCount = len(cmdBlocks)
	for _, cmd := range cmdBlocks {
		if strings.Contains(cmd, "CMD:") {
			orchestratedFprintfStderr("[gt-agent] warning: dropping malformed command with embedded CMD:\n")
			continue
		}
		if err := r.validateCommand(cmd); err != nil {
			orchestratedFprintfStderr("[gt-agent] rejected command: %v\n", err)
			combined.WriteString(fmt.Sprintf("Command REJECTED (%s): %s\nReason: %v\n\n", r.rejectScope(), cmd, err))
			continue
		}
		cmd = r.rewriteCommand(cmd)
		r.repairPipBeforeRun(cmd)
		cmdEnv := r.commandEnv(os.Environ())
		cmd = r.rewritePythonCmd(cmd, cmdEnv)
		r.beforeDevServerCommand(cmd)
		orchestratedPrintf("[gt-agent] $ %s\n", cmd)
		if needsOrchestratedScriptFile(cmd) {
			orchestratedPrintf("[gt-agent] running multiline/heredoc via temp script\n")
		}
		workDir := r.workDir()
		if isStandaloneHeredocDelimiter(strings.TrimSpace(cmd)) {
			orchestratedPrintf("[gt-agent] skipping stray heredoc delimiter command: %q\n", cmd)
			combined.WriteString(fmt.Sprintf("Command skipped (stray heredoc delimiter): %s\n\n", cmd))
			continue
		}
		out, cmdErr := r.runShellCommand(cmd, workDir, sessionName, cmdEnv)
		if cmdErr != nil && (benignGoCommandError(cmd, cmdErr, out) || (r.hooks.Artifacts == "planning" && benignPlanningShellNoise(cmd, cmdErr))) {
			orchestratedPrintf("[gt-agent] treating as ok: %v\n", cmdErr)
			combined.WriteString(fmt.Sprintf("Command: %s\n(note: %v — continuing)\nOutput: %s\n\n", cmd, cmdErr, string(out)))
			cmdErr = nil
		}
		r.afterCommand(cmd, cmdErr, workDir, sessionName, cmdEnv, combined)
		if cmdErr != nil {
			orchestratedFprintfStderr("[gt-agent] command failed: %v\n%s\n", cmdErr, string(out))
			combined.WriteString(fmt.Sprintf("Command: %s\nError: %v\nOutput: %s\n\n", cmd, cmdErr, string(out)))
			if r.hooks.AppendGoCompileContext && orchestrator.WorkflowUsesGo(r.v) {
				appendGoCompileSourceContext(combined, r.townRoot, r.rig, rigMayorRigDir(r.townRoot, r.rig), r.v.LayoutRoot,
					r.activeImplementBeadPath(), r.v, cmd, string(out))
			}
			if strings.EqualFold(strings.TrimSpace(r.hooks.Track), "qa") {
				appendQAFailureReportNudge(combined, cmd, cmdErr)
			}
		} else {
			feedbackOut := formatSuccessCommandOutput(out)
			orchestratedPrintf("[gt-agent] output: %s\n", strings.TrimSpace(feedbackOut))
			combined.WriteString(feedbackOut)
		}
	}
	return hadNative, cmdCount
}

func (r *stateRunner) mayorRigWorkDir() string {
	if r.townRoot != "" && r.rig != "" {
		return rigMayorRigDir(r.townRoot, r.rig)
	}
	return r.workDir()
}

func (r *stateRunner) executeNativeEdits(ops []nativeEditOp, editDir, sessionName string, cmdEnv []string, combined *strings.Builder) bool {
	if len(ops) == 0 {
		return false
	}
	any := false
	for _, op := range ops {
		any = true
		feedback, err := r.executeNativeEditOp(op, editDir)
		label := strings.ToUpper(op.kind) + ": " + op.path
		if err != nil {
			r.track.hadCmdFailure = true
			orchestratedFprintfStderr("[gt-agent] %s rejected: %v\n", label, err)
			combined.WriteString(fmt.Sprintf("%s\nError: %v\n\n", label, err))
			continue
		}
		orchestratedPrintf("[gt-agent] %s ok\n", label)
		combined.WriteString(fmt.Sprintf("%s\n%s\n\n", label, feedback))
		if op.kind == "edit" || op.kind == "write" {
			r.runAutoVerifyForNativeLayoutWrite(sessionName, cmdEnv, combined)
		}
	}
	return any
}

func (r *stateRunner) executeNativeEditOp(op nativeEditOp, workDir string) (string, error) {
	rel, abs, err := resolveNativeEditAbsPath(workDir, op.path, r.v.LayoutRoot)
	if err != nil {
		return "", err
	}
	switch op.kind {
	case "read":
		if err := orchestrator.ValidateImplementReadPath(r.townRoot, r.rig, r.track.activeBead, rel, r.v); err != nil {
			return "", err
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return "", err
		}
		if len(data) > nativeReadMaxBytes {
			data = data[:nativeReadMaxBytes]
			return string(data) + "\n...(truncated)", nil
		}
		return string(data), nil
	case "edit":
		if err := orchestrator.ValidateImplementWritePath(r.townRoot, r.rig, r.track.activeBead, rel, r.v, false); err != nil {
			return "", err
		}
		return applyNativeSearchReplace(abs, op.search, op.replace)
	case "write":
		if err := orchestrator.ValidateImplementWritePath(r.townRoot, r.rig, r.track.activeBead, rel, r.v, true); err != nil {
			return "", err
		}
		if len(op.content) > nativeWriteMaxBytes {
			return "", fmt.Errorf("WRITE body too large (%d bytes; max %d)", len(op.content), nativeWriteMaxBytes)
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			return "", err
		}
		if err := os.WriteFile(abs, []byte(op.content), 0644); err != nil {
			return "", err
		}
		return fmt.Sprintf("wrote %d bytes", len(op.content)), nil
	default:
		return "", fmt.Errorf("unknown native edit kind %q", op.kind)
	}
}

func resolveNativeEditAbsPath(workDir, path, layoutRoot string) (rel, abs string, err error) {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" || strings.Contains(path, "..") {
		return "", "", fmt.Errorf("invalid path %q", path)
	}
	layout := strings.Trim(strings.TrimSpace(layoutRoot), "/")
	if layout != "" && !strings.HasPrefix(path, layout+"/") && path != layout {
		if !strings.Contains(path, "/") {
			path = layout + "/" + path
		}
	}
	rel = orchestrator.NormalizeBeadPathForLayout(path, layoutRoot)
	if rel == "" {
		return "", "", fmt.Errorf("invalid path %q", path)
	}
	abs = filepath.Join(workDir, filepath.FromSlash(rel))
	workClean, err := filepath.Abs(workDir)
	if err != nil {
		return "", "", err
	}
	absClean, err := filepath.Abs(abs)
	if err != nil {
		return "", "", err
	}
	if !strings.HasPrefix(absClean, workClean+string(filepath.Separator)) && absClean != workClean {
		return "", "", fmt.Errorf("path %q escapes workdir", rel)
	}
	return rel, absClean, nil
}

func applyNativeSearchReplace(abs, search, replace string) (string, error) {
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	content := string(data)
	n := strings.Count(content, search)
	if n == 0 {
		// Try CRLF-normalized match
		norm := strings.ReplaceAll(content, "\r\n", "\n")
		normSearch := strings.ReplaceAll(search, "\r\n", "\n")
		if strings.Count(norm, normSearch) == 0 {
			return "", fmt.Errorf("SEARCH block not found in file (must match exactly, including whitespace)")
		}
		if strings.Count(norm, normSearch) > 1 {
			return "", fmt.Errorf("SEARCH block matches %d times — make it unique", strings.Count(norm, normSearch))
		}
		updated := strings.Replace(norm, normSearch, strings.ReplaceAll(replace, "\r\n", "\n"), 1)
		if err := os.WriteFile(abs, []byte(updated), 0644); err != nil {
			return "", err
		}
		return "applied 1 search/replace (normalized line endings)", nil
	}
	if n > 1 {
		return "", fmt.Errorf("SEARCH block matches %d times — make it unique", n)
	}
	updated := strings.Replace(content, search, replace, 1)
	if err := os.WriteFile(abs, []byte(updated), 0644); err != nil {
		return "", err
	}
	return "applied 1 search/replace", nil
}

func orchestratedEmptyTurnHint(hooks orchestrator.StateHooks) string {
	if hooks.NativeEditTools {
		return "Use READ:/EDIT:/WRITE: for file changes (see system prompt), or CMD: for bd/verify. When done, reply with JSON only: {\"outcome\":\"...\",\"summary\":\"...\"}"
	}
	return "Use CMD: lines to run shell commands (heredoc for multi-line files). When done, reply with JSON only: {\"outcome\":\"...\",\"summary\":\"...\"}"
}

func (r *stateRunner) runAutoVerifyForNativeLayoutWrite(sessionName string, cmdEnv []string, combined *strings.Builder) {
	// One implementation verify from town root (same as post-heredoc auto_verify for go_write_layout).
	var verifyCmd string
	for _, hook := range r.hooks.AutoVerify {
		if hook.When == "go_write_layout" || hook.When == "python_import_check" || hook.When == "python_compileall" {
			verifyCmd = r.verifyCommand(hook.Verify)
			if verifyCmd != "" {
				break
			}
		}
	}
	if verifyCmd == "" {
		return
	}
	if fixed, ok := rewriteUnittestToWorkdir(verifyCmd, r.rig, r.v); ok {
		verifyCmd = fixed
	}
	workDir := r.workDir()
	verifyOut, verifyErr := r.runShellCommand(verifyCmd, workDir, sessionName, cmdEnv)
	if verifyErr != nil {
		r.track.hadCmdFailure = true
		r.track.verifyOK = false
		combined.WriteString(fmt.Sprintf("Auto-verify (after native edit): %s\nError: %v\nOutput: %s\n\n", verifyCmd, verifyErr, string(verifyOut)))
		if r.hooks.AppendGoCompileContext && orchestrator.WorkflowUsesGo(r.v) {
			appendGoCompileSourceContext(combined, r.townRoot, r.rig, rigMayorRigDir(r.townRoot, r.rig), r.v.LayoutRoot,
				r.activeImplementBeadPath(), r.v, verifyCmd, string(verifyOut))
		}
		return
	}
	r.track.verifyOK = true
	r.track.hadCmdFailure = false
	combined.WriteString(fmt.Sprintf("Auto-verify (after native edit): %s\n%s", verifyCmd, formatSuccessCommandOutput(verifyOut)))
}
