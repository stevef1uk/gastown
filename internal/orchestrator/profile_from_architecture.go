package orchestrator

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	specLayoutDirRe   = regexp.MustCompile(`(?m)^([a-zA-Z][a-zA-Z0-9_-]*)/\s*$`)
	specTreeFileInLine = regexp.MustCompile(`([a-zA-Z0-9_.-]+\.(?:py|go|txt|md|yaml|yml|json|toml|mod|js|ts|jsx|tsx|css|html|sum))`)
	profileArchDebug bool
)

func init() {
	for _, v := range strings.Split(os.Getenv("GT_DEBUG"), ",") {
		if strings.TrimSpace(v) == "profile_arch" {
			profileArchDebug = true
			break
		}
	}
}

func archDebug(format string, args ...interface{}) {
	if profileArchDebug {
		fmt.Fprintf(os.Stderr, "[profile_arch] "+format+"\n", args...)
	}
}

// EnrichWorkflowValidationFromArchitecture aligns the rig profile with mayor/rig docs.
// SPEC.md is authoritative for layout_root and required_files when it lists project paths;
// architecture.md is used only when SPEC has no usable layout. Not platform-specific.
func EnrichWorkflowValidationFromArchitecture(v WorkflowValidation, mayorRigDir string) WorkflowValidation {
	archPath := filepath.Join(mayorRigDir, "architecture.md")
	archDebug("EnrichWorkflowValidationFromArchitecture: mayorRigDir=%s, current RequiredFiles=%v", mayorRigDir, v.RequiredFiles)
	if specPaths, ok := extractSpecLayoutPaths(mayorRigDir); ok {
		archDebug("SPEC paths found: %v", specPaths)
		// Profile from spec-index already lists canonical nested paths; SPEC layout tree
		// parsing only captures leaf filenames (linkshelf/handlers.go not internal/api/handlers.go).
		if !shouldReplaceProfileRequiredFilesWithSpec(v, specPaths) {
			archDebug("shouldReplaceProfileRequiredFilesWithSpec=false — keeping profile RequiredFiles=%v", v.RequiredFiles)
			return SanitizeRigFlowProfile(v)
		}
		v = applySpecPathsToValidation(v, specPaths)
		archDebug("applied SPEC paths: RequiredFiles=%v, LayoutRoot=%s", v.RequiredFiles, v.LayoutRoot)
		return SanitizeRigFlowProfile(v)
	}
	archDebug("no SPEC paths — falling back to architecture.md")

	// When SPEC.md layout tree can't be parsed, fall back to aligning the profile with
	// architecture.md to handle flat mayor/rig worktrees and spec-index prefix confusion.
	archDebug("AlignProfileLayoutWithArchitecture before: RequiredFiles=%v", v.RequiredFiles)
	v = AlignProfileLayoutWithArchitecture(v, archPath)
	archDebug("AlignProfileLayoutWithArchitecture after: RequiredFiles=%v", v.RequiredFiles)

	if len(v.UnionRequiredFiles()) > 0 {
		archDebug("UnionRequiredFiles non-empty after alignment — keeping: %v", v.UnionRequiredFiles())
		return SanitizeRigFlowProfile(v)
	}

	data, err := os.ReadFile(archPath)
	if err != nil || len(data) == 0 {
		archDebug("cannot read architecture.md: %v", err)
		return SanitizeRigFlowProfile(v)
	}

	paths := extractArchPaths(string(data), v.LayoutRootDir())
	archDebug("extractArchPaths from architecture.md: %v (layoutRoot=%s)", paths, v.LayoutRootDir())
	if len(paths) == 0 {
		archDebug("no paths found in architecture.md")
		return SanitizeRigFlowProfile(v)
	}

	v.RequiredFiles = paths
	archDebug("set RequiredFiles from architecture.md: %v", paths)
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
		archDebug("no SPEC.md at %s: %v", specPath, err)
		return nil, false
	}
	text := string(data)

	treePaths := parseSpecLayoutTree(text)
	archDebug("parseSpecLayoutTree from SPEC: %v", treePaths)

	// The layout tree is the authoritative source of required files.
	// Prose backtick refs are documentation only — they often mention files
	// negatively ("no package.json") or with bare paths that contradict the tree.
	// If the tree exists, it defines the complete required set.
	var paths []string
	if len(treePaths) > 0 {
		log.Printf("[extractSpecLayoutPaths] using SPEC layout tree: %d files", len(treePaths))
		paths = treePaths
	} else {
		// Fallback: no parseable tree — use prose backtick refs as last resort.
		log.Printf("[extractSpecLayoutPaths] no layout tree found; falling back to prose backtick extraction")
		archPaths := extractArchPaths(text, "")
		archDebug("extractArchPaths from SPEC (fallback): %v", archPaths)
		paths = archPaths
	}
	paths = dedupeStrings(paths)
	archDebug("extractSpecLayoutPaths result: %v", paths)

	if len(paths) == 0 {
		archDebug("no paths found in SPEC.md")
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
// Directory nesting is tracked from tree connectors/indentation, so a file under
// `handler/` yields `helloapi/handler/hello.go` (not a flat `helloapi/hello.go`).
// Works with or without code fences (```) for backward compatibility.
func parseSpecLayoutTree(specText string) []string {
	section := specLayoutSection(specText)
	dirs := []string{}
	var out []string
	inCodeFence := false
	hasCodeFence := strings.Contains(section, "```")
	for _, line := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(line)
		// Toggle code fence state
		if strings.HasPrefix(trimmed, "```") {
			inCodeFence = !inCodeFence
			continue
		}
		// If section has code fences, only parse inside them.
		// If no code fences, parse the whole section.
		if hasCodeFence && !inCodeFence {
			continue
		}
		depth, entry := treeLineDepthEntry(line)
		entry = strings.Trim(entry, "`")
		if entry == "" {
			continue
		}
		if strings.HasSuffix(entry, "/") {
			name := strings.TrimRight(entry, "/")
			if !validTreeDirName(name) {
				continue
			}
			if depth < len(dirs) {
				dirs = dirs[:depth]
			}
			dirs = append(dirs, name)
			continue
		}
		// Indented flat list format (e.g. "pingapp/  requirements.txt" without
		// connectors): treeLineDepthEntry returns depth 0, but we have dirs →
		// treat as flat child at depth 1.
		if depth < 1 && len(dirs) > 0 {
			depth = 1
		}
		if depth < 1 || len(dirs) < depth {
			continue
		}
		if m := specTreeFileInLine.FindStringSubmatch(entry); len(m) == 2 {
			fileName := m[1]
			// Truncate dirs to current depth for ALL file entries
			if depth < len(dirs) {
				dirs = dirs[:depth]
			}
			// Infer additional directories from file paths containing "/" (e.g. "web/index.html" -> "web/")
			inferredDirs := ""
			if idx := strings.LastIndex(entry, "/"); idx >= 0 {
				pathDirs := strings.Split(strings.TrimSuffix(entry[:idx], "/"), "/")
				for _, d := range pathDirs {
					if d != "" && validTreeDirName(d) {
						dirs = append(dirs, d)
					}
				}
				inferredDirs = strings.Join(pathDirs, "/")
			}
			// Build path: explicit dirs up to depth + inferred dirs from file path + filename
			basePath := strings.Join(dirs[:depth], "/")
			if inferredDirs != "" {
				basePath += "/" + inferredDirs
			}
			out = append(out, basePath+"/"+fileName)
		}
	}
	return out
}

