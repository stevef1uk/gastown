package orchestrator

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// Defaults for language-agnostic stub detection (QA / implementation guards).
const (
	DefaultMinImplementationFileBytes int64 = 400
	MinImplementationFileBytesFloor   int64 = 80
	MaxMinImplementationFileBytes     int64 = 8192
	DefaultMinSubstantiveLines        int   = 3
)

// StubCheckOptions configures heuristic stub detection for a source file.
type StubCheckOptions struct {
	MinFileBytes        int64
	MinSubstantiveLines int
}

// StubCheckOptionsFromValidation returns effective stub-check settings from a profile.
func StubCheckOptionsFromValidation(v WorkflowValidation) StubCheckOptions {
	minBytes := v.MinImplementationFileBytes
	if minBytes <= 0 {
		minBytes = DefaultMinImplementationFileBytes
	}
	minLines := v.MinSubstantiveLines
	if minLines <= 0 {
		minLines = DefaultMinSubstantiveLines
	}
	return StubCheckOptions{MinFileBytes: minBytes, MinSubstantiveLines: minLines}
}

var (
	htmlTagRe      = regexp.MustCompile(`(?is)<[^>]+>`)
	stubOnlyTextRe = regexp.MustCompile(`(?i)^(hello(\s+world)?|world|test|stub|placeholder|todo|fixme|tbd|not\s+implemented|coming\s+soon)\.?$`)
	trivialCodeRe  = regexp.MustCompile(`(?i)^(pass|\.\.\.|nil|null|undefined|void|return\s*;?|noop|no-op)\s*;?$`)
)

// sourceFileExtensions lists extensions scanned under layout_root (language-agnostic set).
var sourceFileExtensions = map[string]bool{
	".py": true, ".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".tsx": true,
	".jsx": true, ".html": true, ".htm": true, ".css": true, ".scss": true,
	".go": true, ".rs": true, ".java": true, ".kt": true, ".scala": true,
	".rb": true, ".php": true, ".c": true, ".cc": true, ".cpp": true, ".h": true, ".hpp": true,
	".cs": true, ".swift": true, ".vue": true, ".svelte": true, ".sh": true, ".sql": true,
	".lua": true, ".zig": true, ".ex": true, ".exs": true, ".clj": true, ".hs": true,
}

// configFileExtensions are small manifest files (requirements, lockfiles) — non-empty only.
var configFileExtensions = map[string]bool{
	".txt": true, ".json": true, ".yaml": true, ".yml": true, ".toml": true, ".ini": true, ".cfg": true,
	".lock": true, ".mod": true, ".sum": true,
}

// webAssetExtension marks extensions that are naturally small (HTML/CSS/JS/SCSS) — non-empty with ≥1 line.
var webAssetExtension = map[string]bool{
	".html": true, ".htm": true, ".css": true, ".scss": true,
	".js": true, ".mjs": true, ".cjs": true,
}

// shellScriptExtension marks shell/PowerShell scripts that are often short utility wrappers.
var shellScriptExtension = map[string]bool{
	".sh": true, ".bash": true, ".zsh": true, ".fish": true, ".ps1": true,
}

// dependencyManifestNames are lockfiles and dependency lists — non-empty only, no min byte/line counts.
var dependencyManifestNames = map[string]bool{
	"requirements.txt":      true,
	"requirements-dev.txt":  true,
	"requirements-test.txt": true,
	"constraints.txt":       true,
	"pyproject.toml":        true,
	"poetry.lock":           true,
	"Pipfile":               true,
	"Pipfile.lock":          true,
	"go.mod":                true,
	"go.sum":                true,
	"go.work":               true,
	"go.work.sum":           true,
	"package.json":          true,
	"package-lock.json":     true,
	"yarn.lock":             true,
	"pnpm-lock.yaml":        true,
	"npm-shrinkwrap.json":   true,
	"Cargo.toml":            true,
	"Cargo.lock":            true,
	"composer.json":         true,
	"composer.lock":         true,
	"Gemfile":               true,
	"Gemfile.lock":          true,
}

