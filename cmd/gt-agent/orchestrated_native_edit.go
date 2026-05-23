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
const (
	maxNativeOpsPerTurn   = 12
	maxNativeReadsPerTurn = 3
)

func parseOrchestratedNativeEdits(response string) []nativeEditOp {
	filtered := preprocessOrchestratedResponse(response)
	filtered = stripOutcomeLinesForCmdParse(filtered)
	var ops []nativeEditOp
	lines := strings.Split(filtered, "\n")
	var i int
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		upper := strings.ToUpper(trimmed)
		switch {
		case strings.HasPrefix(upper, "READ:"):
			path := orchestrator.SanitizeNativeEditRelPath(trimmed[len("READ:"):])
			if path != "" {
				ops = append(ops, nativeEditOp{kind: "read", path: path})
			}
			i++
		case strings.HasPrefix(upper, "EDIT:"):
			path := orchestrator.SanitizeNativeEditRelPath(trimmed[len("EDIT:"):])
			i++
			if path != "" && !orchestrator.IsValidImplementBeadPath(path) {
				i = skipNativeEditBlock(lines, i)
				continue
			}
			search, replace, next, ok := parseNativeEditSearchReplace(lines, i)
			if !ok || path == "" {
				i = next
				continue
			}
			ops = append(ops, nativeEditOp{kind: "edit", path: path, search: search, replace: replace})
			i = next
		case strings.HasPrefix(upper, "WRITE:"):
			path := orchestrator.SanitizeNativeEditRelPath(trimmed[len("WRITE:"):])
			i++
			if path != "" && !orchestrator.IsValidImplementBeadPath(path) {
				continue
			}
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
		switch {
		case t == nativeEditSearchMarker:
			mode = "search"
		case t == nativeEditReplaceMarker:
			if mode != "search" {
				return "", "", i + 1, false
			}
			mode = "replace"
		case isNativeEditEndMarker(t):
			if mode != "replace" {
				return "", "", i + 1, false
			}
			search = strings.TrimRight(strings.Join(searchLines, "\n"), "\n")
			replace = strings.TrimRight(strings.Join(replaceLines, "\n"), "\n")
			return search, replace, i + 1, search != ""
		default:
			if isMarkdownFenceOnlyLine(t) {
				continue
			}
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

// skipNativeEditBlock advances past a malformed EDIT block (e.g. prose path, missing SEARCH).
func skipNativeEditBlock(lines []string, start int) int {
	for i := start; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		tu := strings.ToUpper(t)
		if strings.HasPrefix(tu, "EDIT:") || strings.HasPrefix(tu, "WRITE:") ||
			strings.HasPrefix(tu, "READ:") || strings.HasPrefix(tu, "CMD:") {
			return i
		}
		if isNativeEditEndMarker(t) || t == nativeEditReplaceMarker || strings.HasPrefix(t, ">>>>>>>") {
			return i + 1
		}
	}
	return len(lines)
}

func isNativeOrchestratedToolLine(line string) bool {
	upper := strings.ToUpper(strings.TrimSpace(line))
	return strings.HasPrefix(upper, "CMD:") ||
		strings.HasPrefix(upper, "READ:") ||
		strings.HasPrefix(upper, "EDIT:") ||
		strings.HasPrefix(upper, "WRITE:")
}

func parseNativeWriteBody(lines []string, start int) (content string, next int) {
	var body []string
	for i := start; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == nativeEditWriteEnd || strings.EqualFold(trimmed, "---END WRITE---") {
			return strings.Join(body, "\n"), i + 1
		}
		if isNativeOrchestratedToolLine(lines[i]) {
			return strings.Join(body, "\n"), i
		}
		// Model often closes a markdown fence instead of ---END WRITE---.
		if trimmed == "```" && len(body) > 0 {
			return strings.Join(body, "\n"), i + 1
		}
		body = append(body, lines[i])
	}
	return strings.Join(body, "\n"), len(lines)
}

// processOrchestratedTools runs native READ/EDIT/WRITE and CMD lines from one LLM response.
func (r *stateRunner) processOrchestratedTools(response, sessionName string, combined *strings.Builder) (hadNative bool, hadSuccessfulNative bool, cmdCount int) {
	if hint := FormatMalformedNativeEditFeedback(response); hint != "" {
		combined.WriteString(hint)
		combined.WriteString("\n\n")
	}
	var ranPreInProgress bool
	if r.hooks.NativeEditTools {
		r.syncActiveImplementBeadFromQueue()
		ranPreInProgress = r.runInProgressBeadUpdatesBeforeNativeEdits(response, sessionName, combined)
		ops := parseOrchestratedNativeEdits(response)
		reads := 0
		var capped []nativeEditOp
		for _, op := range ops {
			if len(capped) >= maxNativeOpsPerTurn {
				break
			}
			if op.kind == "read" {
				reads++
				if reads > maxNativeReadsPerTurn {
					continue
				}
			}
			capped = append(capped, op)
		}
		if len(ops) > len(capped) {
			orchestratedPrintf("[gt-agent] capped native tools %d → %d (max %d ops, %d reads)\n",
				len(ops), len(capped), maxNativeOpsPerTurn, maxNativeReadsPerTurn)
		}
		ops = capped
		editDir := r.mayorRigWorkDir()
		cmdEnv := r.commandEnv(os.Environ())
		hadNative, hadSuccessfulNative = r.executeNativeEdits(ops, editDir, sessionName, cmdEnv, combined)
	}
	cmdBlocks := parseOrchestratedCommands(response)
	cmdCount = len(cmdBlocks)
	for _, cmd := range cmdBlocks {
		if ranPreInProgress && isBeadUpdateInProgressCommand(cmd) {
			continue
		}
		if strings.Contains(cmd, "CMD:") {
			orchestratedFprintfStderr("[gt-agent] warning: dropping malformed command with embedded CMD:\n")
			continue
		}
		if isOrchestratedNativeToolLine(cmd) {
			orchestratedFprintfStderr("[gt-agent] warning: dropping native tool line mistaken for shell: %q\n", cmd)
			combined.WriteString(fmt.Sprintf("Command skipped (native tool line, not shell): %s\n\n", cmd))
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
		if isBdInfrastructureFailure(cmdErr, string(out)) {
			r.track.bdInfraFailed = true
		}
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
				outStr := string(out)
				if orchestrator.GoCompileOutputHasUnusedImport(outStr) {
					r.tryGoimportsForCompileFailure(rigMayorRigDir(r.townRoot, r.rig), outStr, combined)
				}
				appendGoCompileSourceContext(combined, r.townRoot, r.rig, rigMayorRigDir(r.townRoot, r.rig), r.v.LayoutRoot,
					r.activeImplementBeadPath(), r.v, cmd, outStr)
				if strings.EqualFold(strings.TrimSpace(r.hooks.Track), "implementation") {
					r.noteImplementationVerifyFailure(cmd, outStr)
				}
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
	return hadNative, hadSuccessfulNative, cmdCount
}

func isNativeEditSearchNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "SEARCH block not found")
}

func isMarkdownFenceOnlyLine(t string) bool {
	t = strings.TrimSpace(t)
	if t == "```" {
		return true
	}
	if strings.HasPrefix(t, "```") {
		lang := strings.TrimSpace(strings.TrimPrefix(t, "```"))
		switch strings.ToLower(lang) {
		case "", "go", "golang", "python", "py", "bash", "sh", "shell", "text", "json":
			return true
		}
	}
	return false
}

// reconcileActiveImplementBeadWithQueue clears a stale persisted active bead when the queue head moved on.
func (r *stateRunner) reconcileActiveImplementBeadWithQueue() {
	if r.track == nil || !strings.EqualFold(strings.TrimSpace(r.hooks.Track), "implementation") {
		return
	}
	if len(r.v.RequiredFiles) == 0 {
		return
	}
	next, err := orchestrator.NextOpenImplementBead(r.townRoot, r.rig, r.v)
	if err != nil || next == nil || next.ID == "" {
		return
	}
	active := strings.TrimSpace(r.track.activeBead)
	if active == "" {
		r.track.activeBead = next.ID
		r.track.activeBeadPath = orchestrator.ImplementBeadPathForID(r.townRoot, r.rig, next.ID, r.v)
		return
	}
	if active == next.ID {
		return
	}
	orchestratedPrintf("[gt-agent] clearing stale active bead %s (queue head is %s); use CMD: bd update %s --status=in_progress before EDIT\n",
		active, next.ID, next.ID)
	r.track.activeBead = ""
	r.track.activeBeadPath = ""
	r.track.verifyOK = false
	if r.implProgress != nil && r.implProgress.ActiveBead == active {
		r.implProgress.ActiveBead = ""
		r.implProgress.ActiveBeadPath = ""
		if err := saveImplementationProgress(r.townRoot, r.rig, r.implProgress); err != nil {
			orchestratedFprintfStderr("[gt-agent] implementation progress save: %v\n", err)
		}
	}
}

// syncActiveImplementBeadFromQueue aligns track.activeBead with the implementation queue head.
func (r *stateRunner) syncActiveImplementBeadFromQueue() {
	r.reconcileActiveImplementBeadWithQueue()
}

// runInProgressBeadUpdatesBeforeNativeEdits runs bd update --status=in_progress before EDIT/WRITE in the same turn.
func (r *stateRunner) runInProgressBeadUpdatesBeforeNativeEdits(response, sessionName string, combined *strings.Builder) bool {
	if !strings.EqualFold(strings.TrimSpace(r.hooks.Track), "implementation") {
		return false
	}
	var ran bool
	for _, cmd := range parseOrchestratedCommands(response) {
		if !isBeadUpdateInProgressCommand(cmd) {
			continue
		}
		if err := r.validateCommand(cmd); err != nil {
			orchestratedFprintfStderr("[gt-agent] pre-native rejected command: %v\n", err)
			combined.WriteString(fmt.Sprintf("Command REJECTED (%s, before native tools): %s\nReason: %v\n\n", r.rejectScope(), cmd, err))
			continue
		}
		cmd = r.rewriteCommand(cmd)
		r.repairPipBeforeRun(cmd)
		cmdEnv := r.commandEnv(os.Environ())
		cmd = r.rewritePythonCmd(cmd, cmdEnv)
		r.beforeDevServerCommand(cmd)
		orchestratedPrintf("[gt-agent] (pre-native) $ %s\n", cmd)
		workDir := r.workDir()
		out, cmdErr := r.runShellCommand(cmd, workDir, sessionName, cmdEnv)
		r.afterCommand(cmd, cmdErr, workDir, sessionName, cmdEnv, combined)
		if cmdErr != nil {
			orchestratedFprintfStderr("[gt-agent] pre-native command failed: %v\n%s\n", cmdErr, string(out))
			combined.WriteString(fmt.Sprintf("Command: %s\nError: %v\nOutput: %s\n\n", cmd, cmdErr, string(out)))
		} else {
			combined.WriteString(formatSuccessCommandOutput(out))
		}
		ran = true
	}
	return ran
}

func (r *stateRunner) mayorRigWorkDir() string {
	if r.townRoot != "" && r.rig != "" {
		return rigMayorRigDir(r.townRoot, r.rig)
	}
	return r.workDir()
}

func (r *stateRunner) executeNativeEdits(ops []nativeEditOp, editDir, sessionName string, cmdEnv []string, combined *strings.Builder) (bool, bool) {
	if len(ops) == 0 {
		return false, false
	}
	any := false
	success := false
	for _, op := range ops {
		any = true
		feedback, err := r.executeNativeEditOp(op, editDir)
		label := strings.ToUpper(op.kind) + ": " + op.path
		if err != nil {
			r.track.hadCmdFailure = true
			orchestratedFprintfStderr("[gt-agent] %s rejected: %v\n", label, err)
			combined.WriteString(fmt.Sprintf("%s\nError: %v\n\n", label, err))
			if op.kind == "edit" && isNativeEditSearchNotFound(err) {
				r.attemptEditSearchMiss = true
				r.appendAutoReadAfterEditSearchMiss(combined, op.path, editDir)
			}
			continue
		}
		if op.kind == "edit" || op.kind == "write" {
			success = true
			r.attemptFixWork = true
		}
		orchestratedPrintf("[gt-agent] %s ok\n", label)
		combined.WriteString(fmt.Sprintf("%s\n%s\n\n", label, feedback))
		if op.kind == "edit" || op.kind == "write" {
			r.tidyGoFileAfterNativeWrite(op.path, combined)
			r.runPostNativeWriteVerify(op.path, sessionName, cmdEnv, combined)
			r.runAutoVerifyForNativeLayoutWrite(sessionName, cmdEnv, combined)
		}
	}
	return any, success
}

func (r *stateRunner) appendAutoReadAfterEditSearchMiss(combined *strings.Builder, relPath, editDir string) {
	relPath = orchestrator.SanitizeNativeEditRelPath(relPath)
	if relPath == "" {
		return
	}
	op := nativeEditOp{kind: "read", path: relPath}
	feedback, err := r.executeNativeEditOp(op, editDir)
	if err != nil {
		combined.WriteString(fmt.Sprintf("Auto-READ %s failed: %v\n\n", relPath, err))
		return
	}
	orchestratedPrintf("[gt-agent] auto-READ after SEARCH miss: %s\n", relPath)
	combined.WriteString(fmt.Sprintf("### Auto-READ after SEARCH miss (%s)\n%s\n\n", relPath, feedback))
	combined.WriteString(fmt.Sprintf("Retry **EDIT:** %s with `<<<<<<< SEARCH` copied exactly from Auto-READ above (or ### Current file on disk), then run Verify.\n\n", relPath))
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
			if nudgeErr := nativeReadMissingFileError(r.townRoot, r.rig, r.track.activeBead, r.track.activeBeadPath, rel, r.v, err); nudgeErr != err {
				return "", nudgeErr
			}
			return "", err
		}
		if len(data) > nativeReadMaxBytes {
			data = data[:nativeReadMaxBytes]
			return string(data) + "\n...(truncated)", nil
		}
		return string(data), nil
	case "edit":
		if err := orchestrator.ValidateImplementWritePath(r.townRoot, r.rig, r.track.activeBead, rel, r.v, false, r.track.lastVerifyOutput); err != nil {
			return "", err
		}
		replace := sanitizeNativeFileContent(op.replace)
		return applyNativeSearchReplaceValidated(rel, abs, op.search, replace)
	case "write":
		if err := orchestrator.ValidateImplementWritePath(r.townRoot, r.rig, r.track.activeBead, rel, r.v, true, r.track.lastVerifyOutput); err != nil {
			return "", err
		}
		if len(op.content) > nativeWriteMaxBytes {
			return "", fmt.Errorf("WRITE body too large (%d bytes; max %d)", len(op.content), nativeWriteMaxBytes)
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			return "", err
		}
		content := sanitizeNativeFileContent(op.content)
		if err := validateNativeGoContent(rel, content); err != nil {
			return "", err
		}
		if err := os.WriteFile(abs, []byte(content), 0644); err != nil {
			return "", err
		}
		return fmt.Sprintf("wrote %d bytes", len(content)), nil
	default:
		return "", fmt.Errorf("unknown native edit kind %q", op.kind)
	}
}

