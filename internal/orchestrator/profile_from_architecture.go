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
	if specPaths, ok, _ := extractSpecLayoutPaths(mayorRigDir); ok {
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

// extractSpecLayoutPaths reads SPEC.md and architecture.md (when present) and returns
// repo-relative paths (e.g. pingapp/main.py). Architecture.md is the authoritative
// source for implementation files once it exists, since the architect expands all
// SPEC abbreviations into concrete file paths.
func extractSpecLayoutPaths(mayorRigDir string) ([]string, bool, map[string]bool) {
	specPath := filepath.Join(mayorRigDir, "SPEC.md")
	data, err := os.ReadFile(specPath)
	if err != nil || len(data) == 0 {
		archDebug("no SPEC.md at %s: %v", specPath, err)
		return nil, false, nil
	}
	specText := string(data)

	archPath := filepath.Join(mayorRigDir, "architecture.md")
	archData, archErr := os.ReadFile(archPath)
	var archText string
	if archErr == nil && len(archData) > 0 {
		archText = string(archData)
	}

	treePaths := parseSpecLayoutTree(specText)
	specDirPrefixes := extractSpecDirPrefixes(specText)
	archDebug("parseSpecLayoutTree from SPEC: %v", treePaths)
	archDebug("extractSpecDirPrefixes: %v", specDirPrefixes)

	// The layout tree is the primary source. Architecture.md supplements it
	// with implementation files the architect expanded (e.g., test files,
	// config files, all frontend/backend components).
	var paths []string
	if len(treePaths) > 0 {
		log.Printf("[extractSpecLayoutPaths] using SPEC layout tree: %d files", len(treePaths))
		paths = treePaths
	} else {
		// Fallback: no parseable tree — use prose backtick refs as last resort.
		log.Printf("[extractSpecLayoutPaths] no layout tree found; falling back to prose backtick extraction")
		archPaths := extractArchPaths(specText, "")
		archDebug("extractArchPaths from SPEC (fallback): %v", archPaths)
		paths = archPaths
	}

	// If architecture.md exists, extract its file paths and union them.
	// Architecture.md is the authoritative source for implementation files
	// once it exists — the architect expands SPEC abbreviations into
	// concrete files (test files, config files, all components).
	if archText != "" {
		archPaths := extractArchPaths(archText, "")
		log.Printf("[extractSpecLayoutPaths] using architecture.md: %d files", len(archPaths))
		// Union: add arch paths not already in SPEC tree
		seen := make(map[string]bool)
		for _, p := range paths {
			seen[p] = true
		}
		for _, p := range archPaths {
			if !seen[p] {
				paths = append(paths, p)
				seen[p] = true
			}
		}
		archDebug("extractArchPaths from architecture.md: %v", archPaths)
	}

	paths = dedupeStrings(paths)
	archDebug("extractSpecLayoutPaths result: %v", paths)

	if len(paths) == 0 {
		archDebug("no paths found in SPEC.md or architecture.md")
		return nil, false, nil
	}
	// Prefer paths with a shared layout prefix (pingapp/...) over flat ./main.py-only lists.
	// Filter out ./ prefixed paths (from architecture.md) before inferring the root,
	// since SPEC layout tree paths are the authoritative source for layout structure.
	var cleanPaths []string
	for _, p := range paths {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if !strings.HasPrefix(p, "./") {
			cleanPaths = append(cleanPaths, p)
		}
	}
	if root := inferLayoutRootFromPaths(cleanPaths); root != "" && root != "." {
		var prefixed []string
		for _, p := range paths {
			p = filepath.ToSlash(strings.TrimSpace(p))
			if strings.HasPrefix(p, root+"/") {
				prefixed = append(prefixed, p)
			}
		}
		if len(prefixed) > 0 {
			return prefixed, true, specDirPrefixes
		}
	}
	// SPEC lists files at repo root (layout_root ".") — still authoritative.
	if len(paths) >= 1 {
		return paths, true, specDirPrefixes
	}
	return nil, false, nil
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
		entry = stripTreeComment(entry)
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
		fileName := ""
		if m := specTreeFileInLine.FindStringSubmatch(entry); len(m) == 2 {
			fileName = m[1]
		} else if name := treeEntryFileName(entry); name != "" {
			fileName = name
		}
		if fileName == "" {
			continue
		}
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
	return out
}

// extractSpecDirPrefixes returns all directory prefixes defined in the SPEC layout
// tree (e.g., "finally/backend/", "finally/backend/db/"). These represent the
// canonical directory structure that architecture.md should follow.
func extractSpecDirPrefixes(specText string) map[string]bool {
	section := specLayoutSection(specText)
	dirs := []string{}
	prefixes := map[string]bool{}
	inCodeFence := false
	hasCodeFence := strings.Contains(section, "```")
	for _, line := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inCodeFence = !inCodeFence
			continue
		}
		if hasCodeFence && !inCodeFence {
			continue
		}
		depth, entry := treeLineDepthEntry(line)
		entry = strings.Trim(entry, "`")
		entry = stripTreeComment(entry)
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
			// Record this directory prefix
			prefix := strings.Join(dirs, "/") + "/"
			prefixes[prefix] = true
			continue
		}
	}
	return prefixes
}

// treeEntryFileName extracts a filename from a markdown tree entry line (comments
// already stripped; directory lines ending in "/" are handled by the caller). The
// last whitespace-delimited token is used, so "pingapp/  requirements.txt" yields
// "requirements.txt". Only tokens that look like file names are accepted: anything
// with an extension, or a known extensionless manifest/dotfile (Dockerfile,
// Makefile, .env, .gitignore, ...). Placeholder/prose tokens like "..." are rejected.
func treeEntryFileName(entry string) string {
	entry = stripTreeComment(entry)
	if entry == "" || strings.HasSuffix(entry, "/") {
		return ""
	}
	tokens := strings.Fields(entry)
	if len(tokens) == 0 {
		return ""
	}
	last := tokens[len(tokens)-1]
	if !looksLikeFileName(last) {
		return ""
	}
	return last
}