// smallConfigFileNames are dotfiles / small example configs that are inherently
// short (e.g. .env.example). They are verified by existence and a few key tokens
// rather than by implementation-size heuristics.
var smallConfigFileNames = map[string]bool{
	".env.example":        true,
	".env":                true,
	".gitignore":          true,
	".dockerignore":       true,
	".gitattributes":      true,
	".editorconfig":       true,
	"postcss.config.js":   true,
	"postcss.config.ts":   true,
	"tailwind.config.js":  true,
	"tailwind.config.ts":  true,
	"next.config.js":      true,
	"next.config.ts":      true,
	"jest.config.js":      true,
	"jest.config.ts":      true,
	".eslintrc.js":        true,
	".eslintrc.json":      true,
	".eslintrc.yaml":      true,
	".prettierrc.js":      true,
	".prettierrc.json":    true,
	"webpack.config.js":   true,
	"vite.config.js":      true,
	"rollup.config.js":    true,
	"karma.conf.js":       true,
}

// IsPackageInitFile reports package entrypoints that are intentionally minimal (e.g. Python __init__.py).
func IsPackageInitFile(displayRel string) bool {
	base := filepath.Base(filepath.ToSlash(strings.TrimSpace(displayRel)))
	return base == "__init__.py"
}

// IsStructuralInfraPath reports paths that are infrastructure/structural files not tied to
// any specific implement bead. These are allowed to be written freely regardless of bead queue
// order (e.g. Python __init__.py package markers, go.mod, go.sum).
func IsStructuralInfraPath(relPath string) bool {
	if IsPackageInitFile(relPath) {
		return true
	}
	base := filepath.Base(filepath.ToSlash(strings.TrimSpace(relPath)))
	switch base {
	case "go.mod", "go.sum":
		return true
	}
	return false
}

// IsDependencyManifest reports whether rel is a dependency/manifest file (size guards do not apply).
func IsDependencyManifest(displayRel string) bool {
	base := filepath.Base(filepath.ToSlash(strings.TrimSpace(displayRel)))
	if dependencyManifestNames[base] {
		return true
	}
	lower := strings.ToLower(base)
	return dependencyManifestNames[lower]
}

// ValidateRequiredFilesNotStubbed checks only v.RequiredFiles (no layout tree walk).
func ValidateRequiredFilesNotStubbed(rigDir string, v WorkflowValidation) error {
	opts := StubCheckOptionsFromValidation(v)
	for _, rel := range v.RequiredFiles {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" {
			continue
		}
		path := ResolveRequiredFileOnDisk(rigDir, rel, v.LayoutRoot)
		if IsBareDirectoryPath(rel) {
			info, err := os.Stat(path)
			if err != nil || !info.IsDir() {
				return fmt.Errorf("%s missing or not a directory", rel)
			}
			entries, err := os.ReadDir(path)
			if err != nil || len(entries) == 0 {
				return fmt.Errorf("%s is an empty directory", rel)
			}
			continue
		}
		if err := CheckPathNotStub(path, rel, optsForPath(rel, opts)); err != nil {
			return err
		}
	}
	return nil
}

// UnionStubArtifactsOnDisk returns union required files that exist but still fail stub checks.
func UnionStubArtifactsOnDisk(rigDir string, v WorkflowValidation) []string {
	chk := v
	if v.HasPhasedDelivery() {
		chk.RequiredFiles = v.UnionRequiredFiles()
	}
	opts := StubCheckOptionsFromValidation(chk)
	var stubbed []string
	for _, rel := range chk.RequiredFiles {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" {
			continue
		}
		path := ResolveRequiredFileOnDisk(rigDir, rel, chk.LayoutRoot)
		if IsBareDirectoryPath(rel) {
			info, err := os.Stat(path)
			if err != nil || !info.IsDir() {
				stubbed = append(stubbed, rel)
				continue
			}
			entries, err := os.ReadDir(path)
			if err != nil || len(entries) == 0 {
				stubbed = append(stubbed, rel)
			}
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if err := CheckContentNotStub(data, rel, optsForPath(rel, opts)); err != nil {
			stubbed = append(stubbed, rel)
		}
	}
	return stubbed
}