// treeLineDepthEntry reports the nesting depth and entry text of a markdown tree
// line.  The depth is determined by the column position of the first `├` or `└`
// connector character (each tree level is 4 chars wide).  Returns depth 0 and
// the trimmed entry for bare lines (e.g. the root directory or continuation bars).
func treeLineDepthEntry(line string) (int, string) {
	// Find the column position of the tree connector (├ or └) using rune counting.
	connIdx := -1
	runes := []rune(line)
	for i, r := range runes {
		if r == '├' || r == '└' {
			connIdx = i
			break
		}
	}

	if connIdx < 0 {
		// No connector — root directory, continuation bar, or prose line.
		return 0, strings.TrimSpace(line)
	}

	// Column-based depth: each tree level is 4 chars wide.
	depth := connIdx/4 + 1

	// Extract entry name after the connector (skip "── " or "──").
	rest := string(runes[connIdx:])
	if strings.HasPrefix(rest, "├── ") || strings.HasPrefix(rest, "└── ") {
		rest = strings.TrimPrefix(rest, "├── ")
		rest = strings.TrimPrefix(rest, "└── ")
	} else if strings.HasPrefix(rest, "├──") || strings.HasPrefix(rest, "└──") {
		rest = strings.TrimPrefix(rest, "├──")
		rest = strings.TrimPrefix(rest, "└──")
	}
	return depth, strings.TrimSpace(rest)
}

