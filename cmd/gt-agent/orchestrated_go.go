package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/orchestrator"
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

func isGoModTidyCommand(cmd string) bool {
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, "go mod tidy")
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
		if err := validatePythonProjectSetupCommand(cmd); err != nil {
			return err
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
	if !verifyOK {
		return fmt.Errorf("project_setup requires green verify: %s", orchestrator.GoProjectSetupVerifyCommand(v))
	}
	layout := strings.TrimSpace(v.LayoutRoot)
	if layout == "" {
		layout = "."
	}
	goMod := filepath.Join(rigMayorRigDir(townRoot, rig), layout, "go.mod")
	if _, err := os.Stat(goMod); err != nil {
		return fmt.Errorf("go.mod missing at %s after setup", goMod)
	}
	if len(v.RequiredFiles) > 0 {
		open, err := orchestrator.ListOpenImplementBeads(townRoot, rig, v)
		if err != nil {
			return fmt.Errorf("list implement beads: %w", err)
		}
		archPath := filepath.Join(rigMayorRigDir(townRoot, rig), "architecture.md")
		if err := orchestrator.ValidatePlanBeads(open, archPath, v); err != nil {
			return fmt.Errorf("bead set must match required_files exactly (bd delete junk, one bead per path): %w", err)
		}
	}
	return nil
}

func validateGoImplementationCommand(cmd, mayorRigDir string, v orchestrator.WorkflowValidation, verifyOK bool) error {
	if !orchestrator.WorkflowUsesGo(v) {
		return nil
	}
	if writesGoModuleFilesViaHeredoc(cmd) {
		return fmt.Errorf("do not write go.mod or go.sum via heredoc — use go mod init, go get, and go mod tidy in project_setup or before bd close")
	}
	lower := strings.ToLower(cmd)
	if (strings.Contains(lower, "go run") || strings.Contains(lower, "curl ")) &&
		!orchestrator.GoServerMainExists(mayorRigDir, v) {
		return fmt.Errorf("go run/curl not until cmd/server/main.go exists — use: %s",
			orchestrator.GoImplementationVerifyCommand(v, mayorRigDir))
	}
	if isBeadCloseCommand(cmd) && !verifyOK {
		return fmt.Errorf("run green verify before bd close: %s",
			orchestrator.GoImplementationVerifyCommand(v, mayorRigDir))
	}
	return nil
}