// ValidateWorkNotStubbed rejects placeholder implementations under the rig worktree.
// Checks required_files and, when layout_root is set, other source files in that tree.
func ValidateWorkNotStubbed(rigDir string, v WorkflowValidation) error {
	opts := StubCheckOptionsFromValidation(v)
	checked := make(map[string]bool)

	for _, rel := range v.RequiredFiles {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" {
			continue
		}
		path := ResolveRequiredFileOnDisk(rigDir, rel, v.LayoutRoot)
		checked[path] = true
		if err := CheckPathNotStub(path, rel, optsForPath(rel, opts)); err != nil {
			return err
		}
	}

	// Phased rigs validate explicit union paths at success time; skip layout walk so
	// earlier-phase stubs are fixed via rework writes instead of blocking unrelated work.
	if v.HasPhasedDelivery() {
		return nil
	}

	layout := strings.TrimSpace(v.LayoutRoot)
	if layout == "" {
		return nil
	}
	layoutDir := filepath.Join(rigDir, filepath.FromSlash(layout))
	info, err := os.Stat(layoutDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return nil
	}

	return filepath.WalkDir(layoutDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			base := d.Name()
			if base == ".git" || base == "node_modules" || base == "__pycache__" || base == ".gastown" || base == ".venv" || base == "venv" {
				return filepath.SkipDir
			}
			return nil
		}
		if checked[path] {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if !sourceFileExtensions[ext] && !configFileExtensions[ext] {
			return nil
		}
		if info, err := d.Info(); err == nil && info.Size() > 512*1024 {
			return nil // skip very large files
		}
		rel, err := filepath.Rel(rigDir, path)
		if err != nil {
			rel = path
		}
		checked[path] = true
		return CheckPathNotStub(path, filepath.ToSlash(rel), optsForPath(filepath.ToSlash(rel), opts))
	})
}

func optsForPath(displayRel string, opts StubCheckOptions) StubCheckOptions {
	if IsPackageInitFile(displayRel) {
		return StubCheckOptions{MinFileBytes: 0, MinSubstantiveLines: 0}
	}
	if IsDependencyManifest(displayRel) {
		return StubCheckOptions{MinFileBytes: 1, MinSubstantiveLines: 0}
	}
	base := strings.ToLower(filepath.Base(filepath.ToSlash(strings.TrimSpace(displayRel))))
	if smallConfigFileNames[base] {
		relaxed := opts
		relaxed.MinFileBytes = 1
		if relaxed.MinSubstantiveLines > 1 {
			relaxed.MinSubstantiveLines = 1
		}
		return relaxed
	}
	ext := strings.ToLower(filepath.Ext(displayRel))
	// Web assets are naturally small — accept non-empty with at least 1 substantive line.
	if webAssetExtension[ext] {
		relaxed := opts
		if relaxed.MinFileBytes > 80 {
			relaxed.MinFileBytes = 80
		}
		if relaxed.MinSubstantiveLines > 1 {
			relaxed.MinSubstantiveLines = 1
		}
		return relaxed
	}
	if shellScriptExtension[ext] {
		relaxed := opts
		if relaxed.MinFileBytes > 80 {
			relaxed.MinFileBytes = 80
		}
		if relaxed.MinSubstantiveLines > 1 {
			relaxed.MinSubstantiveLines = 1
		}
		return relaxed
	}
	if !configFileExtensions[ext] {
		return opts
	}
	relaxed := opts
	relaxed.MinFileBytes = MinImplementationFileBytesFloor
	if relaxed.MinSubstantiveLines > 1 {
		relaxed.MinSubstantiveLines = 1
	}
	return relaxed
}

// CheckPathNotStub reads path and applies language-agnostic stub heuristics.
func CheckPathNotStub(path, displayRel string, opts StubCheckOptions) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", displayRel, err)
	}
	return CheckContentNotStub(data, displayRel, opts)
}

