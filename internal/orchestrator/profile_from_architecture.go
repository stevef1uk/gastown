package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
)

// EnrichWorkflowValidationFromArchitecture aligns the rig profile with on-disk
// architecture.md (and optional SPEC.md) so project_setup/implementation use the
// stack and paths from the design artifacts — not a generic dual Go/Python prompt.
func EnrichWorkflowValidationFromArchitecture(v WorkflowValidation, mayorRigDir string) WorkflowValidation {
	archPath := filepath.Join(mayorRigDir, "architecture.md")
	v = AlignProfileLayoutWithArchitecture(v, archPath)

	if len(v.UnionRequiredFiles()) > 0 {
		return SanitizeRigFlowProfile(v)
	}

	data, err := os.ReadFile(archPath)
	if err != nil || len(data) == 0 {
		return SanitizeRigFlowProfile(v)
	}

	paths := extractArchPaths(string(data), v.LayoutRootDir())
	if len(paths) == 0 {
		return SanitizeRigFlowProfile(v)
	}

	v.RequiredFiles = paths
	if root := inferLayoutRootFromPaths(paths); root != "" && root != "." {
		v.LayoutRoot = root
	}
	v = inferTestRunnerFromPaths(v, paths)
	return SanitizeRigFlowProfile(v)
}

func inferLayoutRootFromPaths(paths []string) string {
	if len(paths) == 0 {
		return "."
	}
	first := filepath.ToSlash(strings.TrimSpace(paths[0]))
	if !strings.Contains(first, "/") {
		return "."
	}
	seg := strings.SplitN(first, "/", 2)[0]
	if seg == "" {
		return "."
	}
	for _, p := range paths[1:] {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if p == "" || !strings.HasPrefix(p, seg+"/") {
			return "."
		}
	}
	return seg
}

func inferTestRunnerFromPaths(v WorkflowValidation, paths []string) WorkflowValidation {
	if strings.TrimSpace(v.TestRunner) != "" {
		return v
	}
	for _, p := range paths {
		if strings.HasSuffix(strings.ToLower(p), ".py") {
			v.TestRunner = "pytest"
			return v
		}
		if strings.HasSuffix(strings.ToLower(p), ".go") {
			v.TestRunner = "go"
			return v
		}
	}
	return v
}

// ProjectSetupStackKind returns a short label for prompts (go, python, docker, generic).
func ProjectSetupStackKind(v WorkflowValidation) string {
	if WorkflowUsesGo(v) {
		return "go"
	}
	if WorkflowUsesPython(v) {
		return "python"
	}
	if WorkflowUsesDocker(v) {
		return "docker"
	}
	return "generic"
}

// ProjectSetupFailureHint returns the failure_hint text for project_setup for this profile.
func ProjectSetupFailureHint(v WorkflowValidation) string {
	switch ProjectSetupStackKind(v) {
	case "go":
		layout := v.LayoutRootDir()
		return "Go rig: run go mod init/get/tidy under " + layout +
			" only — never cat/heredoc/touch source files. Green verify: " +
			GoProjectSetupVerifyCommand(v, "") + ". JSON success only after verify passes."
	case "python":
		req := v.RequirementsFilePath()
		if req == "" {
			req = "requirements.txt"
		}
		return "Python rig only — no go mod. Create " + v.PythonVenvRelDir() +
			", pip install -r " + req + " once. Green verify: " +
			PythonProjectSetupVerifyCommand(v) + " (import pytest, not unittest). JSON success only after verify."
	case "docker":
		return "Docker rig: split beads for the active phase; confirm layout exists. Green verify: " +
			v.ProjectSetupVerifyHint() + ". No application source in project_setup."
	default:
		return "Run project_setup per workflow profile; green verify: " + v.ProjectSetupVerifyHint()
	}
}

// FormatProjectSetupStackBlock is injected via hooks.prompt_context so setup agents
// see one stack — derived from profile + architecture, not the full dual-stack prompt file.
func FormatProjectSetupStackBlock(v WorkflowValidation) string {
	kind := ProjectSetupStackKind(v)
	verify := v.ProjectSetupVerifyHint()
	req := v.RequirementsFilePath()
	if req == "" {
		req = "requirements.txt"
	}
	layout := v.LayoutRootDir()

	var b strings.Builder
	b.WriteString("## Active stack for this rig (from SPEC, architecture, and workflow profile)\n\n")
	b.WriteString("**Stack:** " + kind + "\n\n")
	b.WriteString("**Do not use the other language's toolchain.** ")
	switch kind {
	case "python":
		b.WriteString("This is a **Python** rig. Forbidden in project_setup: `go mod`, `go build`, `go test`, `python -m unittest` (no tests exist yet).\n\n")
		b.WriteString("**Required verify (run exactly):** `" + verify + "`\n\n")
		b.WriteString("**Layout root:** `" + layout + "/` — requirements file: `" + req + "`\n\n")
		b.WriteString("**Allowed:** `python3 -m venv " + v.PythonVenvRelDir() + "`, pip install -r " + req + ", `bd list`/`bd delete` for bead splits.\n")
	case "go":
		b.WriteString("This is a **Go** rig. Forbidden in project_setup: Python venv, pip, unittest, writing `.go` sources.\n\n")
		b.WriteString("**Required verify (run exactly):** `" + verify + "`\n\n")
		b.WriteString("**Layout root:** `" + layout + "/` — only `go mod init/get/tidy` under that directory.\n")
	case "docker":
		b.WriteString("This is a **Docker/custom** rig. Follow the Docker section of the prompt only.\n\n")
		b.WriteString("**Required verify:** `" + verify + "`\n")
	default:
		b.WriteString("Follow the profile verify hint only.\n\n")
		b.WriteString("**Verify:** `" + verify + "`\n")
	}
	return b.String()
}
