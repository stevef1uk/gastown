package orchestrator

import (
	"path/filepath"
	"regexp"
	"strings"
)

var qaShellRedirectRE = regexp.MustCompile(`(?i)(?:^|[|;&]\s*|&&\s*)>\s*([^\s|;&<>]+)`)

// QACommandMutatesLayoutSource reports when a shell command would write under layout_root
// or any profile required_files implementation path (GT-VERIFY-005).
func QACommandMutatesLayoutSource(cmd string, v WorkflowValidation) (path string, ok bool) {
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if p := ExtractImplementWritePathFromCmd(cmd, layout); p != "" {
		return p, true
	}
	for _, rel := range append(append([]string(nil), v.RequiredFiles...), v.UnionRequiredFiles()...) {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" || isQAAllowedDocPath(rel) {
			continue
		}
		if qaCommandWritesPath(cmd, rel) {
			return rel, true
		}
	}
	return "", false
}

func isQAAllowedDocPath(rel string) bool {
	base := strings.ToLower(filepath.Base(rel))
	switch base {
	case "spec.md", "architecture.md", "plan.md":
		return true
	}
	return false
}

var qaCmdSplitRE = regexp.MustCompile(`(?:\s*(?:&&|\|\||[;&\n])\s*|\(|\))`)

func qaCommandWritesPath(cmd, relPath string) bool {
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	if relPath == "" {
		return false
	}
	for _, stmt := range qaCmdSplitRE.Split(cmd, -1) {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if !commandMentionsPath(stmt, relPath) {
			continue
		}
		if statementWritesPath(stmt, relPath) {
			return true
		}
	}
	return false
}

func statementWritesPath(stmt, relPath string) bool {
	lower := strings.ToLower(stmt)
	writeMarkers := []string{
		"sed -i", "sed -i'", `sed -i"`, "sed -i ",
		"cat >", "cat>>", " tee ", "patch ", "patch -p",
		"mv ", "cp ", "touch ", "truncate ",
		"sponge ", "perl -pi", "perl -pe",
	}
	for _, m := range writeMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	// Check for coreutils install command (avoid false positives on npm/pnpm/yarn/pip/brew/apt install)
	if strings.HasPrefix(lower, "install ") || strings.Contains(lower, "/install ") {
		return true
	}
	for _, m := range qaShellRedirectRE.FindAllStringSubmatch(stmt, -1) {
		if len(m) < 2 {
			continue
		}
		target := filepath.ToSlash(strings.Trim(m[1], `"'`))
		if commandMentionsPath(target, relPath) || target == relPath {
			return true
		}
	}
	return false
}

func commandMentionsPath(cmd, relPath string) bool {
	if strings.Contains(cmd, relPath) {
		return true
	}
	if i := strings.Index(relPath, "/"); i >= 0 {
		short := relPath[i+1:]
		if short != "" && strings.Contains(cmd, short) {
			return true
		}
	}
	return false
}
