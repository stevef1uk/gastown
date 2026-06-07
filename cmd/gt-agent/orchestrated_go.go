package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/orchestrator"
	rigpkg "github.com/steveyegge/gastown/internal/rig"
)

// writesGoModuleFilesViaHeredoc reports heredoc/redirect writes to go.mod or go.sum.
func writesGoModuleFilesViaHeredoc(cmd string) bool {
	lower := strings.ToLower(cmd)
	if !strings.Contains(lower, "<<") && !strings.Contains(lower, "cat >") && !strings.Contains(lower, "cat>>") {
		return false
	}
	return strings.Contains(lower, "go.mod") || strings.Contains(lower, "go.sum")
}

func isGoModInitCommand(cmd string) bool {
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "go mod init")
}

func destroysGoMod(cmd string) bool {
	lower := strings.ToLower(cmd)
	if !strings.Contains(lower, "rm ") && !strings.Contains(lower, "unlink ") {
		return false
	}
	return strings.Contains(lower, "go.mod") || strings.Contains(lower, "go.sum")
}

func goRequiredFilesUseCmdTree(v orchestrator.WorkflowValidation) bool {
	for _, f := range v.UnionRequiredFiles() {
		if strings.Contains(filepath.ToSlash(f), "/cmd/") {
			return true
		}
	}
	return false
}

func commandRemovesLayoutTree(cmd string, v orchestrator.WorkflowValidation) bool {
	lower := strings.ToLower(cmd)
	if !strings.Contains(lower, "rm ") {
		return false
	}
	if strings.Contains(lower, ".beads") {
		return false
	}
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	if layout != "" && layout != "." {
		if strings.Contains(lower, "rm -rf "+layout) || strings.Contains(lower, "rm -r "+layout) {
			return true
		}
		if strings.Contains(lower, "rm -rf cmd") || strings.Contains(lower, "rm -r cmd") {
			return true
		}
	}
	return strings.Contains(lower, "rm -rf") &&
		(strings.Contains(lower, "main.go") || strings.Contains(lower, "main_test.go") || strings.Contains(lower, "/cmd"))
}

func isGoModTidyCommand(cmd string) bool {
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "go mod tidy")
}

// benignGoCommandError reports errors that are safe to ignore (e.g. go mod init when go.mod exists).
func benignGoCommandError(cmd string, cmdErr error, out []byte) bool {
	if cmdErr == nil {
		return false
	}
	if isGoModInitCommand(cmd) {
		msg := strings.ToLower(string(out) + " " + cmdErr.Error())
		return strings.Contains(msg, "already exists")
	}
	return false
}

func isLLMPlaceholderCommand(cmd string) bool {
	if strings.Contains(cmd, "<") && strings.Contains(cmd, ">") {
		start := strings.Index(cmd, "<")
		end := strings.LastIndex(cmd, ">")
		if start >= 0 && end > start {
			inner := strings.TrimSpace(cmd[start+1 : end])
			if inner != "" && !strings.Contains(inner, " ") {
				return true
			}
			lower := strings.ToLower(inner)
			if strings.Contains(lower, "specific-") || strings.Contains(lower, "module") ||
				strings.Contains(lower, "deps") {
				return true
			}
		}
	}
	if strings.Contains(cmd, "**") {
		return true
	}
	return false
}