// validTreeDirName reports whether a directory entry in a SPEC layout tree is a
// usable layout component (rejects paths, whitespace, and `.`/`..`).
func validTreeDirName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	return !strings.ContainsAny(name, " \t/\\")
}

func specLayoutSection(specText string) string {
	lower := strings.ToLower(specText)

	// Find both "## file layout" and "## layout" sections
	fileLayoutIdx := strings.Index(lower, "## file layout")
	layoutIdx := strings.Index(lower, "## layout")

	// Extract both candidate sections and prefer the one with a code fence
	var fileLayoutSection, layoutSection string
	if fileLayoutIdx >= 0 {
		fileLayoutSection = specText[fileLayoutIdx:]
		if j := strings.Index(fileLayoutSection[1:], "\n## "); j >= 0 {
			fileLayoutSection = fileLayoutSection[:1+j]
		}
	}
	if layoutIdx >= 0 {
		layoutSection = specText[layoutIdx:]
		if j := strings.Index(layoutSection[1:], "\n## "); j >= 0 {
			layoutSection = layoutSection[:1+j]
		}
	}

	// Prefer section containing a code fence (```) — that's where the tree lives
	switch {
	case fileLayoutSection != "" && strings.Contains(fileLayoutSection, "```"):
		return fileLayoutSection
	case layoutSection != "" && strings.Contains(layoutSection, "```"):
		return layoutSection
	case fileLayoutSection != "":
		return fileLayoutSection
	case layoutSection != "":
		return layoutSection
	}
	return specText
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
	// If profile has flat layout (LayoutRoot "." or empty) but SPEC has a valid layout root,
	// always replace to get the correct nested paths from SPEC.
	specLayout := strings.Trim(filepath.ToSlash(strings.TrimSpace(inferLayoutRootFromPaths(specPaths))), "/")
	if (layout == "" || layout == ".") && specLayout != "" && specLayout != "." {
		return true
	}
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
		// Even if all specPaths have the layout prefix, they may be flatter than the
		// profile's nested paths (e.g. linkshelf/handlers.go vs linkshelf/internal/api/handlers.go).
		// If any profile path has a directory prefix that the corresponding spec path lacks,
		// keep the more specific profile paths.
		if profileIsMoreSpecific(v.RequiredFiles, specPaths, layout) {
			return false
		}
	}
	specV := WorkflowValidation{
		RequiredFiles: append([]string(nil), specPaths...),
		LayoutRoot:    inferLayoutRootFromPaths(specPaths),
	}
	return RequiresExactImplementPaths(specV)
}