// extensionlessManifestRE names files that have no extension but are still files
// (Dockerfiles, Makefiles, dotfiles) — needed because specTreeFileInLine only
// matches extension-bearing names.
var extensionlessManifestRE = regexp.MustCompile(`(?i)^(?:dockerfile|containerfile|makefile|\.env(?:\.example)?|\.gitignore|\.gitkeep|\.dockerignore|\.gitattributes|\.editorconfig)$`)

func looksLikeFileName(name string) bool {
	if name == "" || name == "..." || strings.ContainsAny(name, "()[]{}<>") {
		return false
	}
	if strings.Contains(name, ".") {
		return len(name) > 1
	}
	return extensionlessManifestRE.MatchString(name)
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
	// Prefer a code-fenced file tree wherever it lives in the SPEC. Content-based
	// detection finds trees under headings like "## 4. Directory Structure" that
	// named-section scans miss, and never mistakes a "### Layout" prose subsection
	// for the layout tree.
	if sec := treeFenceBlock(specText); sec != "" {
		return sec
	}
	return headingLayoutSection(specText)
}

// treeFenceBlock returns the first code-fenced block in the SPEC that looks like a
// file tree (contains a directory line ending in "/"), including the ``` fences.
func treeFenceBlock(specText string) string {
	lines := strings.Split(specText, "\n")
	var block []string
	inFence := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inFence {
				inFence = false
				block = append(block, line)
				if treeBlockLooksLikeLayout(block) {
					return strings.Join(block, "\n")
				}
				block = nil
			} else {
				inFence = true
				block = []string{line}
			}
			continue
		}
		if inFence {
			block = append(block, line)
		}
	}
	return ""
}

// treeBlockLooksLikeLayout reports whether a code-fenced block contains a directory
// entry (a line whose content ends in "/" once comments are stripped) — the
// signature of a markdown file tree. ASCII diagrams, JSON samples, and command
// blocks do not contain trailing-slash directory lines.
func treeBlockLooksLikeLayout(block []string) bool {
	for _, line := range block {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			continue
		}
		if strings.HasSuffix(stripTreeComment(trimmed), "/") {
			return true
		}
	}
	return false
}

// headingLayoutSection returns the section under a named level-2 layout heading
// ("## Layout", "## File Layout", "## Directory Structure", ...), preferring the
// candidate that contains a code fence. Only exact level-2 headings are matched so
// a "### Layout" level-3 subsection is never mistaken for the layout tree.
func headingLayoutSection(specText string) string {
	lines := strings.Split(specText, "\n")
	var candidates []string
	for i, line := range lines {
		if !isLayoutHeading(line) {
			continue
		}
		sec := lines[i:]
		for j := i + 1; j < len(lines); j++ {
			if isLevel2Heading(lines[j]) {
				sec = lines[i:j]
				break
			}
		}
		candidates = append(candidates, strings.Join(sec, "\n"))
	}
	if len(candidates) == 0 {
		return specText
	}
	for _, c := range candidates {
		if strings.Contains(c, "```") {
			return c
		}
	}
	return candidates[0]
}