func validateProjectSetupCommand(cmd, rig string, v orchestrator.WorkflowValidation) error {
	if err := rigpkg.RejectMayorRigRootShellCommand(cmd, v.LayoutRoot); err != nil {
		return err
	}
	lower := strings.ToLower(cmd)
	if isLLMPlaceholderCommand(cmd) {
		return fmt.Errorf("do not run markdown example placeholders — use real package names and paths")
	}
	if strings.Contains(cmd, "CMD:") {
		return fmt.Errorf("one shell command per CMD: line — do not glue CMD: markers inside a command")
	}
	if strings.Contains(lower, "```") {
		return fmt.Errorf("do not wrap commands in markdown code fences")
	}
	if strings.Contains(lower, "gt bd ") {
		return fmt.Errorf("use bare `bd` from %s", rigMayorRigPath(rig))
	}
	if orchestrator.WorkflowUsesPython(v) {
		if err := validatePythonProjectSetupCommand(cmd, v); err != nil {
			return err
		}
		if written := orchestrator.ExtractImplementWritePathFromCmd(cmd, v.LayoutRoot); written != "" {
			if req := v.RequirementsFilePath(); req == "" || !orchestrator.PathMatchesRequiredFile(written, req) {
				return fmt.Errorf("project_setup may only write %s (not %q) before implementation", req, written)
			}
		}
		return nil
	}
	if !orchestrator.WorkflowUsesGo(v) {
		return nil
	}
	if isBeadCreateCommand(cmd) {
		if title := extractBeadCreateTitle(cmd); title != "" {
			if err := orchestrator.ValidateImplementBeadCreateTitle(title, v); err != nil {
				return err
			}
		}
	}
	for _, blocked := range []string{"go build", "go run", "go test", "go vet", "curl "} {
		if strings.Contains(lower, blocked) {
			return fmt.Errorf("project_setup only runs go mod init/get/tidy — polecat runs %s after setup", strings.TrimSpace(blocked))
		}
	}
	if writesGoModuleFilesViaHeredoc(cmd) {
		return fmt.Errorf("do not write go.mod or go.sum via heredoc — use go mod init and go mod tidy")
	}
	if strings.Contains(lower, "python3") || strings.Contains(lower, "pip install") {
		return fmt.Errorf("project_setup is for Go scaffold and beads only — no Python in this step")
	}
	if commandWritesImplementationTree(lower, v.LayoutRoot) {
		return fmt.Errorf("project_setup scaffolds go.mod and beads only — polecat implements code under %s/", v.LayoutRootDir())
	}
	for _, ext := range []string{".go", ".html", ".js", ".css", ".py"} {
		if !strings.Contains(lower, ext) {
			continue
		}
		if strings.Contains(lower, "cat >") || strings.Contains(lower, "<<") ||
			strings.Contains(lower, "echo ") && strings.Contains(lower, ">") {
			return fmt.Errorf("project_setup must not write source files (%s) — use go mod init/tidy and bd only", ext)
		}
	}
	if strings.Contains(lower, "touch ") {
		return fmt.Errorf("project_setup must not touch files — polecat creates implementation files")
	}
	return nil
}

// commandWritesImplementationTree reports mkdir/touch/heredoc writes to implementation paths.
func commandWritesImplementationTree(lower, layoutRoot string) bool {
	if strings.Contains(lower, "bd create") || strings.Contains(lower, "bd delete") ||
		strings.Contains(lower, "bd list") || strings.Contains(lower, "bd show") || strings.Contains(lower, "bd update") {
		return false
	}
	layout := strings.Trim(strings.TrimSpace(layoutRoot), "/")
	if layout == "" {
		layout = "backend"
	}
	layoutSlash := strings.ToLower(layout) + "/"
	hasLayoutPath := strings.Contains(lower, layoutSlash) ||
		strings.Contains(lower, "/"+layoutSlash) ||
		strings.Contains(lower, " "+layoutSlash)
	if !hasLayoutPath && layout != "backend" {
		// Legacy python rigs without layout in cmd still use backend/ checks via commandWritesBackend.
		return commandWritesBackend(lower)
	}
	if strings.Contains(lower, "touch ") && hasLayoutPath {
		return true
	}
	if strings.Contains(lower, "mkdir") && hasLayoutPath {
		// Allow mkdir -p <layout> only; block deep package trees (polecat creates paths).
		if strings.Count(lower, layoutSlash) > 0 {
			after := lower[strings.Index(lower, layoutSlash)+len(layoutSlash):]
			if strings.Contains(after, "/") {
				return true
			}
		}
	}
	srcExt := []string{".go", ".html", ".js", ".css", ".py", ".ts", ".tsx"}
	for _, ext := range srcExt {
		if !strings.Contains(lower, ext) {
			continue
		}
		if strings.Contains(lower, "cat >") || strings.Contains(lower, "<<") ||
			strings.Contains(lower, "tee ") || strings.Contains(lower, ext+">") ||
			(strings.Contains(lower, "echo ") && strings.Contains(lower, ">")) {
			return true
		}
	}
	return false
}

