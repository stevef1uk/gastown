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

func qaCommandWritesPath(cmd, relPath string) bool {
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	if relPath == "" {
		return false
	}
	if !commandMentionsPath(cmd, relPath) {
		return false
	}
	lower := strings.ToLower(cmd)
	writeMarkers := []string{
		"sed -i", "sed -i'", `sed -i"`, "sed -i ",
		"cat >", "cat>>", " tee ", "patch ", "patch -p",
		"mv ", "cp ", "touch ", "install ", "truncate ",
		"sponge ", "perl -pi", "perl -pe",
	}
	for _, m := range writeMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	for _, m := range qaShellRedirectRE.FindAllStringSubmatch(cmd, -1) {
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
