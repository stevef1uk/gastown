package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	specLayoutDirRe   = regexp.MustCompile(`(?m)^([a-zA-Z][a-zA-Z0-9_-]*)/\s*$`)
	specTreeFileInLine = regexp.MustCompile(`([a-zA-Z0-9_.-]+\.(?:py|go|txt|md|yaml|yml|json|toml))`)
)

// EnrichWorkflowValidationFromArchitecture aligns the rig profile with mayor/rig docs.
// SPEC.md is authoritative for layout_root and required_files when it lists project paths;
// architecture.md is used only when SPEC has no usable layout. Not platform-specific.
func EnrichWorkflowValidationFromArchitecture(v WorkflowValidation, mayorRigDir string) WorkflowValidation {
	archPath := filepath.Join(mayorRigDir, "architecture.md")
	if specPaths, ok := extractSpecLayoutPaths(mayorRigDir); ok {
		// Profile from spec-index already lists canonical nested paths; SPEC layout tree
		// parsing only captures leaf filenames (linkshelf/handlers.go not internal/api/handlers.go).
		if !shouldReplaceProfileRequiredFilesWithSpec(v, specPaths) {
			return SanitizeRigFlowProfile(v)
		}
		v = applySpecPathsToValidation(v, specPaths)
		return SanitizeRigFlowProfile(v)
	}

	// When SPEC.md layout tree can't be parsed, fall back to aligning the profile with
	// architecture.md to handle flat mayor/rig worktrees and spec-index prefix confusion.
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

// extractSpecLayoutPaths reads SPEC.md and returns repo-relative paths (e.g. pingapp/main.py).
func extractSpecLayoutPaths(mayorRigDir string) ([]string, bool) {
	specPath := filepath.Join(mayorRigDir, "SPEC.md")
	data, err := os.ReadFile(specPath)
	if err != nil || len(data) == 0 {
		return nil, false
	}
	text := string(data)

	var paths []string
	paths = append(paths, extractArchPaths(text, "")...)
	paths = append(paths, parseSpecLayoutTree(text)...)
	paths = dedupeStrings(paths)

	if len(paths) == 0 {
		return nil, false
	}
	// Prefer paths with a shared layout prefix (pingapp/...) over flat ./main.py-only lists.
	if root := inferLayoutRootFromPaths(paths); root != "" && root != "." {
		var prefixed []string
		for _, p := range paths {
			p = filepath.ToSlash(strings.TrimSpace(p))
			if strings.HasPrefix(p, root+"/") {
				prefixed = append(prefixed, p)
			}
		}
		if len(prefixed) > 0 {
			return prefixed, true
		}
	}
	// SPEC lists files at repo root (layout_root ".") — still authoritative.
	if len(paths) >= 1 {
		return paths, true
	}
	return nil, false
}

// parseSpecLayoutTree extracts paths from markdown tree blocks under "## Layout" in SPEC.md.
func parseSpecLayoutTree(specText string) []string {
	section := specLayoutSection(specText)
	dir := ""
	var out []string
	for _, line := range strings.Split(section, "\n") {
		line = strings.TrimSpace(line)
		line = strings.Trim(line, "`")
		if line == "```" || strings.HasPrefix(line, "```") {
			continue
		}
		if m := specLayoutDirRe.FindStringSubmatch(line); len(m) == 2 {
			dir = m[1]
			continue
		}
		if dir == "" {
			continue
		}
		if m := specTreeFileInLine.FindStringSubmatch(line); len(m) == 2 {
			out = append(out, dir+"/"+m[1])
		}
	}
	return out
}

func specLayoutSection(specText string) string {
	lower := strings.ToLower(specText)
	i := strings.Index(lower, "## layout")
	if i < 0 {
		return specText
	}
	section := specText[i:]
	if j := strings.Index(section[1:], "\n## "); j >= 0 {
		return section[:1+j]
	}
	return section
}

// shouldReplaceProfileRequiredFilesWithSpec returns false when the saved profile already
// requires nested layout paths but SPEC extraction produced flat basename-only paths.
func shouldReplaceProfileRequiredFilesWithSpec(v WorkflowValidation, specPaths []string) bool {
	if len(v.RequiredFiles) == 0 {
		return true
	}
	if !RequiresExactImplementPaths(v) {
		return true
	}
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	if layout != "" && layout != "." && profilePathsUseLayoutPrefix(v.RequiredFiles, layout) {
		prefixed := 0
		for _, p := range specPaths {
			p = filepath.ToSlash(strings.TrimSpace(p))
			if strings.HasPrefix(p, layout+"/") {
				prefixed++
			}
		}
		// SPEC layout tree parsing often yields flat cmd/internal/web paths without the
		// layout_root prefix; keep canonical profile paths from spec-index in that case.
		if prefixed < len(specPaths) {
			return false
		}
	}
	specV := WorkflowValidation{
		RequiredFiles: append([]string(nil), specPaths...),
		LayoutRoot:    inferLayoutRootFromPaths(specPaths),
	}
	return RequiresExactImplementPaths(specV)
}

func profilePathsUseLayoutPrefix(paths []string, layout string) bool {
	if layout == "" || layout == "." {
		return false
	}
	for _, f := range paths {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if f == "" || !strings.HasPrefix(f, layout+"/") {
			return false
		}
	}
	return len(paths) > 0
}

func applySpecPathsToValidation(v WorkflowValidation, specPaths []string) WorkflowValidation {
	v.RequiredFiles = append([]string(nil), specPaths...)
	if root := inferLayoutRootFromPaths(specPaths); root != "" {
		v.LayoutRoot = root
		if root != "." {
			v.BeadTitleContains = "Implement " + root + "/"
		}
	}
	v = inferTestRunnerFromPaths(v, specPaths)
	if v.QAVerifyCommand == "" || strings.Contains(v.QAVerifyCommand, "cd .") {
		v = inferQAVerifyFromSpecPaths(v, specPaths)
	}
	return v
}

func inferQAVerifyFromSpecPaths(v WorkflowValidation, paths []string) WorkflowValidation {
	root := v.LayoutRootDir()
	v = inferTestRunnerFromPaths(v, paths)
	if WorkflowUsesPython(v) {
		if root != "" && root != "." {
			v.QAVerifyCommand = "cd " + root + " && pytest"
		} else {
			v.QAVerifyCommand = "pytest"
		}
	}
	return v
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

// ProjectSetupStackKind returns a short label for prompts (go, python, nodejs, docker, generic).
func ProjectSetupStackKind(v WorkflowValidation) string {
	if WorkflowUsesGo(v) {
		return "go"
	}
	if WorkflowUsesNodeJS(v) {
		return "nodejs"
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
	case "nodejs":
		return "Node.js rig only — no go mod or python venv. Run " + NodeProjectSetupVerifyCommand(v) +
			". JSON success only after verify."
	case "docker":
		return "Docker rig: split beads for the active phase; confirm layout exists. Green verify: " +
			v.ProjectSetupVerifyHint() + ". No application source in project_setup."
	default:
		return "Run project_setup per workflow profile; green verify: " + v.ProjectSetupVerifyHint()
	}
}

// FormatProjectSetupStackBlock is injected via hooks.prompt_context so setup agents
// see one stack — derived from profile + SPEC, not the full dual-stack prompt file.
func FormatProjectSetupStackBlock(v WorkflowValidation) string {
	scoped := v.ForActivePhase()
	kind := ProjectSetupStackKind(scoped)
	verify := scoped.ProjectSetupVerifyHint()
	req := scoped.RequirementsFilePath()
	if req == "" {
		req = "requirements.txt"
	}
	layout := scoped.LayoutRootDir()

	var b strings.Builder
	b.WriteString("## Active stack for this rig (from SPEC.md, architecture, and workflow profile)\n\n")
	b.WriteString("**Stack:** " + kind + "\n\n")
	b.WriteString("**Do not use the other language's toolchain.** ")
	switch kind {
	case "python":
		b.WriteString("This is a **Python** rig. Forbidden in project_setup: `go mod`, `go build`, `go test`, `python -m unittest` (no tests exist yet).\n\n")
		b.WriteString("**Required verify (run exactly):** `" + verify + "`\n\n")
		b.WriteString("**Layout root:** `" + layout + "/` — requirements file: `" + req + "`\n\n")
		b.WriteString("**Allowed:** `python3 -m venv " + scoped.PythonVenvRelDir() + "`, pip install -r " + req + ", `bd list`/`bd delete` for bead splits.\n")
	case "go":
		b.WriteString("This is a **Go** rig. Forbidden in project_setup: Python venv, pip, unittest, writing `.go` sources.\n\n")
		b.WriteString("**Required verify (run exactly):** `" + verify + "`\n\n")
		b.WriteString("**Layout root:** `" + layout + "/` — only `go mod init/get/tidy` under that directory.\n")
	case "nodejs":
		b.WriteString("This is a **Node.js** rig. Forbidden in project_setup: `go mod`, `python3 -m venv`, `pip install`, writing app source files.\n\n")
		b.WriteString("**Required verify (run exactly):** `" + verify + "`\n\n")
		b.WriteString("**Layout root:** `" + layout + "/` — only `npm install`/`yarn install`/`pnpm install` under the Node directory.\n")
	case "docker":
		b.WriteString("This is a **Docker/custom** rig. Follow the Docker section of the prompt only.\n\n")
		b.WriteString("**Required verify:** `" + verify + "`\n")
	default:
		b.WriteString("Follow the profile verify hint only.\n\n")
		b.WriteString("**Verify:** `" + verify + "`\n")
	}
	return b.String()
}

// ReconcileProfileWithArchitecture merges architecture.md backtick paths into
// the workflow profile, ensuring files the architect specifies but spec-index
// missed are added to required_files and the final delivery phase.
func ReconcileProfileWithArchitecture(townRoot, rig string) error {
	if rig == "" || townRoot == "" {
		return nil
	}
	path := filepath.Join(townRoot, rig, "mayor", "rig", rigProfileDir, rigProfileFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil // no profile yet
	}
	var env rigProfileEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil
	}
	archPath := filepath.Join(townRoot, rig, "mayor", "rig", "architecture.md")
	archData, err := os.ReadFile(archPath)
	if err != nil || len(archData) == 0 {
		return nil
	}
	archPaths := extractArchPaths(string(archData), env.Validation.LayoutRootDir())
	if len(archPaths) == 0 {
		return nil
	}
	var filePaths []string
	for _, p := range archPaths {
		p = filepath.ToSlash(strings.TrimSpace(p))
		for strings.HasPrefix(p, "./") {
			p = p[2:]
		}
		if isImplementableFilePath(p) {
			filePaths = append(filePaths, p)
		}
	}
	if len(filePaths) == 0 {
		return nil
	}
	changed := false
	existing := map[string]bool{}
	for _, f := range env.Validation.RequiredFiles {
		existing[filepath.ToSlash(strings.TrimSpace(f))] = true
	}
	for _, p := range filePaths {
		if !existing[p] {
			env.Validation.RequiredFiles = append(env.Validation.RequiredFiles, p)
			existing[p] = true
			changed = true
		}
	}
	if changed && len(env.Validation.DeliveryPhases) > 0 {
		// Place each new file in the phase whose existing files share the longest
		// directory prefix (scripts/config/deployment go to the final phase).
		for _, p := range filePaths {
			bestIdx := -1
			bestLen := 0
			for i, phase := range env.Validation.DeliveryPhases {
				for _, f := range phase.RequiredFiles {
					prefix := longestCommonPathPrefix(p, f)
					if prefix != "" && len(prefix) > bestLen {
						bestLen = len(prefix)
						bestIdx = i
					}
				}
			}
			if bestIdx < 0 {
				bestIdx = len(env.Validation.DeliveryPhases) - 1
			}
			phaseExisting := map[string]bool{}
			for _, f := range env.Validation.DeliveryPhases[bestIdx].RequiredFiles {
				phaseExisting[filepath.ToSlash(strings.TrimSpace(f))] = true
			}
			if !phaseExisting[p] {
				env.Validation.DeliveryPhases[bestIdx].RequiredFiles = append(
					env.Validation.DeliveryPhases[bestIdx].RequiredFiles, p)
			}
		}
	}
	if !changed {
		return nil
	}
	return SaveRigWorkflowProfileEnvelope(townRoot, rig, env)
}

// longestCommonPathPrefix returns the longest directory prefix shared by a and b,
// or "" if there is none. The prefix always ends at a path separator.
func longestCommonPathPrefix(a, b string) string {
	a = filepath.ToSlash(strings.TrimSpace(a))
	b = filepath.ToSlash(strings.TrimSpace(b))
	min := len(a)
	if len(b) < min {
		min = len(b)
	}
	var i int
	for i = 0; i < min && a[i] == b[i]; i++ {
	}
	if i == 0 {
		return ""
	}
	// Walk back to the last separator so the prefix is a directory.
	for i > 0 && a[i-1] != '/' {
		i--
	}
	return strings.TrimRight(a[:i], "/")
}

// isImplementableFilePath reports whether p looks like a source/config file that
// should have an implement bead, as opposed to API endpoints, URLs, Docker
// images, or directory references.
func isImplementableFilePath(p string) bool {
	p = filepath.ToSlash(strings.TrimSpace(p))
	for strings.HasPrefix(p, "./") {
		p = p[2:]
	}
	if p == "" || p == "." || p == "/" {
		return false
	}
	if strings.Contains(p, "://") || strings.Contains(p, "{") || strings.Contains(p, "}") || strings.Contains(p, ":") {
		return false
	}
	if strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/") {
		return false
	}
	if strings.HasSuffix(p, "/") {
		return false
	}
	base := filepath.Base(p)
	known := map[string]bool{
		"dockerfile": true, "docker-compose.yml": true, "docker-compose.yaml": true,
		".dockerignore": true, ".env": true, ".env.example": true, ".gitignore": true,
	}
	if known[strings.ToLower(base)] {
		return true
	}
	return strings.Contains(base, ".")
}