func validateProjectSetupArtifacts(townRoot, rig string, hadCmdFailure, verifyOK bool, v orchestrator.WorkflowValidation) error {
	if hadCmdFailure {
		return fmt.Errorf("project_setup had failed commands; fix errors before completing")
	}
	if orchestrator.WorkflowUsesPython(v) {
		return validatePythonProjectSetupArtifacts(townRoot, rig, hadCmdFailure, verifyOK, v)
	}
	if !orchestrator.WorkflowUsesGo(v) {
		return nil
	}
	rigDir := rigMayorRigDir(townRoot, rig)
	if !verifyOK {
		return fmt.Errorf("project_setup requires green verify: %s", orchestrator.GoProjectSetupVerifyCommand(v, rigDir))
	}
	goMod := orchestrator.ResolveRequiredFileOnDisk(rigDir, "go.mod", v.LayoutRoot)
	if !strings.HasSuffix(filepath.ToSlash(goMod), "go.mod") {
		layout := strings.TrimSpace(v.LayoutRoot)
		if layout == "" {
			layout = "."
		}
		goMod = filepath.Join(rigDir, layout, "go.mod")
	}
	if _, err := os.Stat(goMod); err != nil {
		return fmt.Errorf("go.mod missing at %s after setup", goMod)
	}
	if err := orchestrator.ValidatePlanningPhaseGate(townRoot, rig, "project_setup", v); err != nil {
		return err
	}
	return nil
}

func validateGoImplementationCommand(cmd, townRoot, rig, mayorRigDir, activeBead string, v orchestrator.WorkflowValidation, verifyOK bool) error {
	if !orchestrator.WorkflowUsesGo(v) {
		return nil
	}
	if writesGoModuleFilesViaHeredoc(cmd) {
		return fmt.Errorf("do not write go.mod or go.sum via heredoc — use go mod init, go get, and go mod tidy in project_setup or before bd close")
	}
	if destroysGoMod(cmd) {
		return fmt.Errorf("do not delete go.mod or go.sum — use go mod edit, go get, and go mod tidy")
	}
	if commandRemovesLayoutTree(cmd, v) {
		return fmt.Errorf("do not rm source trees under %s/ during implementation — use EDIT:/WRITE: on the active bead", v.LayoutRootDir())
	}
	lower := strings.ToLower(cmd)
	if strings.Contains(lower, "./cmd/") || strings.Contains(lower, " cmd/") {
		if strings.Contains(lower, "go build") || strings.Contains(lower, "go run") || strings.Contains(lower, "go test") {
			if !goRequiredFilesUseCmdTree(v) {
				return fmt.Errorf("required_files use flat layout under %s/ (main.go at module root) — do not use cmd/ paths from linkshelf examples", v.LayoutRootDir())
			}
		}
	}
	beadPath := orchestrator.ImplementBeadPathForID(townRoot, rig, activeBead, v)
	verifyHint := orchestrator.AgentShellVerifyCommand(rig, v, mayorRigDir, beadPath)
	onGoModBead := strings.HasSuffix(filepath.ToSlash(beadPath), "go.mod")
	onServerMainBead := orchestrator.IsServerMainImplementBead(beadPath)
	if strings.Contains(lower, "go run") || strings.Contains(lower, "curl ") {
		if onGoModBead || !onServerMainBead || !orchestrator.GoServerMainExists(mayorRigDir, v) {
			return fmt.Errorf("for this bead use compile verify only — use: %s", verifyHint)
		}
	}
	if orchestrator.IsFrontendImplementPath(beadPath) && strings.Contains(lower, "go test") {
		return fmt.Errorf("frontend bead %s: go test does not apply to web assets — use EDIT:/WRITE: on the file, then bd close after the artifact validates", beadPath)
	}
	if isBeadCloseCommand(cmd) && !verifyOK {
		if orchestrator.IsFrontendImplementPath(beadPath) {
			if err := orchestrator.ValidateBeadArtifactOnDisk(mayorRigDir, beadPath, v); err != nil {
				return fmt.Errorf("cannot bd close %s: %w — fix the file with EDIT:/WRITE: first", activeBead, err)
			}
			// Frontend beads use post-write artifact validation, not go test. Allow close when the
			// artifact is valid (verifyOK may be cleared by deferred HTTP contract notes).
			return nil
		}
		return fmt.Errorf("run green verify before bd close: %s (in this session, since verify clears on restart)", verifyHint)
	}
	return nil
}