func resolveNativeEditAbsPath(workDir, path, layoutRoot string) (rel, abs string, err error) {
	path = orchestrator.SanitizeNativeEditRelPath(path)
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
	if rel == "" || !orchestrator.IsValidImplementBeadPath(rel) {
		return "", "", fmt.Errorf("invalid path %q (use repo-relative paths like %s/internal/foo.go, not markdown prose)", path, strings.Trim(layoutRoot, "/"))
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
	return applyNativeSearchReplaceValidated(filepath.Base(abs), abs, search, replace)
}

func validateNativeGoContent(relPath, content string) error {
	if !strings.HasSuffix(filepath.ToSlash(relPath), ".go") {
		return nil
	}
	if !strings.Contains(content, "package ") {
		return nil
	}
	if strings.Contains(content, "}; if err") || strings.Contains(content, "}||") || strings.Contains(content, "Descriptionn") {
		return fmt.Errorf("EDIT/WRITE body contains merged patch fragments — use one full WRITE with a complete file per SPEC Store contract")
	}
	if err := orchestrator.GoSourceBytesValid([]byte(content)); err != nil {
		return fmt.Errorf("Go syntax invalid — fix WRITE/EDIT body before saving (%v). If the file on disk is already broken, use one full WRITE with a complete file per SPEC", err)
	}
	return nil
}

func applyNativeSearchReplaceValidated(relPath, abs, search, replace string) (string, error) {
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	content := string(data)
	updated, msg, err := computeNativeSearchReplace(content, search, replace)
	if err != nil {
		return "", err
	}
	if err := validateNativeGoContent(relPath, updated); err != nil {
		return "", err
	}
	if err := os.WriteFile(abs, []byte(updated), 0644); err != nil {
		return "", err
	}
	return msg, nil
}

func computeNativeSearchReplace(content, search, replace string) (updated, msg string, err error) {
	n := strings.Count(content, search)
	if n == 0 {
		norm := strings.ReplaceAll(content, "\r\n", "\n")
		normSearch := strings.ReplaceAll(search, "\r\n", "\n")
		if strings.Count(norm, normSearch) == 0 {
			return "", "", fmt.Errorf("SEARCH block not found in file (must match exactly, including whitespace)")
		}
		if strings.Count(norm, normSearch) > 1 {
			return "", "", fmt.Errorf("SEARCH block matches %d times — make it unique", strings.Count(norm, normSearch))
		}
		updated = strings.Replace(norm, normSearch, strings.ReplaceAll(replace, "\r\n", "\n"), 1)
		return updated, "applied 1 search/replace (normalized line endings)", nil
	}
	if n > 1 {
		return "", "", fmt.Errorf("SEARCH block matches %d times — make it unique", n)
	}
	updated = strings.Replace(content, search, replace, 1)
	return updated, "applied 1 search/replace", nil
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
			outStr := string(verifyOut)
			appendGoCompileSourceContext(combined, r.townRoot, r.rig, rigMayorRigDir(r.townRoot, r.rig), r.v.LayoutRoot,
				r.activeImplementBeadPath(), r.v, verifyCmd, outStr)
			r.noteImplementationVerifyFailure(verifyCmd, outStr)
		}
		return
	}
	r.track.verifyOK = true
	r.track.hadCmdFailure = false
	r.persistImplementationProgress(verifyCmd)
	combined.WriteString(fmt.Sprintf("Auto-verify (after native edit): %s\n%s", verifyCmd, formatSuccessCommandOutput(verifyOut)))
}