// CheckContentNotStub applies stub heuristics to file bytes.
func CheckContentNotStub(data []byte, displayRel string, opts StubCheckOptions) error {
	// Placeholder files like .gitkeep, .gitignore are intentionally minimal.
	if IsPlaceholderFile(displayRel) {
		return nil
	}
	if IsPackageInitFile(displayRel) {
		return nil
	}
	// Dependency manifests like go.sum can legitimately be empty
	// when no external dependencies are used (e.g., modernc.org/sqlite)
	if IsDependencyManifest(displayRel) {
		return nil
	}
	if len(data) == 0 {
		return fmt.Errorf("%s is empty (stub/placeholder)", displayRel)
	}
	if opts.MinSubstantiveLines <= 0 {
		// Dependency manifests (requirements.txt, go.mod, lockfiles): non-empty is enough.
		return nil
	}

	text := string(data)
	substantive := substantiveLines(text)
	if len(substantive) < opts.MinSubstantiveLines {
		return fmt.Errorf("%s has only %d substantive line(s) (need ≥%d); likely a stub", displayRel, len(substantive), opts.MinSubstantiveLines)
	}

	visible := strings.TrimSpace(visibleText(text))
	if visible != "" && len(visible) < 24 && stubOnlyTextRe.MatchString(visible) {
		return fmt.Errorf("%s visible text is trivial %q (stub/placeholder)", displayRel, visible)
	}
	if opts.MinFileBytes <= MinImplementationFileBytesFloor+1 && len(substantive) >= 1 && len(visible) == 0 {
		// Config/manifest file: non-empty with at least one line is enough.
		return nil
	}

	// Single-line or few-line files that are only a trivial code token.
	if len(substantive) <= 2 {
		joined := strings.ToLower(strings.Join(substantive, " "))
		if trivialCodeRe.MatchString(strings.TrimSpace(joined)) {
			return fmt.Errorf("%s is a trivial code stub (%q)", displayRel, strings.TrimSpace(joined))
		}
	}

	if strings.HasSuffix(strings.ToLower(displayRel), ".js") {
		if jsHasServerSideCalls(text) {
			return fmt.Errorf("%s calls server-side-only functions (e.g. InitSchema) — these belong in Go code, not frontend JS", displayRel)
		}
	}

	minBytes := opts.MinFileBytes
	if looksSubstantiveImplementation(text, substantive, opts) || IsFrontendImplementPath(displayRel) {
		// Small but complete modules or frontend files may be well under 400 bytes.
		if minBytes > MinImplementationFileBytesFloor {
			minBytes = MinImplementationFileBytesFloor
		}
	}
	if int64(len(data)) < minBytes {
		return fmt.Errorf("%s is only %d bytes (minimum %d for non-stub work)", displayRel, len(data), minBytes)
	}

	// Mostly comments/whitespace with almost no code (only when not clearly substantive).
	if !looksSubstantiveImplementation(text, substantive, opts) {
		nonWS := strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, text)
		if len(nonWS) > 0 && int64(len(nonWS)) < opts.MinFileBytes/2 {
			return fmt.Errorf("%s has too little non-whitespace content (%d bytes)", displayRel, len(nonWS))
		}
	}

	return nil
}

// looksSubstantiveImplementation detects small but real code (not hello-world / pass stubs).
func looksSubstantiveImplementation(text string, substantive []string, opts StubCheckOptions) bool {
	if len(substantive) < opts.MinSubstantiveLines {
		return false
	}
	joined := strings.ToLower(strings.Join(substantive, "\n"))
	if trivialCodeRe.MatchString(strings.TrimSpace(strings.Join(substantive, " "))) {
		return false
	}
	visible := strings.TrimSpace(visibleText(text))
	if visible != "" && len(visible) < 24 && stubOnlyTextRe.MatchString(visible) {
		return false
	}
	signals := 0
	for _, pat := range []string{
		"def ", "function ", "func ", "fn ", "class ", "struct ",
		"if ", "elif ", "else if", "else:", "for ", "while ", "switch ",
		"return ", "=>", "export ", "import ", "from ", "package ",
	} {
		if strings.Contains(joined, pat) {
			signals++
		}
	}
	if signals >= 2 {
		return true
	}
	return signals >= 1 && len(substantive) >= opts.MinSubstantiveLines+2
}

func visibleText(s string) string {
	s = htmlTagRe.ReplaceAllString(s, " ")
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(s)
}

func substantiveLines(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if isCommentOnlyLine(line) {
			continue
		}
		out = append(out, line)
	}
	return out
}

func isCommentOnlyLine(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	switch {
	case strings.HasPrefix(lower, "#"):
		return true
	case strings.HasPrefix(lower, "//"):
		return true
	case strings.HasPrefix(lower, "/*") && strings.HasSuffix(lower, "*/"):
		return true
	case strings.HasPrefix(lower, "<!--") && strings.Contains(lower, "-->"):
		return true
	case lower == "*" || strings.HasPrefix(lower, "* "):
		return true
	case lower == "{" || lower == "}" || lower == "};":
		return true
	}
	return false
}

var esModuleImportRE = regexp.MustCompile(`(?m)^\s*import\s+`)

func jsHasESModuleImports(text string) bool {
	return esModuleImportRE.MatchString(text) || strings.Contains(text, "export ")
}

var serverSideFuncRE = regexp.MustCompile(`\b(InitSchema|DB\.Exec|sql\.Open|store\.DB)\b`)

func jsHasServerSideCalls(text string) bool {
	return serverSideFuncRE.MatchString(text)
}