// profileIsMoreSpecific returns true if the profile has paths with deeper nesting
// than the corresponding SPEC paths for the same basenames, indicating the
// profile's paths are more specific and should not be flattened.
func profileIsMoreSpecific(profilePaths, specPaths []string, layout string) bool {
	// Build basename -> path depth map for profile and spec
	profileDepth := map[string]int{}
	for _, p := range profilePaths {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if strings.HasPrefix(p, layout+"/") {
			rel := strings.TrimPrefix(p, layout+"/")
			depth := strings.Count(rel, "/")
			profileDepth[filepath.Base(p)] = depth
		}
	}
	specDepth := map[string]int{}
	for _, p := range specPaths {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if strings.HasPrefix(p, layout+"/") {
			rel := strings.TrimPrefix(p, layout+"/")
			depth := strings.Count(rel, "/")
			specDepth[filepath.Base(p)] = depth
		}
	}
	// If any basename has deeper profile path, profile is more specific
	for base, pDepth := range profileDepth {
		if sDepth, ok := specDepth[base]; ok && pDepth > sDepth {
			return true
		}
	}
	return false
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
	// Filter out design documents (architecture.md, design.md, plan.md) for layout inference
	// as they don't have the layout prefix and would confuse the root inference.
	filteredPaths := make([]string, 0, len(specPaths))
	for _, p := range specPaths {
		base := filepath.Base(p)
		if base != "architecture.md" && base != "design.md" && base != "plan.md" {
			filteredPaths = append(filteredPaths, p)
		}
	}
	if root := inferLayoutRootFromPaths(filteredPaths); root != "" {
		if root != "." {
			v.LayoutRoot = root
			v.BeadTitleContains = "Implement " + root + "/"
		}
		// When spec-inferred root is "." (e.g. spec paths mix file paths with
		// Go import paths like net/http, encoding/json), keep the existing
		// LayoutRoot from the profile rather than resetting it to ".".
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
		b.WriteString("**Layout root:** `" + layout + "/` — only `npm install --ignore-scripts`/`yarn install --ignore-scripts`/`pnpm install --ignore-scripts` under the Node directory. Always disable lifecycle scripts to block supply-chain worms (Shai-Hulud).\n")
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

// SyncRigWorkflowProfileFromArchitecture re-derives the workflow profile from SPEC.md
// and architecture.md after design succeeds, replacing the LLM-guessed required_files
// (which can hallucinate files like config.py or emit wildcards like @app.get/post/...)
// with the authoritative file set. Runs on design success so planning creates beads
// only for real implement paths. Returns true when the profile was rewritten.
func SyncRigWorkflowProfileFromArchitecture(townRoot, rig string) (bool, error) {
	if rig == "" || townRoot == "" {
		return false, nil
	}
	path := filepath.Join(townRoot, rig, "mayor", "rig", rigProfileDir, rigProfileFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return false, nil // no profile yet
	}
	var env rigProfileEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return false, nil
	}
	mayorRig := filepath.Join(townRoot, rig, "mayor", "rig")
	archData, _ := os.ReadFile(filepath.Join(mayorRig, "architecture.md"))
	if len(archData) == 0 {
		return false, nil // design not complete — nothing authoritative to sync from
	}
	archPaths := extractArchPaths(string(archData), env.Validation.LayoutRootDir())
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
	// Merge SPEC and architecture paths: the union wins, and when the same basename
	// resolves to different paths (e.g. SPEC's prose says handler/hello.go while the
	// architect designed helloapi/handler/hello.go), architecture.md is authoritative
	// because it reflects the concrete design decisions planning must implement.
	var authoritative []string
	if specPaths, ok := extractSpecLayoutPaths(mayorRig); ok {
		archDebug("sync: SPEC layout paths=%v arch paths=%v", specPaths, filePaths)
		authoritative = mergeArchWinsOnConflict(specPaths, filePaths)
	} else {
		archDebug("sync: no SPEC paths, falling back to architecture paths=%v", filePaths)
		authoritative = filePaths
	}
	authoritative = dedupeStrings(authoritative)
	if len(authoritative) == 0 {
		return false, nil
	}
	// Reject hallucinated entries (wildcards, route stubs, non-file tokens).
	authoritative = filterValidImplementPaths(authoritative)
	if len(authoritative) == 0 {
		return false, nil
	}
	// Judge/architect hallucination: paths prefixed with the literal placeholder
	// "layout_root/" (the JSON key echoed as a directory name). No real project
	// dir is ever called "layout_root" — remap them onto the profile's real layout
	// root (e.g. pingapp) so agents keep writing where the scaffold put files.
	authoritative = remapLayoutRootPlaceholderPaths(authoritative, env.Validation.LayoutRoot)
	env.Validation.RequiredFiles = append([]string(nil), authoritative...)
	if root := inferLayoutRootFromPaths(authoritative); root != "" && root != "." {
		env.Validation.LayoutRoot = root
		env.Validation.BeadTitleContains = "Implement " + root + "/"
	}
	env.Validation = inferTestRunnerFromPaths(env.Validation, authoritative)
	// Rebuild delivery phases so the active phase required_files (used by
	// ForActivePhase at planning time) match the authoritative set instead of the
	// hallucinated list the LLM emitted.
	env.Validation = rebuildDeliveryPhasesFromAuthoritative(env.Validation, authoritative)
	// Rebuilt/re-distributed phases may have empty or wrong-stack verify commands;
	// fill them with stack-appropriate defaults (never go vet in a Python phase).
	env.Validation = SanitizePhaseVerifyCommandsForStack(env.Validation)
	// SPEC/architecture are authoritative for the runtime smoke too. When they
	// document an HTTP server + probes, ensure the profile has a smoke phase whose
	// qa_verify_command starts the server and curls the documented routes — the
	// first spec-index run (before architecture.md exists) can emit a pytest-only
	// profile that QA later rejects ("requires a successful runtime smoke CMD").
	env.Validation = ensureRuntimeSmokePhaseFromSpec(townRoot, rig, env.Validation)
	archDebug("SyncRigWorkflowProfileFromArchitecture: required=%v layout=%q", env.Validation.RequiredFiles, env.Validation.LayoutRoot)
	if err := SaveRigWorkflowProfileEnvelope(townRoot, rig, env); err != nil {
		return false, err
	}
	return true, nil
}

// remapLayoutRootPlaceholderPaths rewrites the literal "layout_root/" placeholder
// prefix (a judge/architect echo of the JSON key) onto the profile's real layout
// root, keeping the project rooted in the same directory the scaffold wrote to.
// If no real layout root is set, paths collapse to project root.
func remapLayoutRootPlaceholderPaths(paths []string, layoutRoot string) []string {
	changed := false
	target := strings.Trim(filepath.ToSlash(strings.TrimSpace(layoutRoot)), "/")
	if target == "." {
		target = ""
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if strings.HasPrefix(p, "layout_root/") {
			p = strings.TrimPrefix(p, "layout_root/")
			changed = true
		}
		if target != "" {
			p = target + "/" + p
		}
		if p != "" {
			out = append(out, p)
		}
	}
	if !changed {
		return paths
	}
	return out
}

// hasRuntimeSmokeCommand reports whether a QA verify command already starts a
// server and curls it (uvicorn/gunicorn/flask/go run + curl + loopback host).
func hasRuntimeSmokeCommand(cmd string) bool {
	lower := strings.ToLower(strings.TrimSpace(cmd))
	if lower == "" {
		return false
	}
	hasServer := strings.Contains(lower, "uvicorn") || strings.Contains(lower, "gunicorn") ||
		strings.Contains(lower, "flask run") || strings.Contains(lower, "go run") ||
		strings.Contains(lower, "hypercorn")
	if !hasServer {
		return false
	}
	hasCurl := strings.Contains(lower, "curl ") || strings.Contains(lower, "curl\t") || strings.Contains(lower, ".gt-smoke.pid")
	hasLocal := strings.Contains(lower, "localhost") || strings.Contains(lower, "127.0.0.1")
	return hasCurl && hasLocal
}

// deriveSmokeQACommand builds a readable qa_verify_command from the SPEC/architecture
// smoke spec: start the documented server, curl each documented route, kill by port.
// Matches the shape spec-index emits for smoke-test phases.
func deriveSmokeQACommand(v WorkflowValidation, spec APISmokeSpec) string {
	serverStart := strings.TrimSpace(spec.ServerStart)
	if serverStart == "" {
		return ""
	}
	port := spec.Port
	if port == 0 {
		port = 8080
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	var curls []string
	for _, probe := range spec.orderedSmokeProbes() {
		path := normalizeSmokePath(probe.Path)
		if path == "" {
			continue
		}
		method := strings.ToUpper(strings.TrimSpace(probe.Method))
		if method == "POST" {
			body := strings.ReplaceAll(strings.TrimSpace(probe.Body), "'", `'\''`)
			curls = append(curls, fmt.Sprintf(`curl -sf -X POST -d '%s' %s%s`, body, base, path))
		} else {
			curls = append(curls, fmt.Sprintf("curl -sf %s%s", base, path))
		}
	}
	if len(curls) == 0 {
		return ""
	}
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	serverCmd := serverStart
	if layout != "" && layout != "." && !strings.Contains(serverCmd, "cd ") {
		serverCmd = "cd " + layout + " && " + serverCmd
	}
	return serverCmd + " & sleep 2 && " + strings.Join(curls, " && ") +
		fmt.Sprintf(" && kill $(lsof -ti:%d)", port)
}

// ensureRuntimeSmokePhaseFromSpec appends a smoke-test delivery phase when SPEC/
// architecture document an HTTP server with probes but no existing phase command
// performs a runtime smoke. Runs only inside the design-success sync so the QA
// step is given a command that can satisfy the runtime-smoke validator.
func ensureRuntimeSmokePhaseFromSpec(townRoot, rig string, v WorkflowValidation) WorkflowValidation {
	if townRoot == "" || rig == "" {
		return v
	}
	if !WorkflowNeedsQARuntimeSmoke(townRoot, rig, v) {
		return v
	}
	for i := range v.DeliveryPhases {
		if hasRuntimeSmokeCommand(v.DeliveryPhases[i].QAVerifyCommand) {
			return v
		}
	}
	if hasRuntimeSmokeCommand(v.QAVerifyCommand) {
		return v
	}
	spec, err := LoadAPISmokeSpecFromRig(townRoot, rig, v)
	if err != nil {
		return v
	}
	cmd := deriveSmokeQACommand(v, spec)
	if cmd == "" {
		return v
	}
	var files []string
	for _, f := range v.RequiredFiles {
		f = filepath.ToSlash(strings.TrimSpace(f))
		base := strings.ToLower(filepath.Base(f))
		if base == "main.py" || base == "app.py" || base == "server.py" ||
			strings.HasSuffix(base, "_main.go") || base == "main.go" {
			files = append(files, f)
			break
		}
	}
	phase := DeliveryPhase{
		ID:              "smoke-test",
		Title:           "Smoke Test with Running Server",
		RequiredFiles:   files,
		QAVerifyCommand: cmd,
		SpecFocus:       "Verify HTTP server runs and responds correctly",
	}
	if len(v.DeliveryPhases) > 0 {
		prev := v.DeliveryPhases[len(v.DeliveryPhases)-1].ID
		if prev != "" {
			phase.DependsOn = []string{prev}
		}
	}
	v.DeliveryPhases = append(v.DeliveryPhases, phase)
	if v.ActivePhaseIDField == "" && len(v.DeliveryPhases) > 0 {
		v.ActivePhaseIDField = v.DeliveryPhases[0].ID
	}
	return v
}

// filterValidImplementPaths drops entries that are not concrete file paths:
// wildcards (*), FastAPI/route stubs (@app.get/post/...), URLs, or junk tokens.
func filterValidImplementPaths(paths []string) []string {
	var out []string
	for _, p := range paths {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if p == "" || !isImplementableFilePath(p) {
			continue
		}
		if strings.ContainsAny(p, "*?") || strings.Contains(p, "...") ||
			strings.Contains(p, "://") || strings.ContainsAny(p, "{}") {
			continue
		}
		if strings.HasPrefix(p, "@") || strings.HasPrefix(p, "/api/") {
			continue
		}
		if !IsValidImplementBeadPath(p) {
			continue
		}
		out = append(out, p)
	}
	return dedupeStrings(out)
}

// rebuildDeliveryPhasesFromAuthoritative reconstructs delivery_phases so each phase's
// required_files is a filtered slice of the authoritative set, preserving the phase
// order and QA verify commands already on disk. Phases whose files all belong to a
// shared layout root keep their structure; hallucinated entries are dropped.
func rebuildDeliveryPhasesFromAuthoritative(v WorkflowValidation, authoritative []string) WorkflowValidation {
	if len(v.DeliveryPhases) == 0 {
		return v
	}
	// A degenerate phase structure (one phase per file, or any empty phase) is a
	// spec-index artifact that QA can never pass — e.g. a flat pingapp split into
	// requirementstxt/mainpy/test-mainpy. Rebuild from the authoritative file set
	// instead of re-distributing into the broken skeleton.
	if degenerateDeliveryPhaseStructure(v, authoritative) {
		v.DeliveryPhases = PhasesFromFilePaths(authoritative)
		return v
	}
	// Map authoritative entries to the phase whose existing files share the longest
	// directory prefix (mirrors ReconcileProfileWithArchitecture's placement).
	// Capture each phase's original basenames first — after the keep-loop a phase
	// whose files were renamed goes empty, so basename provenance is lost.
	origBase := make([]map[string]bool, len(v.DeliveryPhases))
	for i := range v.DeliveryPhases {
		origBase[i] = map[string]bool{}
		for _, f := range v.DeliveryPhases[i].RequiredFiles {
			origBase[i][filepath.Base(f)] = true
		}
	}
	for i := range v.DeliveryPhases {
		var keep []string
		for _, p := range v.DeliveryPhases[i].RequiredFiles {
			p = filepath.ToSlash(strings.TrimSpace(p))
			if containsString(authoritative, p) {
				keep = append(keep, p)
			}
		}
		v.DeliveryPhases[i].RequiredFiles = keep
	}
	// Distribute authoritative files not placed by exact match. Prefer basename
	// provenance: a file whose basename a phase originally declared (e.g. core
	// had main.go) returns to that phase even after the restructure moved it to
	// cmd/server/main.go. This keeps the SPEC's semantic split (go-module/core/
	// web/integration-test) instead of dumping everything into phase 0.
	for _, p := range authoritative {
		placed := false
		for i := range v.DeliveryPhases {
			if containsString(v.DeliveryPhases[i].RequiredFiles, p) {
				placed = true
				break
			}
		}
		if placed {
			continue
		}
		base := filepath.Base(p)
		bestIdx := -1
		// First pass: a phase that originally declared this basename.
		for i := range v.DeliveryPhases {
			if origBase[i][base] {
				bestIdx = i
				break
			}
		}
		// Second pass: a phase currently holding a same-basename file.
		if bestIdx < 0 {
			for i := range v.DeliveryPhases {
				for _, f := range v.DeliveryPhases[i].RequiredFiles {
					if filepath.Base(f) == base {
						bestIdx = i
						break
					}
				}
				if bestIdx >= 0 {
					break
				}
			}
		}
		if bestIdx >= 0 {
			v.DeliveryPhases[bestIdx].RequiredFiles = append(v.DeliveryPhases[bestIdx].RequiredFiles, p)
			continue
		}
		// Same-directory fallback: place the file with the phase that already
		// holds the most files in the same directory (root-level config/Docker
		// files cluster with the integration-test phase, cmd/server files with
		// the core phase). Longest-prefix alone can't distinguish them because
		// every file shares the layout_root prefix, so ties collapse to phase 0.
		bestIdx, bestScore := -1, -1
		dir := filepath.Dir(p)
		for i, phase := range v.DeliveryPhases {
			score := 0
			for _, f := range phase.RequiredFiles {
				if filepath.Dir(f) == dir {
					score++
				}
			}
			if score > bestScore {
				bestScore = score
				bestIdx = i
			}
		}
		if bestIdx < 0 {
			bestIdx = len(v.DeliveryPhases) - 1
		}
		v.DeliveryPhases[bestIdx].RequiredFiles = append(v.DeliveryPhases[bestIdx].RequiredFiles, p)
	}
	// If any phase was emptied by a layout restructure (flat main.go →
	// cmd/server/main.go, index.html → web/index.html) with no basename/prefix
	// match to re-attach its files, rebuild the whole skeleton from the
	// authoritative paths. Redistributing from a hollow skeleton by longest-prefix
	// alone dumps every remaining file into phase 0 (they all share the
	// layout_root prefix and ties resolve to the first phase).
	for i := range v.DeliveryPhases {
		if len(v.DeliveryPhases[i].RequiredFiles) == 0 {
			archDebug("rebuild: phase %q emptied by layout restructure; rebuilding phases from authoritative", v.DeliveryPhases[i].ID)
			v.DeliveryPhases = PhasesFromFilePaths(authoritative)
			// Active phase must resolve after a full rebuild, or planning hangs.
			if v.ActivePhaseID() == "" || !phaseIDExists(v, v.ActivePhaseID()) {
				v.ActivePhaseIDField = v.DeliveryPhases[0].ID
			}
			return v
		}
	}
	return v
}

// phaseIDExists reports whether the profile defines a delivery phase with the
// given ID. Used to keep active_phase_id resolvable after a full rebuild.
func phaseIDExists(v WorkflowValidation, id string) bool {
	for _, p := range v.DeliveryPhases {
		if strings.TrimSpace(p.ID) == strings.TrimSpace(id) {
			return true
		}
	}
	return false
}

// degenerateDeliveryPhaseStructure reports whether the profile's phase skeleton
// is a spec-index artifact rather than a deliberate split: any phase with no
// required files, or as many phases as files (one file per phase).
func degenerateDeliveryPhaseStructure(v WorkflowValidation, authoritative []string) bool {
	for i := range v.DeliveryPhases {
		if len(v.DeliveryPhases[i].RequiredFiles) == 0 {
			return true
		}
	}
	if len(authoritative) > 0 && len(v.DeliveryPhases) >= len(authoritative) {
		return true
	}
	return false
}

// ValidateRigWorkflowProfileForQA reports profile defects that would break planning
// or implementation: hallucinated required_files entries (wildcards, route stubs like
// @app.get/post/..., URLs), verify commands referencing files missing from
// required_files, and layout_root mismatches. Returns "" when the profile is sound.
// Used by the design_review QA step so spec-index guesswork is caught before planning.
func ValidateRigWorkflowProfileForQA(townRoot, rig string, v WorkflowValidation) string {
	var problems []string
	check := func(files []string, scope string) {
		for _, f := range files {
			f = filepath.ToSlash(strings.TrimSpace(f))
			if f == "" {
				continue
			}
			if strings.ContainsAny(f, "*?") || strings.Contains(f, "...") ||
				strings.Contains(f, "://") || strings.ContainsAny(f, "{}") {
				problems = append(problems, fmt.Sprintf("%s contains wildcard/non-file %q", scope, f))
				continue
			}
			if strings.HasPrefix(f, "@") || strings.HasPrefix(f, "/api/") {
				problems = append(problems, fmt.Sprintf("%s contains route stub %q (not a file path)", scope, f))
				continue
			}
			if !IsValidImplementBeadPath(f) {
				problems = append(problems, fmt.Sprintf("%s contains invalid path %q", scope, f))
			}
		}
	}
	check(v.RequiredFiles, "required_files")
	for i := range v.DeliveryPhases {
		phase := &v.DeliveryPhases[i]
		if phase.RequiredFiles == nil || len(phase.RequiredFiles) == 0 {
			problems = append(problems, fmt.Sprintf("phase %q has no required_files — QA cannot verify this phase", phase.ID))
			continue
		}
		check(phase.RequiredFiles, fmt.Sprintf("phase %q required_files", phase.ID))
	}
	// Layout drift: the same basename must not resolve to two different paths (e.g.
	// helloapi/hello.go in one phase vs helloapi/handler/hello.go in another), or
	// go test will fail with "found packages main and hello". Detect it before
	// planning so QA can send the Architect back before any bead is created.
	byBase := map[string]string{}
	for _, f := range v.UnionRequiredFiles() {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if f == "" {
			continue
		}
		if prev, ok := byBase[filepath.Base(f)]; ok && prev != f {
			problems = append(problems, fmt.Sprintf("layout drift: %q and %q share basename %q — required files disagree on directory layout", prev, f, filepath.Base(f)))
			continue
		}
		byBase[filepath.Base(f)] = f
	}
	// Verify commands must reference files that exist in the union of required_files.
	union := v.UnionRequiredFiles()
	if len(union) > 0 {
		for _, cmd := range []string{v.QAVerifyCommand} {
			if cmd == "" {
				continue
			}
			if !commandPathsMatchPhaseFiles(cmd, union) {
				problems = append(problems, fmt.Sprintf("verify command references a path not in required_files: %s", cmd))
			}
		}
	}
	if len(problems) == 0 {
		return ""
	}
	return "workflow profile defects:\n- " + strings.Join(problems, "\n- ")
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

// ProbeParseSpecLayoutTree exposes parseSpecLayoutTree for diagnostic probes.
func ProbeParseSpecLayoutTree(specText string) []string { return parseSpecLayoutTree(specText) }

// mergeArchWinsOnConflict unions two path sets, and when the same basename resolves
// to different paths the architecture.md variant wins (it reflects the concrete
// design decisions planning must implement).
func mergeArchWinsOnConflict(specPaths, archPaths []string) []string {
	archByBase := map[string]string{}
	for _, p := range archPaths {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if p != "" {
			if _, ok := archByBase[filepath.Base(p)]; !ok {
				archByBase[filepath.Base(p)] = p
			}
		}
	}
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, p := range specPaths {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if arch, ok := archByBase[filepath.Base(p)]; ok && arch != p {
			add(arch) // architecture wins on conflict
			continue
		}
		add(p)
	}
	for _, p := range archPaths {
		add(filepath.ToSlash(strings.TrimSpace(p)))
	}
	return out
}
func ProbeExtractSpecLayoutPaths(dir string) ([]string, bool) { return extractSpecLayoutPaths(dir) }