// isLayoutHeading reports whether a line is a level-2 heading that names the
// project's file layout section ("## Layout", "## File Layout", "## 4. Directory
// Structure", ...). Level-3 headings like "### Layout" are excluded.
func isLayoutHeading(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !isLevel2Heading(trimmed) {
		return false
	}
	text := strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
	text = regexp.MustCompile(`^\d+[\.\)\-]?\s*`).ReplaceAllString(text, "")
	lower := strings.ToLower(text)
	for _, kw := range []string{"layout", "directory structure", "project structure", "folder structure", "file tree", "directory tree", "directory layout"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// isLevel2Heading reports whether a line is exactly a "## Heading" (not "###").
func isLevel2Heading(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "## ") && !strings.HasPrefix(trimmed, "### ")
}

// stripTreeComment removes a trailing "# comment" annotation from a markdown tree
// entry ("frontend/  # Next.js project" -> "frontend/"). Only a comment preceded by
// whitespace is stripped so a filename that itself contains '#' is preserved.
func stripTreeComment(s string) string {
	if idx := strings.Index(s, " #"); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
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
		install := "pip install -r " + req
		uv := isUvProject(req) || isUvManaged(v)
		if uv {
			if dir := filepath.Dir(filepath.ToSlash(req)); dir != "." && dir != "" {
				install = "cd " + dir + " && uv sync"
			} else {
				install = "uv sync"
			}
		} else if strings.HasSuffix(strings.ToLower(filepath.ToSlash(req)), ".toml") {
			venvPip := v.PythonVenvRelDir() + "/bin/pip"
			install = PipInstallRequirementsCmd(venvPip, req)
		}
		return "Python rig only — no go mod. Create " + v.PythonVenvRelDir() +
			", run `" + install + "` once. Green verify: " +
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
	if specPaths, ok, specDirPrefixes := extractSpecLayoutPaths(mayorRig); ok {
		archDebug("sync: SPEC layout paths=%v arch paths=%v", specPaths, filePaths)
		authoritative = mergeArchWinsOnConflict(specPaths, filePaths, specDirPrefixes)
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
	if root := inferLayoutRootFromPaths(authoritative); root != "" && root != "." {
		env.Validation.LayoutRoot = root
		env.Validation.BeadTitleContains = "Implement " + root + "/"
	}
	env.Validation.TestRunner = "" // force re-inference on sync
	env.Validation = inferTestRunnerFromPaths(env.Validation, authoritative)

	archPhases := parseArchPhases(string(archData), env.Validation.LayoutRootDir())
	// Prefer architect's phases when they are well-formed (clean titles/IDs, cover the authoritative set).
	// Fall back to SPEC phases when architect phases are mangled (backtick-laden, path-embedded titles)
	// or don't meaningfully cover the file set. This preserves the keepsSpecPhases regression test.
	if len(archPhases) > 0 {
		if archPhasesWellFormed(archPhases, authoritative) {
			// Add depends_on chain for sequential architect phases
			for i := range archPhases {
				if i > 0 {
					archPhases[i].DependsOn = []string{archPhases[i-1].ID}
				}
			}
			env.Validation.DeliveryPhases = archPhases
			// SYSTEMATIC FIX: When architect phases are well-formed, also replace each
			// phase's RequiredFiles with the ## Requirements section from architecture.md.
			// This eliminates spec-index hallucinations (e.g. handlers_test.go) that
			// may have been in the profile from before architecture.md existed, making
			// architecture.md the authoritative source over spec-index-derived profiles.
			env.Validation, _ = updatePhaseRequiredFilesFromRequirementsSection(env.Validation, string(archData))
		} else if len(env.Validation.DeliveryPhases) == 0 {
			env.Validation.DeliveryPhases = archPhases
		}
	}

	// Update each existing phase's required_files from the "## Requirements" section
	// of architecture.md (which has ### <phase-id> headings with clean file lists).
	// This avoids greedy extraction from the whole document and preserves profile phase IDs.
	// Note: When archPhasesWellFormed was true, RequiredFiles were already systematically
	// replaced above via updatePhaseRequiredFilesFromRequirementsSection, making architecture.md
	// the authoritative source over spec-index hallucinations.
	_, updatedFromReqs := updatePhaseRequiredFilesFromRequirementsSection(env.Validation, string(archData))
	// Top-level RequiredFiles = union of all phase required_files (deduped)
	seen := make(map[string]bool)
	var union []string
	for i := range env.Validation.DeliveryPhases {
		for _, f := range env.Validation.DeliveryPhases[i].RequiredFiles {
			f = filepath.ToSlash(strings.TrimSpace(f))
			if f != "" && !seen[f] {
				seen[f] = true
				union = append(union, f)
			}
		}
	}
	// If we didn't successfully update from Requirements section, use authoritative paths
	// for top-level and redistribute to phases via rebuildDeliveryPhasesFromAuthoritative.
	if !updatedFromReqs {
		union = authoritative
		env.Validation.RequiredFiles = union
		env.Validation = rebuildDeliveryPhasesFromAuthoritative(env.Validation, authoritative)
	} else {
		env.Validation.RequiredFiles = union
	}
	env.Validation = updateTopLevelFromCanonicalSection(env.Validation, string(archData))
	// Rebuilt/re-distributed phases may have empty or wrong-stack verify commands;
	// fill them with stack-appropriate defaults (never go vet in a Python phase).
	env.Validation = SanitizePhaseVerifyCommandsForStack(env.Validation)
	// SPEC/architecture are authoritative for the runtime smoke too. When they
	// document an HTTP server + probes, ensure the profile has a smoke phase whose
	// qa_verify_command starts the server and curls the documented routes — the
	// first spec-index run (before architecture.md exists) can emit a pytest-only
	// profile that QA later rejects ("requires a successful runtime smoke CMD").
	env.Validation = ensureRuntimeSmokePhaseFromSpec(townRoot, rig, env.Validation)
	// Apply full sanitization: doubled layout paths, --no-cache stripping,
	// .gastown/ filtering, path prefix normalization, etc.
	env.Validation = SanitizeRigFlowProfile(env.Validation)
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
		// Collapse duplicated layout root prefix (e.g., "finally/finally/backend/..." -> "finally/backend/...").
		if target != "" && strings.HasPrefix(p, target+"/"+target+"/") {
			p = strings.TrimPrefix(p, target+"/")
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
	// A docker-compose invocation that boots the app and propagates the test
	// container's exit code IS a runtime smoke — recognize it so
	// ensureRuntimeSmokePhaseFromSpec stops appending a duplicate smoke-test
	// phase alongside an existing integration-test phase.
	if strings.Contains(lower, "docker-compose") || strings.Contains(lower, "docker compose") {
		return true
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
			curls = append(curls, fmt.Sprintf(`curl -s -X POST -d '%s' %s%s`, body, base, path))
		} else {
			curls = append(curls, fmt.Sprintf("curl -s %s%s", base, path))
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
		// Reject runtime database files (SQLite, etc.) — created at runtime, not committed
		lower := strings.ToLower(p)
		if strings.HasSuffix(lower, ".db") || strings.HasSuffix(lower, ".sqlite") ||
			strings.HasSuffix(lower, ".sqlite3") {
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
	// Capture each phase's original basenames AND directories first — after the
	// keep-loop a phase whose files were renamed goes empty, so provenance is lost.
	origBase := make([]map[string]bool, len(v.DeliveryPhases))
	origDirs := make([]map[string]int, len(v.DeliveryPhases))
	origDirPrefixes := make([][]string, len(v.DeliveryPhases))
	for i := range v.DeliveryPhases {
		origBase[i] = map[string]bool{}
		origDirs[i] = map[string]int{}
		prefixSet := map[string]bool{}
		for _, f := range v.DeliveryPhases[i].RequiredFiles {
			f = filepath.ToSlash(strings.TrimSpace(f))
			origBase[i][filepath.Base(f)] = true
			dir := filepath.Dir(f)
			origDirs[i][dir]++
			// Also track all prefix directories (e.g., "pingapp" for "pingapp/cmd/server/main.go")
			parts := strings.Split(dir, "/")
			for j := 1; j <= len(parts); j++ {
				prefix := strings.Join(parts[:j], "/")
				prefixSet[prefix] = true
			}
		}
		for p := range prefixSet {
			origDirPrefixes[i] = append(origDirPrefixes[i], p)
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
		// Third pass: token matching against phase ID, Title, and SpecFocus.
		// This is the primary semantic matcher for layout restructures.
		if bestIdx < 0 {
			bestScore := 0
			for i, phase := range v.DeliveryPhases {
				score := scorePhaseForFilePath(phase, p)
				if score > bestScore {
					bestScore = score
					bestIdx = i
				}
			}
		}
		// Fourth pass: Same-directory-prefix fallback using original phase directories
		// (captured before keep-loop). This handles layout restructure where files
		// move but phases keep their semantic directory ownership.
		// Match if the file's directory has one of the phase's original directory prefixes.
		if bestIdx < 0 {
			bestScore := -1
			fileDir := filepath.Dir(p)
			for i := range v.DeliveryPhases {
				score := 0
				for _, prefix := range origDirPrefixes[i] {
					if strings.HasPrefix(fileDir, prefix+"/") || fileDir == prefix {
						// Prefer longer (more specific) prefixes
						if len(prefix) > score {
							score = len(prefix)
						}
					}
				}
				if score > bestScore {
					bestScore = score
					bestIdx = i
				}
			}
		}
		if bestIdx < 0 {
			bestIdx = 0
		}
		v.DeliveryPhases[bestIdx].RequiredFiles = append(v.DeliveryPhases[bestIdx].RequiredFiles, p)
	}

	// Prune any phase that remains empty after distributing all authoritative files,
	// unless all phases are empty (in which case rebuild via PhasesFromFilePaths).
	var keptPhases []DeliveryPhase
	for _, phase := range v.DeliveryPhases {
		if len(phase.RequiredFiles) > 0 {
			keptPhases = append(keptPhases, phase)
		}
	}
	if len(keptPhases) == 0 {
		v.DeliveryPhases = PhasesFromFilePaths(authoritative)
	} else {
		v.DeliveryPhases = keptPhases
	}
	if v.ActivePhaseID() == "" || !phaseIDExists(v, v.ActivePhaseID()) {
		v.ActivePhaseIDField = v.DeliveryPhases[0].ID
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
// is a spec-index artifact rather than a deliberate split: as many phases as files
// (one file per phase) or all phases empty.
func degenerateDeliveryPhaseStructure(v WorkflowValidation, authoritative []string) bool {
	if len(v.DeliveryPhases) == 0 {
		return true
	}
	if len(authoritative) > 0 && len(v.DeliveryPhases) >= len(authoritative) {
		return true
	}
	allEmpty := true
	for i := range v.DeliveryPhases {
		if len(v.DeliveryPhases[i].RequiredFiles) > 0 {
			allEmpty = false
			break
		}
	}
	return allEmpty
}

// parseArchPhases parses delivery phases and their required file paths from the
// "## Delivery phases" section of architecture.md.
func parseArchPhases(archText, layoutRoot string) []DeliveryPhase {
	if archText == "" {
		return nil
	}
	// Try sections in order: "## Delivery phases" (table), "## Requirements" (proper headings)
	for _, marker := range []string{"## delivery phases", "## requirements", "## phases"} {
		phases := parseArchPhasesFromSection(archText, layoutRoot, marker)
		if len(phases) > 0 {
			return phases
		}
	}
	return nil
}

func parseArchPhasesFromSection(archText, layoutRoot, marker string) []DeliveryPhase {
	lower := strings.ToLower(archText)
	markerIdx := strings.Index(lower, marker)
	if markerIdx < 0 {
		return nil
	}
	section := archText[markerIdx:]
	if j := strings.Index(section[1:], "\n## "); j >= 0 {
		section = section[:1+j]
	}

	lines := strings.Split(section, "\n")
	var phases []DeliveryPhase
	var currentPhase *DeliveryPhase
	targetRoot := strings.Trim(filepath.ToSlash(strings.TrimSpace(layoutRoot)), "/")
	if targetRoot == "." {
		targetRoot = ""
	}

	flush := func() {
		if currentPhase != nil {
			currentPhase.RequiredFiles = dedupeStrings(currentPhase.RequiredFiles)
			if currentPhase.Title != "" || len(currentPhase.RequiredFiles) > 0 {
				phases = append(phases, *currentPhase)
			}
			currentPhase = nil
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "```") {
			continue
		}

		if p := parseArchPhaseHeader(trimmed); p != nil {
			flush()
			currentPhase = p
		}

		if currentPhase != nil {
			matches := extractArchPaths(line, layoutRoot)
			matches = append(matches, extractPhaseLinePaths(line, layoutRoot)...)
			matches = append(matches, extractInlinePaths(line, layoutRoot)...)
			for _, m := range matches {
				p := filepath.ToSlash(strings.TrimSpace(m))
				for strings.HasPrefix(p, "./") {
					p = p[2:]
				}
				if targetRoot != "" && !strings.HasPrefix(p, targetRoot+"/") && !strings.Contains(p, "/") {
					p = targetRoot + "/" + p
				}
				if isImplementableFilePath(p) && IsValidImplementBeadPath(p) {
					currentPhase.RequiredFiles = append(currentPhase.RequiredFiles, p)
				}
			}
		}
	}
	flush()
	return phases
}

func updatePhaseRequiredFilesFromRequirementsSection(v WorkflowValidation, archData string) (WorkflowValidation, bool) {
	if len(v.DeliveryPhases) == 0 || archData == "" {
		return v, false
	}
	// Extract the "## Requirements" section
	lower := strings.ToLower(archData)
	reqIdx := strings.Index(lower, "## requirements")
	if reqIdx < 0 {
		// No "## Requirements" section — fall back to extracting from whole document
		// using parseArchPhases (which tries multiple sections)
		archPhases := parseArchPhases(archData, v.LayoutRootDir())
		if len(archPhases) > 0 {
			// Use the parsed phases' required_files to update existing phases
			phaseFiles := make(map[string][]string)
			for _, p := range archPhases {
				if len(p.RequiredFiles) > 0 {
					phaseFiles[p.ID] = p.RequiredFiles
				}
			}
			for i := range v.DeliveryPhases {
				if files, ok := phaseFiles[v.DeliveryPhases[i].ID]; ok && len(files) > 0 {
					v.DeliveryPhases[i].RequiredFiles = dedupeStrings(files)
				}
			}
			return v, true
		}
		return v, false
	}
	section := archData[reqIdx:]
	if j := strings.Index(section[1:], "\n## "); j >= 0 {
		section = section[:1+j]
	}

	// Parse ### <phase-id> headings and their file lists.
	// Stop before "### Canonical" subsection which has malformed backtick lists.
	phaseFiles := make(map[string][]string)
	lines := strings.Split(section, "\n")
	currentPhase := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "### ") {
			phaseID := strings.TrimSpace(strings.TrimPrefix(trimmed, "### "))
			// Skip the "Canonical" subsection
			if strings.HasPrefix(strings.ToLower(phaseID), "canonical") {
				currentPhase = ""
				continue
			}
			// Match phase ID if it matches a known delivery phase OR is kebab-case
			phaseIDLower := strings.ToLower(phaseID)
			isKnownPhase := false
			for _, dp := range v.DeliveryPhases {
				if strings.ToLower(dp.ID) == phaseIDLower {
					isKnownPhase = true
					break
				}
			}
			if isKnownPhase || (strings.Contains(phaseID, "-") && !strings.Contains(phaseID, " ") && !strings.Contains(phaseID, ".")) {
				currentPhase = phaseID
				phaseFiles[currentPhase] = []string{}
			} else {
				currentPhase = ""
			}
			continue
		}
		if currentPhase != "" {
			// Extract backtick paths from this line
			matches := extractArchPaths(line, v.LayoutRootDir())
			matches = append(matches, extractPhaseLinePaths(line, v.LayoutRootDir())...)
			// Architect prose often names the file inline (e.g. "pingapp/main.py
			// must expose app = FastAPI()") with no backticks or bullets. Without
			// extractInlinePaths those per-phase files never land in the phase's
			// required_files, so the sync keeps stale spec-index assignments
			// (core absorbing test_main.py, test left empty).
			matches = append(matches, extractInlinePaths(line, v.LayoutRootDir())...)
			for _, m := range matches {
				p := filepath.ToSlash(strings.TrimSpace(m))
				for strings.HasPrefix(p, "./") {
					p = p[2:]
				}
				// Clean backtick artifacts like `linkshelf/...`
				p = strings.Trim(p, "`")
				// Remove any embedded backticks like linkshelf/`linkshelf/...
				if strings.Contains(p, "`") {
					p = strings.ReplaceAll(p, "`", "")
				}
				if isImplementableFilePath(p) && IsValidImplementBeadPath(p) {
					phaseFiles[currentPhase] = append(phaseFiles[currentPhase], p)
				}
			}
		}
	}

	// Update each phase's required_files if we found files for it
	for i := range v.DeliveryPhases {
		if files, ok := phaseFiles[v.DeliveryPhases[i].ID]; ok && len(files) > 0 {
			v.DeliveryPhases[i].RequiredFiles = dedupeStrings(files)
		}
	}
	return v, true
}

func extractInlinePaths(line, layoutRoot string) []string {
	// Extract inline file paths from prose like "Create pingapp/main.py" or "Deliver linkshelf/go.mod"
	// Uses the layoutRoot to build a dynamic regex instead of hardcoding one layout.
	var out []string
	seen := map[string]bool{}
	if layoutRoot == "" {
		return nil
	}
	// Match layoutRoot/some.path.ext (letters, digits, underscores, hyphens, slashes, dots)
	re := regexp.MustCompile(regexp.QuoteMeta(layoutRoot) + `/[a-zA-Z0-9_/.-]+\.[a-zA-Z0-9]+`)
	matches := re.FindAllString(line, -1)
	for _, m := range matches {
		m = strings.TrimSpace(m)
		m = strings.Trim(m, "`")
		m = strings.ReplaceAll(m, "`", "")
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		if isImplementableFilePath(m) && IsValidImplementBeadPath(m) {
			out = append(out, m)
		}
	}
	return out
}

func updateTopLevelFromCanonicalSection(v WorkflowValidation, archData string) WorkflowValidation {
	if archData == "" {
		return v
	}
	lower := strings.ToLower(archData)
	var canonicalSection string
	// Look for "The following four phase IDs are canonical" (specific to this format)
	canonicalRe := regexp.MustCompile(`(?i)the following four phase ids are canonical`)
	canonicalIdx := -1
	if loc := canonicalRe.FindStringIndex(lower); loc != nil {
		canonicalIdx = loc[0]
		canonicalSection = archData[canonicalIdx:]
		if j := strings.Index(canonicalSection[1:], "\n## "); j >= 0 {
			canonicalSection = canonicalSection[:1+j]
		}
		// Stop at "The canonical top-level required files are exactly" to avoid
		// parsing the negative-list and top-level paragraphs which contain
		// backtick paths that would pollute command extraction.
		if stopIdx := strings.Index(strings.ToLower(canonicalSection), "the canonical top-level required files are exactly"); stopIdx >= 0 {
			canonicalSection = canonicalSection[:stopIdx]
		}
	} else {
		// Fallback: try "### canonical"
		canonicalIdx = strings.Index(lower, "### canonical")
		if canonicalIdx >= 0 {
			canonicalSection = archData[canonicalIdx:]
			if j := strings.Index(canonicalSection[1:], "\n### "); j >= 0 {
				canonicalSection = canonicalSection[:1+j]
			}
		}
	}

	if canonicalSection == "" {
		return v
	}

	// Parse the canonical section ONLY for qa_verify_commands (not required_files,
	// which are already clean from phase union).
	lines := strings.Split(canonicalSection, "\n")
	var topQA string
	phaseQA := make(map[string]string)
	currentPhase := ""
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Track phase in the canonical section (look for ### backend-store, **backend-store**, etc.)
		if strings.HasPrefix(trimmed, "### ") {
			phaseID := strings.TrimSpace(strings.TrimPrefix(trimmed, "### "))
			if strings.Contains(phaseID, "-") && !strings.Contains(phaseID, " ") && !strings.Contains(phaseID, "<") {
				currentPhase = phaseID
			}
		} else if strings.HasPrefix(trimmed, "**") && strings.HasSuffix(trimmed, "**") && strings.Contains(trimmed, "-") {
			phaseID := strings.Trim(trimmed, "*")
			phaseID = strings.TrimSpace(phaseID)
			if strings.Contains(phaseID, "-") && !strings.Contains(phaseID, " ") {
				currentPhase = phaseID
			}
		}
		// Look for command patterns: "Verify with", "verification is", "command is", "command:", "qa_verify_command"
		isCommandLine := strings.Contains(strings.ToLower(trimmed), "verify with") ||
			strings.Contains(strings.ToLower(trimmed), "verification is") ||
			strings.Contains(strings.ToLower(trimmed), "command is") ||
			strings.Contains(strings.ToLower(trimmed), "command:") ||
			strings.Contains(strings.ToLower(trimmed), "qa_verify_command")
		if isCommandLine {
			// Extract command from backticks on this line OR the next line(s)
			var cmd string
			if idx := strings.Index(trimmed, "`"); idx >= 0 {
				end := strings.Index(trimmed[idx+1:], "`")
				if end >= 0 {
					cmd = strings.TrimSpace(trimmed[idx+1 : idx+1+end])
				}
			}
			// If no backtick on this line, check next few lines
			if cmd == "" {
				for j := i + 1; j < len(lines) && j <= i+3; j++ {
					nextLine := strings.TrimSpace(lines[j])
					if idx := strings.Index(nextLine, "`"); idx >= 0 {
						end := strings.Index(nextLine[idx+1:], "`")
						if end >= 0 {
							cmd = strings.TrimSpace(nextLine[idx+1 : idx+1+end])
							break
						}
					}
					// Stop if we hit another phase or section
					if strings.HasPrefix(nextLine, "**") && strings.HasSuffix(nextLine, "**") {
						break
					}
				}
			}
			if cmd != "" {
				// Skip if cmd looks like a file path (contains / but no shell command verbs)
				if strings.Contains(cmd, "/") && !strings.ContainsAny(cmd, " &|;<>") && !regexp.MustCompile(`\b(cd|go|npm|npx|python|pytest|make|build|test|run)\b`).MatchString(cmd) {
					// This looks like a file path, not a command - skip it
				} else {
					if currentPhase != "" {
						phaseQA[currentPhase] = cmd
					} else {
						topQA = cmd
					}
				}
			}
		}
	}

	if topQA != "" {
		v.QAVerifyCommand = topQA
	}
	// Update phase qa_verify_commands from canonical section
	for i := range v.DeliveryPhases {
		if cmd, ok := phaseQA[v.DeliveryPhases[i].ID]; ok {
			v.DeliveryPhases[i].QAVerifyCommand = cmd
		}
	}
	return v
}

var archVerbRe = regexp.MustCompile(`\b(creates|builds|implements|adds|completes|includes|delivers|wires|sets up|provides|generates|introduces|establishes|installs|configures|initializes|starts|runs|writes|produces|scaffolds|contains|verifies|tests|creates?)\b`)

func parseArchPhaseHeader(line string) *DeliveryPhase {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "```") {
		return nil
	}
	s := strings.TrimLeft(trimmed, "# \t")
	s = strings.TrimLeft(s, "-* \t")

	var title string
	if m := regexp.MustCompile(`^\d+[\.\)]\s*(.+)`).FindStringSubmatch(s); len(m) == 2 {
		rest := m[1]
		rest = strings.TrimPrefix(rest, "**")
		if idx := strings.Index(rest, "**"); idx > 0 {
			rest = rest[:idx]
		}
		if idx := strings.IndexAny(rest, ":—"); idx > 0 {
			title = strings.TrimSpace(rest[:idx])
		} else {
			if vIdx := archVerbRe.FindStringIndex(rest); vIdx != nil && vIdx[0] > 0 {
				title = strings.TrimSpace(rest[:vIdx[0]])
			} else {
				title = strings.TrimSpace(rest)
			}
		}
	} else if strings.HasPrefix(s, "Phase ") {
		rest := strings.TrimPrefix(s, "Phase ")
		if idx := strings.Index(rest, " "); idx > 0 {
			rest = rest[idx+1:]
		}
		rest = strings.TrimLeft(rest, " \t—-:")
		if idx := strings.IndexAny(rest, ":—"); idx > 0 {
			title = strings.TrimSpace(rest[:idx])
		} else {
			title = strings.TrimSpace(rest)
		}
	} else if isKebabPhaseID(s) {
		// Markdown heading like "### backend-store" -> "backend-store"
		title = s
	}

	if title != "" {
		id := slugify(title)
		if id != "" {
			return &DeliveryPhase{
				ID:            id,
				Title:         title,
				RequiredFiles: []string{},
			}
		}
	}
	return nil
}

func isKebabPhaseID(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	// Reject markdown table separators like |---|---|
	if strings.HasPrefix(s, "|") || strings.HasSuffix(s, "|") {
		return false
	}
	// Reject lines that are just dashes/pipes (table separators)
	if regexp.MustCompile(`^[\-|:\s]+$`).MatchString(s) {
		return false
	}
	if strings.Contains(s, " ") || strings.Contains(s, ".") {
		return false
	}
	if regexp.MustCompile(`[A-Z]`).MatchString(s) {
		return false
	}
	// Reject if it contains a pipe (table cell) or is purely numeric
	if strings.Contains(s, "|") || regexp.MustCompile(`^\d+$`).MatchString(s) {
		return false
	}
	// Common prose words that aren't phase IDs
	proseWords := []string{"the", "and", "or", "but", "for", "with", "from", "into", "onto", "upon", "within", "without", "about", "after", "before", "during", "since", "until", "while", "where", "which", "whose", "that", "this", "these", "those", "then", "than", "when", "what", "who", "whom", "why", "how", "all", "any", "each", "every", "some", "such", "only", "own", "same", "other", "another", "more", "most", "less", "few", "many", "much", "very", "too", "so", "as", "if", "because", "since", "unless", "until", "while", "whereas", "whereby", "wherein", "whereupon", "wherever", "whether", "which", "whichever", "whoever", "whomever", "whose", "why", "however", "moreover", "nevertheless", "therefore", "thus", "hence", "accordingly", "consequently", "furthermore", "meanwhile", "otherwise", "besides", "instead", "likewise", "similarly", "indeed", "certainly", "probably", "possibly", "apparently", "evidently", "obviously", "presumably", "seemingly", "supposedly", "theoretically", "practically", "virtually", "essentially", "basically", "actually", "really", "truly", "surely", "clearly", "plainly", "obviously", "manifestly", "patently", "transparently", "unmistakably", "indisputably", "undeniably", "incontrovertibly", "irrefutably", "canonical"}
	for _, w := range proseWords {
		if s == w {
			return false
		}
	}
	return true
}

// extractPhaseLinePaths extracts plain comma-separated file paths from an architecture phase line.
// The fin architecture format uses "1. Title creates path1, path2, and path3." without backticks.
// Returns deduplicated paths that pass isLikelyRepoFilePath.
func extractPhaseLinePaths(line, layoutRoot string) []string {
	seen := map[string]bool{}
	var out []string
	// Split on commas, semicolons, and " and " as separators
	re := regexp.MustCompile(`[,;]|\band\b`)
	parts := re.Split(line, -1)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// Strip trailing sentence-ending punctuation (but NOT periods in file extensions)
		part = strings.TrimRight(part, " ")
		// If part contains whitespace (prose), try to extract layoutRoot/... path from it
		if strings.ContainsAny(part, " \t") && layoutRoot != "" {
			if idx := strings.Index(part, layoutRoot+"/"); idx >= 0 {
				candidate := part[idx:]
				// Trim at next whitespace, comma, or backtick to isolate the path.
				// Do NOT trim at periods — they're part of file extensions.
				for i, ch := range candidate {
					if ch == ' ' || ch == '\t' || ch == ',' || ch == ';' || ch == '`' {
						candidate = candidate[:i]
						break
					}
				}
				// Trim trailing sentence-ending periods and backticks
				candidate = strings.TrimRight(candidate, ".`")
				if candidate != "" && !seen[candidate] && isLikelyRepoFilePath(candidate, layoutRoot) {
					seen[candidate] = true
					out = append(out, candidate)
				}
				continue
			}
		}
		// Trim leading/trailing backticks from comma-split parts (backtick-wrapped paths)
		part = strings.Trim(part, "`")
		// Skip prose fragments (no path-like structure)
		if !strings.Contains(part, "/") && !strings.HasPrefix(part, layoutRoot) && !isLikelyRepoFilePath(part, layoutRoot) {
			continue
		}
		// Trim trailing sentence-ending periods and backticks from comma-split parts
		part = strings.TrimRight(part, ".`")
		if isLikelyRepoFilePath(part, layoutRoot) && !seen[part] {
			seen[part] = true
			out = append(out, part)
		}
	}
	return out
}

// archPhasesWellFormed reports whether the architect's phases are clean and complete enough
// to replace SPEC-derived phases. Rejects mangled phases with backtick-laden titles,
// path-embedded IDs, or insufficient file coverage.
func archPhasesWellFormed(phases []DeliveryPhase, authoritative []string) bool {
	if len(phases) < 2 {
		return false
	}
	hasFilePhase := false
	for _, p := range phases {
		title := strings.TrimSpace(p.Title)
		id := strings.TrimSpace(p.ID)
		if title == "" || id == "" {
			return false
		}
		// Title must not contain backticks, path separators, commas, or file-extension tokens
		if strings.Contains(title, "`") || strings.Contains(title, "/") || strings.Contains(title, ",") {
			return false
		}
		if hasFileExtToken(title) {
			return false
		}
		// ID must be a reasonable slug
		if strings.Contains(id, "`") || strings.Contains(id, "/") || strings.Contains(id, ",") || len(id) > 48 {
			return false
		}
		if len(p.RequiredFiles) > 0 {
			hasFilePhase = true
		}
	}
	if !hasFilePhase {
		return false
	}
	// Union of phase files should cover a meaningful fraction of authoritative set
	covered := 0
	for _, auth := range authoritative {
		for _, ph := range phases {
			if containsString(ph.RequiredFiles, auth) {
				covered++
				break
			}
		}
	}
	if len(authoritative) > 0 && covered*100 < len(authoritative)*50 {
		return false // require at least 50% coverage
	}
	return true
}

// hasFileExtToken reports whether s contains a token that looks like a file path with extension
func hasFileExtToken(s string) bool {
	// Look for patterns like word.word where the suffix is a known extension
	tokens := strings.Fields(s)
	exts := map[string]bool{
		".go": true, ".py": true, ".js": true, ".ts": true, ".jsx": true, ".tsx": true,
		".md": true, ".txt": true, ".json": true, ".yaml": true, ".yml": true,
		".toml": true, ".ini": true, ".cfg": true, ".conf": true, ".config": true,
		".env": true, ".example": true, ".gitignore": true, ".dockerignore": true,
		".html": true, ".css": true, ".scss": true, ".sql": true, ".sh": true,
		".ps1": true, ".bat": true, ".cmd": true, ".lock": true,
	}
	for _, t := range tokens {
		for ext := range exts {
			if strings.HasSuffix(strings.ToLower(t), ext) {
				return true
			}
		}
	}
	return false
}

func scorePhaseForFilePath(phase DeliveryPhase, p string) int {
	pLower := strings.ToLower(p)
	pBase := strings.ToLower(filepath.Base(p))
	text := strings.ToLower(phase.ID + " " + phase.Title + " " + phase.SpecFocus)
	score := 0

	// E2E/integration test files (playwright, .spec.ts, e2e/ dir) -> integration/test/release phases
	isE2ETest := strings.Contains(pLower, "/e2e/") || strings.Contains(pLower, "playwright") || strings.HasSuffix(pBase, ".spec.ts") || strings.HasSuffix(pBase, ".spec.tsx")
	// Unit test files (_test.go, test_*.py, conftest.py) -> stay with module phase
	isUnitTest := strings.HasSuffix(pBase, "_test.go") || strings.HasPrefix(pBase, "test_") || strings.Contains(pLower, "conftest.py")

	if isE2ETest {
		if strings.Contains(text, "integration") || strings.Contains(text, "e2e") || strings.Contains(text, "playwright") || strings.Contains(text, "test") || strings.Contains(text, "verification") || strings.Contains(text, "qa") || strings.Contains(text, "release") {
			score += 15
		}
	} else if isUnitTest {
		// Don't auto-assign unit tests to test phases; they follow their module
	} else if strings.Contains(pLower, "/tests/") || strings.Contains(pLower, "/test/") {
		// Generic test directory - slight preference for test phases
		if strings.Contains(text, "test") || strings.Contains(text, "verification") || strings.Contains(text, "qa") {
			score += 5
		}
	}
	if strings.Contains(pLower, "/frontend/") || strings.Contains(pLower, "/web/") || strings.HasSuffix(pBase, ".tsx") || strings.HasSuffix(pBase, ".css") || strings.HasSuffix(pBase, ".html") {
		if strings.Contains(text, "frontend") || strings.Contains(text, "ui") || strings.Contains(text, "web") || strings.Contains(text, "watchlist") || strings.Contains(text, "visualisation") || strings.Contains(text, "visualization") {
			score += 10
		}
	}
	if strings.Contains(pLower, "/market/") || strings.Contains(pLower, "/store/") || strings.Contains(pLower, "/db/") {
		if strings.Contains(text, "market") || strings.Contains(text, "persistence") || strings.Contains(text, "database") || strings.Contains(text, "engine") || strings.Contains(text, "store") {
			score += 10
		}
	}
	if strings.Contains(pLower, "/llm/") || strings.Contains(pLower, "/ai/") || strings.Contains(pLower, "chat") {
		if strings.Contains(text, "ai") || strings.Contains(text, "copilot") || strings.Contains(text, "assistant") || strings.Contains(text, "llm") || strings.Contains(text, "chat") {
			score += 10
		}
	}
	if strings.Contains(pLower, "/api/") || strings.Contains(pLower, "main.py") || strings.Contains(pLower, "main.go") {
		if strings.Contains(text, "api") || strings.Contains(text, "route") || strings.Contains(text, "copilot") || strings.Contains(text, "server") || strings.Contains(text, "core") {
			score += 5
		}
	}
	// /cmd/ and /server/ directories typically belong to core/backend phases
	if strings.Contains(pLower, "/cmd/") || strings.Contains(pLower, "/server/") {
		if strings.Contains(text, "core") || strings.Contains(text, "backend") || strings.Contains(text, "server") || strings.Contains(text, "api") {
			score += 10
		}
	}
	if strings.Contains(pLower, "/scripts/") || strings.Contains(pBase, "dockerfile") || strings.Contains(pBase, "docker-compose") {
		if strings.Contains(text, "release") || strings.Contains(text, "packaging") || strings.Contains(text, "foundation") || strings.Contains(text, "setup") || strings.Contains(text, "docker") {
			score += 5
		}
		// Integration/test/release phases should own infrastructure files
		if strings.Contains(text, "integration") || strings.Contains(text, "test") || strings.Contains(text, "release") || strings.Contains(text, "deploy") {
			score += 10
		}
	}

	tokens := strings.FieldsFunc(pLower, func(r rune) bool {
		return r == '/' || r == '.' || r == '_' || r == '-'
	})
	for _, tok := range tokens {
		// Skip "test" token since we handle test files explicitly above
		if tok == "test" {
			continue
		}
		if len(tok) > 2 && strings.Contains(text, tok) {
			score += 2
		}
	}
	return score
}

func slugify(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "(", "")
	s = strings.ReplaceAll(s, ")", "")
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, ".", "-")
	return s
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
			if f == "plan_gap" || strings.HasSuffix(f, "/plan_gap") || strings.HasSuffix(f, "plan_gap") || strings.EqualFold(f, "plan_gap") {
				problems = append(problems, fmt.Sprintf("%s contains placeholder %q — must be a real test file path", scope, f))
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
		base := filepath.Base(f)
		if prev, ok := byBase[base]; ok && prev != f {
			// Only flag if they're in the SAME directory (actual collision)
			if filepath.Dir(prev) == filepath.Dir(f) {
				problems = append(problems, fmt.Sprintf("layout drift: %q and %q share basename %q in same directory — required files disagree on layout", prev, f, base))
			}
			continue
		}
		byBase[base] = f
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
func mergeArchWinsOnConflict(specPaths, archPaths []string, specDirPrefixes map[string]bool) []string {
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
	// Track basenames that come from SPEC (or SPEC-overridden-by-arch).
	// This allows architecture to provide multiple files with the same basename
	// (e.g., __init__.py in different packages) as long as SPEC doesn't
	// define that basename. SPEC is the canonical layout; architecture can
	// freely add files not in SPEC.
	specBase := map[string]bool{}
	// Compute SPEC file prefixes (directories that actually contain files in SPEC).
	// Only these prefixes block architecture files — intermediate dirs like "backend/"
	// that only contain subdirectories in SPEC should NOT block "backend/app/..." files.
	// Also exclude the layout root itself (e.g., "finally/") since it's just the common prefix.
	specFileDirs := map[string]bool{}
	layoutRoot := ""
	if len(specPaths) > 0 {
		layoutRoot = inferLayoutRootFromPaths(specPaths)
	}
	for _, p := range specPaths {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		dir := filepath.Dir(p)
		if dir != "." && dir != layoutRoot {
			specFileDirs[dir+"/"] = true
		}
	}
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
		specBase[filepath.Base(p)] = true
		if arch, ok := archByBase[filepath.Base(p)]; ok && arch != p {
			add(arch) // architecture wins on conflict
			continue
		}
		add(p)
	}
	// Add arch paths that either:
	// - have a basename not in SPEC (new files), OR
	// - are the architecture override for a SPEC basename (already added above)
	for _, p := range archPaths {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if p == "" || seen[p] {
			continue
		}
		// Allow if basename not in SPEC
		if !specBase[filepath.Base(p)] {
			// But reject if it falls under a SPEC file directory (where SPEC actually has files).
			// This prevents duplicates like backend/db/schema.py when SPEC has backend/db/.
			if specDirPrefixes != nil {
				underSpecFileDir := false
				for prefix := range specFileDirs {
					if strings.HasPrefix(p, prefix) {
						underSpecFileDir = true
						break
					}
				}
				if underSpecFileDir {
					continue // skip architecture duplicate under SPEC file directory
				}
			}
			add(p)
		}
	}
	return out
}
func ProbeExtractSpecLayoutPaths(dir string) ([]string, bool, map[string]bool) {
	paths, ok, prefixes := extractSpecLayoutPaths(dir)
	return paths, ok, prefixes
}
